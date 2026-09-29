package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"

	"horizon/internal/auth"
	"horizon/internal/ratelimit"
	"horizon/internal/store"
)

// 让 fmt/time 保持被引用（下面多个用例会用到）。
var (
	_ = fmt.Sprintf
	_ = time.Minute
)

func newTestServer(t *testing.T) *gin.Engine {
	st, am := newTestServerParts(t)
	// engine 传 nil：黑名单/管理/指标路径在到达引擎前即返回，不涉及引擎。
	return NewRouter(st, nil, am)
}

// newTestServerParts 只准备存储与鉴权，供需要追加 Option（如元数据抓取）的用例复用。
func newTestServerParts(t *testing.T) (store.Backend, *auth.Manager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return st, auth.New("testsecret")
}

// newTestServerWithStore 与 newTestServer 相同，但把存储层一并返回。
//
// 越权用例需要往「同一个」store 里造一条属于某人的任务：
// 下载引擎是 nil，没法通过 addTorrent 建任务，
// 而另起一个 store 的话路由层根本看不到这条记录。
func newTestServerWithStore(t *testing.T) (*gin.Engine, store.Backend) {
	t.Helper()
	st, am := newTestServerParts(t)
	return NewRouter(st, nil, am), st
}

func doJSON(t *testing.T, r *gin.Engine, method, path, token string, body map[string]string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var m map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func TestSecurityCompliance(t *testing.T) {
	r := newTestServer(t)

	// 首个用户 → 管理员；第二个 → 普通用户
	_, b1 := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "admin", "password": "admin123"})
	adminToken, _ := b1["token"].(string)
	_, b2 := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "user2", "password": "user12345"})
	userToken, _ := b2["token"].(string)
	if adminToken == "" || userToken == "" {
		t.Fatalf("注册未返回 token")
	}

	// 普通用户访问管理员接口 → 403
	w, _ := doJSON(t, r, "GET", "/api/v1/admin/blocked-hashes", userToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("非管理员访问 admin API = %d, want 403", w.Code)
	}

	// 添加信息哈希黑名单（管理员）→ 201
	w, _ = doJSON(t, r, "POST", "/api/v1/admin/blocked-hashes", adminToken,
		map[string]string{"info_hash": "63dde13a0ebeabf4ee7e682563184ade0cdbe3c4", "reason": "DMCA"})
	if w.Code != http.StatusCreated {
		t.Fatalf("添加哈希黑名单 = %d, want 201", w.Code)
	}

	// 被哈希拦截 → 403
	w, _ = doJSON(t, r, "POST", "/api/v1/torrents", userToken,
		map[string]string{"magnet": "magnet:?xt=urn:btih:63DDE13A0EBEABF4EE7E682563184ADE0CDBE3C4"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("被哈希黑名单拦截 = %d, want 403", w.Code)
	}

	// 添加关键词黑名单 → 201
	w, _ = doJSON(t, r, "POST", "/api/v1/admin/blocked-keywords", adminToken, map[string]string{"keyword": "dmcatest"})
	if w.Code != http.StatusCreated {
		t.Fatalf("添加关键词黑名单 = %d, want 201", w.Code)
	}

	// 被关键词拦截 → 403
	w, _ = doJSON(t, r, "POST", "/api/v1/torrents", userToken,
		map[string]string{"magnet": "magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&dn=dmcatest-file"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("被关键词黑名单拦截 = %d, want 403", w.Code)
	}

	// 审计日志（管理员）→ 200，且有拦截记录
	w, m := doJSON(t, r, "GET", "/api/v1/admin/audit", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("审计日志 = %d, want 200", w.Code)
	}
	if audit, _ := m["audit"].([]interface{}); len(audit) < 2 {
		t.Fatalf("审计日志应至少含 2 条拦截记录，实际 %v", audit)
	}

}

// metrics（运行指标）含用户数与任务数，必须登录 + 管理员才可读。
// 匿名可读时任何人都能探测服务规模，属于信息泄露。
func TestMetricsRequiresAdmin(t *testing.T) {
	r := newTestServer(t)

	_, b1 := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "admin", "password": "admin123"})
	adminToken, _ := b1["token"].(string)
	_, b2 := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "user2", "password": "user12345"})
	userToken, _ := b2["token"].(string)

	w, _ := doJSON(t, r, "GET", "/api/v1/admin/metrics", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("匿名访问 metrics = %d, want 401", w.Code)
	}
	w, _ = doJSON(t, r, "GET", "/api/v1/admin/metrics", userToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("普通用户访问 metrics = %d, want 403", w.Code)
	}
	w, body := doJSON(t, r, "GET", "/api/v1/admin/metrics", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("管理员访问 metrics = %d, want 200", w.Code)
	}
	_ = body
	if !strings.Contains(w.Body.String(), "horizon_users") {
		t.Fatalf("metrics 输出应含 horizon_users，实际 %s", w.Body.String())
	}
	// 旧的公开路径必须不再存在
	w, _ = doJSON(t, r, "GET", "/metrics", "", nil)
	if w.Code == http.StatusOK {
		t.Fatal("/metrics 不应再公开可读")
	}
}

// ============ 注册限流 ============
func TestRegisterRateLimited(t *testing.T) {
	st, am := newTestServerParts(t)
	r := NewRouter(st, nil, am, WithRateLimiter(ratelimit.NewMemoryLimiter(2, time.Minute)))

	for i := 0; i < 2; i++ {
		w, _ := doJSON(t, r, "POST", "/api/v1/auth/register", "",
			map[string]string{"username": fmt.Sprintf("rluser%d", i), "password": "pass12345"})
		if w.Code != http.StatusCreated {
			t.Fatalf("第 %d 次注册 = %d, want 201", i+1, w.Code)
		}
	}
	w, _ := doJSON(t, r, "POST", "/api/v1/auth/register", "",
		map[string]string{"username": "rluser3", "password": "pass12345"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 3 次注册 = %d, want 429", w.Code)
	}
}

// ============ 越权（IDOR）回归 ============
//
// 背景：patchTorrent / deleteTorrent 曾经直接拿 URL 里的 infoHash 操作引擎，
// 不校验归属。任何登录用户只要知道 infoHash（公开搜索接口就会返回）
// 就能删掉/暂停别人的下载。这里把三种越权路径都固定成回归用例。
func TestTorrentOwnershipIsolated(t *testing.T) {
	r, st := newTestServerWithStore(t)

	_, a := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "alice", "password": "alice12345"})
	alice, _ := a["token"].(string)
	_, b := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "bobby", "password": "bobby12345"})
	bob, _ := b["token"].(string)
	if alice == "" || bob == "" {
		t.Fatal("注册未拿到 token")
	}

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	owner, err := st.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("查 alice 失败: %v", err)
	}
	if err := st.CreateTask(&store.Task{
		OwnerID:  owner.ID,
		InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash,
		Status: "resolving", Name: "alice-movie",
	}); err != nil {
		t.Fatalf("造任务失败: %v", err)
	}

	// bob 读 alice 的任务 → 404（不暴露是否存在）
	w, _ := doJSON(t, r, "GET", "/api/v1/torrents/"+hash, bob, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob 读 alice 的任务 = %d, want 404", w.Code)
	}
	// bob 暂停 alice 的任务 → 404
	w, _ = doJSON(t, r, "PATCH", "/api/v1/torrents/"+hash, bob, map[string]string{"action": "pause"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob 暂停 alice 的任务 = %d, want 404", w.Code)
	}
	// bob 删除 alice 的任务 → 404
	w, _ = doJSON(t, r, "DELETE", "/api/v1/torrents/"+hash, bob, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob 删除 alice 的任务 = %d, want 404", w.Code)
	}

	// 确认任务仍在
	w, _ = doJSON(t, r, "GET", "/api/v1/torrents/"+hash, alice, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("越权尝试后 alice 的任务应仍存在，实际 %d", w.Code)
	}

	// alice 自己操作 → 允许
	w, _ = doJSON(t, r, "PATCH", "/api/v1/torrents/"+hash, alice, map[string]string{"action": "pause"})
	if w.Code != http.StatusOK {
		t.Fatalf("本人暂停 = %d, want 200", w.Code)
	}
	w, _ = doJSON(t, r, "DELETE", "/api/v1/torrents/"+hash, alice, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("本人删除 = %d, want 200", w.Code)
	}
}

// ============ 登录失败锁定 ============
func TestLoginLockout(t *testing.T) {
	r := newTestServer(t)
	_, b := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "carol", "password": "carol12345"})
	if tok, _ := b["token"].(string); tok == "" {
		t.Fatal("注册失败")
	}

	var last int
	for i := 0; i < maxLoginFails; i++ {
		w, _ := doJSON(t, r, "POST", "/api/v1/auth/login", "", map[string]string{"username": "carol", "password": "wrong-pass"})
		last = w.Code
	}
	if last != http.StatusUnauthorized {
		t.Fatalf("前 %d 次错误密码应为 401，实际 %d", maxLoginFails, last)
	}
	// 达到阈值后被锁定
	w, _ := doJSON(t, r, "POST", "/api/v1/auth/login", "", map[string]string{"username": "carol", "password": "wrong-pass"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过失败上限后应被锁定 = %d, want 429", w.Code)
	}
	// 锁定期内即使密码正确也拒绝
	w, _ = doJSON(t, r, "POST", "/api/v1/auth/login", "", map[string]string{"username": "carol", "password": "carol12345"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内正确密码也应被拒 = %d, want 429", w.Code)
	}
}

// ============ refresh token ============
func TestRefreshTokenFlow(t *testing.T) {
	r := newTestServer(t)
	_, b := doJSON(t, r, "POST", "/api/v1/auth/register", "", map[string]string{"username": "dave", "password": "dave12345"})
	access, _ := b["token"].(string)
	refresh, _ := b["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatal("注册应同时返回 token 与 refresh_token")
	}
	w, body := doJSON(t, r, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh = %d, want 200", w.Code)
	}
	if newTok, _ := body["token"].(string); newTok == "" {
		t.Fatal("refresh 应返回新的 access token")
	}
	// access token 不能当 refresh token 用
	w, _ = doJSON(t, r, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": access})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("用 access token 换新令牌 = %d, want 401", w.Code)
	}
}

// ============ 公开接口限流 ============
func TestPublicEndpointRateLimited(t *testing.T) {
	st, am := newTestServerParts(t)
	r := NewRouter(st, nil, am, WithPublicRateLimit(2, 100))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/dytt/latest", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("第 %d 次不该被限流", i+1)
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/dytt/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过公开配额应 429，实际 %d", w.Code)
	}
}
