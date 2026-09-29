package api

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"horizon/internal/auth"
	"horizon/internal/dytt"
	"horizon/internal/engine"
	"horizon/internal/guard"
	"horizon/internal/ratelimit"
	"horizon/internal/store"
	"horizon/internal/version"
	"horizon/internal/tmdb"
	"horizon/internal/torsearch"
)

// rootHTML 是根路径的状态页。
const rootHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>地平线磁力下载 · 云端服务</title>
</head>
<body style="font-family:system-ui,Segoe UI,Arial,sans-serif;max-width:680px;margin:40px auto;padding:0 18px;color:#1f2328;background:#f6f8fa">
<h1 style="font-size:22px">云端服务运行中</h1>
<p>这是「地平线磁力下载」的下载引擎 API 后台，本身<strong>没有网页界面</strong>。请在桌面客户端「地平线磁力下载.exe」里登录使用。</p>
<h2 style="font-size:16px">常用接口</h2>
<ul style="line-height:1.9">
  <li><a href="/healthz">/healthz</a> — 健康检查</li>
  <li><code>POST /api/v1/auth/register</code> — 注册（首个用户自动成为管理员）</li>
  <li><code>POST /api/v1/auth/login</code> — 登录</li>
  <li><code>POST /api/v1/auth/refresh</code> — 用 refresh token 换新 token</li>
  <li><code>POST /api/v1/torrents</code> — 新建下载任务（需 Bearer Token）</li>
  <li><code>GET  /api/v1/torrents</code> — 下载任务列表</li>
  <li><code>WS   /api/v1/ws/progress</code> — 实时进度推送</li>
</ul>
</body>
</html>`

type Server struct {
	store store.Backend
	eng   *engine.Engine
	auth  *auth.Manager
	rl    ratelimit.Limiter

	// pubRL 限制公开元数据接口（search / dytt list|latest|search|detail / top）。
	// 这些接口每次都会外呼上游索引站与翻译接口，不限流会被刷爆并打满上游配额。
	pubRL ratelimit.Limiter
	// coverRL 单独一档：封面是每张图片一次请求，配额需要比元数据宽得多。
	coverRL ratelimit.Limiter

	// loginFails 记录连续登录失败，用于账号级锁定。
	// 之前只有客户端本地回退路径有锁定，走云端登录反而可以无限慢速试密码。
	loginMu    sync.Mutex
	loginFails map[string]*failState

	// topCache 缓存「近期热门」结果，避免每次请求都同步打上游。
	topMu    sync.Mutex
	topBody  []byte
	topUntil time.Time

	dytt         *dytt.Service
	tmdb         *tmdb.Client
	tmdbFallback *tmdb.Client
	torsearch    *torsearch.Searcher
}

// failState 记录某个账号的失败次数与解锁时间。
type failState struct {
	count   int
	until   time.Time
	updated time.Time
}

type Option func(*Server)

// WithRateLimiter 注入登录/注册限流器（nil 表示不限流）。
func WithRateLimiter(l ratelimit.Limiter) Option {
	return func(s *Server) { s.rl = l }
}

// WithPublicRateLimit 覆盖公开接口的每分钟配额（<=0 表示使用默认值）。
func WithPublicRateLimit(meta, cover int) Option {
	return func(s *Server) {
		if meta > 0 {
			s.pubRL = ratelimit.NewMemoryLimiter(meta, time.Minute)
		}
		if cover > 0 {
			s.coverRL = ratelimit.NewMemoryLimiter(cover, time.Minute)
		}
	}
}

// WithDyttService 注入电影天堂元数据服务；传 nil 表示关闭该功能。
func WithDyttService(svc *dytt.Service) Option {
	return func(s *Server) { s.dytt = svc }
}

// WithTMDb 注入 TMDb 增强客户端：primary 为中文、fallback 为英文兜底。
func WithTMDb(primary, fallback *tmdb.Client) Option {
	return func(s *Server) { s.tmdb, s.tmdbFallback = primary, fallback }
}

// WithTorrentSearcher 注入种子检索器；传 nil 表示使用按环境变量初始化的默认检索器。
func WithTorrentSearcher(sr *torsearch.Searcher) Option {
	return func(s *Server) { s.torsearch = sr }
}

// WithTrustedProxies 声明可信反向代理。
//
// 这件事必须显式做：gin 默认信任所有代理，于是客户端可以自己伪造
// X-Forwarded-For 来让 c.ClientIP() 每次返回不同值，IP 限流形同虚设。
// 只信任本机反代时，XFF 只有真的由反代写入才会被采信。
func WithTrustedProxies(r *gin.Engine, proxies []string) Option {
	return func(s *Server) {
		if len(proxies) == 0 {
			proxies = []string{"127.0.0.1", "::1"}
		}
		_ = r.SetTrustedProxies(proxies)
	}
}

func NewRouter(st store.Backend, eng *engine.Engine, am *auth.Manager, opts ...Option) *gin.Engine {
	s := &Server{
		store:      st,
		eng:        eng,
		auth:       am,
		loginFails: map[string]*failState{},
	}
	r := gin.New()
	for _, o := range opts {
		o(s)
	}
	if s.pubRL == nil {
		s.pubRL = ratelimit.NewMemoryLimiter(120, time.Minute)
	}
	if s.coverRL == nil {
		s.coverRL = ratelimit.NewMemoryLimiter(600, time.Minute)
	}

	r.Use(gin.Logger(), gin.Recovery())

	// /healthz 同时回报版本：客户端据此判断手上这份是不是最新，
	// 不必再去比对文件大小或时间。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "horizon-downloader",
			"version": version.Full(),
		})
	})
	r.GET("/", serveIndex)

	// 公开的种子搜索 / 热门接口（供 Web 页与客户端使用，无需登录）。
	// 一律走 pubRL：这些接口每次都会外呼上游，必须限流。
	r.GET("/api/v1/search", s.publicLimit("search"), s.searchTorrents)
	r.GET("/api/v1/top", s.publicLimit("top"), s.topTorrents)
	// 电影天堂元数据（封面 / 名称 / 简介）：公开接口、无需登录，但同样限流；
	// 封面单独一档配额（每张图一次请求）。
	r.GET("/api/v1/dytt/list", s.publicLimit("dytt"), s.dyttList)
	r.GET("/api/v1/dytt/latest", s.publicLimit("dytt"), s.dyttLatest)
	r.GET("/api/v1/dytt/search", s.publicLimit("dytt"), s.dyttSearch)
	r.GET("/api/v1/dytt/detail", s.publicLimit("dytt"), s.dyttDetail)
	r.GET("/api/v1/dytt/cover", s.coverLimit(), s.dyttCover)

	v1 := r.Group("/api/v1")
	{
		a := v1.Group("/auth")
		a.POST("/register", s.register)
		a.POST("/login", s.login)
		a.POST("/refresh", s.refresh)

		tor := v1.Group("/torrents")
		tor.Use(am.Middleware())
		{
			tor.POST("", s.addTorrent)
			tor.GET("", s.listTorrents)
			tor.GET("/:infoHash", s.getTorrent)
			tor.GET("/:infoHash/file", s.downloadFile)
			tor.PATCH("/:infoHash", s.patchTorrent)
			tor.DELETE("/:infoHash", s.deleteTorrent)
		}

		v1.GET("/ws/progress", am.Middleware(), s.wsProgress)

		adm := v1.Group("/admin")
		adm.Use(am.Middleware(), adminOnly())
		{
			adm.POST("/blocked-hashes", s.addBlockedHash)
			adm.GET("/blocked-hashes", s.listBlockedHashes)
			adm.DELETE("/blocked-hashes/:infoHash", s.removeBlockedHash)
			adm.POST("/blocked-keywords", s.addBlockedKeyword)
			adm.GET("/blocked-keywords", s.listBlockedKeywords)
			adm.DELETE("/blocked-keywords", s.removeBlockedKeyword) // ?keyword=
			adm.GET("/audit", s.listAudit)
			adm.POST("/enforce-blacklist", s.enforceBlacklist)
			// 运行指标含用户数 / 任务数，只给管理员看。
			// 之前挂在公开组，任何人访问 /metrics 都能读到运营数据。
			adm.GET("/metrics", s.metrics)
		}
	}

	return r
}

// publicLimit 对公开元数据接口限流（按 IP，每分钟一档）。
func (s *Server) publicLimit(bucket string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.pubRL != nil && !s.pubRL.Allow(bucket+":"+c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}

// coverLimit 封面代理专用配额（比元数据宽，因为每张图一次请求）。
func (s *Server) coverLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.coverRL != nil && !s.coverRL.Allow("cover:"+c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}

// adminOnly 仅放行管理员。
func adminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		cl := auth.ClaimsFrom(c)
		if cl == nil || !cl.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

type credReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ============ 登录失败锁定 ============

const (
	maxLoginFails  = 5
	loginLockFor   = 10 * time.Minute
	failStateTTL   = 30 * time.Minute
	failKeyMaxSize = 20000
)

// lockKey 把账号与来源 IP 组合成锁定键。
//
// 只用账号名会让攻击者用一个畸形用户名锁死真实用户；
// 只用 IP 会让同一 NAT 后的正常用户被连坐。
// 组合键兼顾两者：针对某个账号的爆破会被锁，
// 而分布式换个 IP 继续试也会因为下面的全局计数被拖慢。
func lockKey(username, ip string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "|" + ip
}

// loginLocked 返回该键是否处于锁定期。
func (s *Server) loginLocked(key string) (bool, time.Duration) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	st, ok := s.loginFails[key]
	if !ok {
		return false, 0
	}
	if !st.until.IsZero() && time.Now().Before(st.until) {
		return true, time.Until(st.until)
	}
	return false, 0
}

// recordLoginFail 记一次失败，达到阈值则锁定。
func (s *Server) recordLoginFail(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	s.sweepFailLocked(now)
	st := s.loginFails[key]
	if st == nil || now.Sub(st.updated) > failStateTTL {
		st = &failState{}
		s.loginFails[key] = st
	}
	st.count++
	st.updated = now
	if st.count >= maxLoginFails {
		st.until = now.Add(loginLockFor)
		st.count = 0
	}
}

// clearLoginFail 登录成功后清零。
func (s *Server) clearLoginFail(key string) {
	s.loginMu.Lock()
	delete(s.loginFails, key)
	s.loginMu.Unlock()
}

// sweepFailLocked 回收长期不活跃的记录，避免 map 无限增长（调用方须持锁）。
func (s *Server) sweepFailLocked(now time.Time) {
	if len(s.loginFails) < failKeyMaxSize {
		return
	}
	for k, st := range s.loginFails {
		if now.Sub(st.updated) > failStateTTL && !now.Before(st.until) {
			delete(s.loginFails, k)
		}
	}
}

func (s *Server) register(c *gin.Context) {
	if s.rl != nil && !s.rl.Allow("reg:"+c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
		return
	}
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名需 3-32 个字符"})
		return
	}
	if len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "密码至少 6 位"})
		return
	}
	taken, _ := s.store.IsUsernameTaken(req.Username)
	if taken {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}
	h, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务内部错误"})
		return
	}
	// 首个注册用户提升为管理员（与本地版首次注册一致）
	count, _ := s.store.UserCount()
	isAdmin := count == 0
	u := &store.User{Username: req.Username, PassHash: h, IsAdmin: isAdmin}
	if err := s.store.CreateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	access, refresh, err := s.auth.IssuePair(u.ID, u.Username, isAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"token":         access,
		"refresh_token": refresh,
		"expires_in":    int(tokenTTLSeconds(access)),
		"user":          gin.H{"id": u.ID, "username": u.Username, "is_admin": isAdmin},
	})
}

func (s *Server) login(c *gin.Context) {
	if s.rl != nil && !s.rl.Allow("login:"+c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
		return
	}
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	key := lockKey(req.Username, c.ClientIP())

	if locked, remain := s.loginLocked(key); locked {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       fmt.Sprintf("连续失败次数过多，账号已锁定，请 %d 分钟后再试", int(remain.Minutes())+1),
			"retry_after": int(remain.Seconds()),
		})
		return
	}

	u, err := s.store.GetUserByUsername(req.Username)
	if err != nil || !auth.CheckPassword(u.PassHash, req.Password) {
		s.recordLoginFail(key)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	s.clearLoginFail(key)

	access, refresh, err := s.auth.IssuePair(u.ID, u.Username, u.IsAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":         access,
		"refresh_token": refresh,
		"expires_in":    int(tokenTTLSeconds(access)),
		"user":          gin.H{"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin},
	})
}

// refresh 用 refresh token 换一对新令牌。
//
// 有了它，短期 access token 到期后客户端可以静默续期，
// 不用把密码留在内存里反复登录。
func (s *Server) refresh(c *gin.Context) {
	if s.rl != nil && !s.rl.Allow("refresh:"+c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.RefreshToken) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 refresh_token"})
		return
	}
	claims, err := s.auth.ParseRefresh(strings.TrimSpace(req.RefreshToken))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh_token 无效或已过期"})
		return
	}
	// 用库里的当前用户信息重新签发，避免权限变更后旧 token 继续沿用旧身份。
	u, err := s.store.GetUserByUsername(claims.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
		return
	}
	access, refresh, err := s.auth.IssuePair(u.ID, u.Username, u.IsAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":         access,
		"refresh_token": refresh,
		"expires_in":    int(tokenTTLSeconds(access)),
		"user":          gin.H{"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin},
	})
}

// tokenTTLSeconds 返回 access token 有效期秒数，用于告知客户端何时续期。
//
// 不解析 token 本身：解析需要密钥且拿不到额外信息，
// 有效期就是签发时用的那个常量。
func tokenTTLSeconds(string) float64 { return auth.AccessTTL().Seconds() }

type addReq struct {
	Magnet string `json:"magnet"`
}

func (s *Server) addTorrent(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	var req addReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.Magnet = strings.TrimSpace(req.Magnet)
	if !strings.HasPrefix(req.Magnet, "magnet:") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持磁力链接（magnet:?xt=urn:btih:...）"})
		return
	}

	// 安全合规：创建任务前校验信息哈希 / 关键词黑名单（管理员豁免）
	ih := guard.ExtractInfoHash(req.Magnet)
	if !cl.IsAdmin {
		if ih != "" {
			if blocked, _ := s.store.IsHashBlocked(ih); blocked {
				s.store.Audit(cl.Username, "blocked", ih, "下载被拒：信息哈希黑名单")
				c.JSON(http.StatusForbidden, gin.H{"error": "该资源已列入黑名单，禁止下载"})
				return
			}
		}
		if kw, hit, _ := s.store.MatchBlockedKeyword(req.Magnet); hit {
			s.store.Audit(cl.Username, "blocked", ih, "下载被拒：命中关键词 "+kw)
			c.JSON(http.StatusForbidden, gin.H{"error": "命中关键词黑名单（" + kw + "），禁止下载"})
			return
		}
	}

	ctxHash, err := s.eng.AddMagnet(req.Magnet)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加任务失败: " + err.Error()})
		return
	}
	t := &store.Task{
		OwnerID:  cl.UserID,
		InfoHash: ctxHash,
		Magnet:   req.Magnet,
		Status:   "resolving",
		Dir:      s.eng.Dir(),
	}
	if err := s.store.CreateTask(t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务入库失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"info_hash": ctxHash, "status": "resolving"})
}

// progress 取任务实时进度；引擎未装配时返回 nil（测试与只读部署下不会 panic）。
func (s *Server) progress(infoHash string) *engine.Progress {
	if s.eng == nil {
		return nil
	}
	p, err := s.eng.Progress(infoHash)
	if err != nil {
		return nil
	}
	return p
}

// removeTask 从引擎移除任务；引擎未装配时静默跳过。
func (s *Server) removeTask(infoHash string) {
	if s.eng == nil {
		return
	}
	_ = s.eng.Remove(infoHash)
}

func (s *Server) listTorrents(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	tasks, err := s.store.ListTasks(cl.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	out := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		if p := s.progress(t.InfoHash); p != nil {
			s.store.UpdateTaskProgress(cl.UserID, t.InfoHash, p.Name, p.Status, p.Total, p.Completed)
			out = append(out, gin.H{
				"info_hash": t.InfoHash, "name": p.Name, "status": p.Status,
				"total": p.Total, "completed": p.Completed, "peers": p.Peers,
				"speed": p.Speed, "created_at": t.CreatedAt,
			})
		} else {
			out = append(out, gin.H{
				"info_hash": t.InfoHash, "name": t.Name, "status": t.Status,
				"total": t.Total, "completed": t.Completed, "peers": 0, "speed": 0, "created_at": t.CreatedAt,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": out})
}

// ownTask 取任务并强制校验归属。
//
// 这是所有按 infoHash 操作的必经入口。之前 patch/delete 直接拿 URL 里的
// hash 去操作引擎，不校验 owner：任何登录用户知道某个 infoHash
// （而 infoHash 就是公开搜索接口直接返回的）就能删掉/暂停别人的下载。
func (s *Server) ownTask(c *gin.Context) (*store.Task, bool) {
	cl := auth.ClaimsFrom(c)
	ih := c.Param("infoHash")
	if strings.TrimSpace(ih) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 info_hash"})
		return nil, false
	}
	t, err := s.store.GetTask(cl.UserID, ih)
	if err != nil {
		// 不区分「不存在」与「不属于你」，避免探测他人任务是否存在。
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return nil, false
	}
	return t, true
}

func (s *Server) getTorrent(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	t, ok := s.ownTask(c)
	if !ok {
		return
	}
	ih := t.InfoHash
	base := gin.H{
		"info_hash": t.InfoHash, "name": t.Name, "status": t.Status,
		"total": t.Total, "completed": t.Completed, "created_at": t.CreatedAt,
	}
	if p := s.progress(ih); p != nil {
		base["name"] = p.Name
		base["status"] = p.Status
		base["total"] = p.Total
		base["completed"] = p.Completed
		base["peers"] = p.Peers
		base["speed"] = p.Speed
	}
	_ = cl
	c.JSON(http.StatusOK, base)
}

type patchReq struct {
	Action string `json:"action"` // pause / resume
}

func (s *Server) patchTorrent(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	t, ok := s.ownTask(c)
	if !ok {
		return
	}
	ih := t.InfoHash

	var req patchReq
	_ = c.ShouldBindJSON(&req)

	switch req.Action {
	case "pause":
		if s.eng != nil {
			if err := s.eng.Pause(ih); err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
				return
			}
		}
		s.store.SetStatus(cl.UserID, ih, "paused")
	case "resume":
		if s.eng != nil {
			if err := s.eng.Resume(ih); err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
				return
			}
		}
		s.store.SetStatus(cl.UserID, ih, "active")
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action 需为 pause 或 resume"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"info_hash": ih, "action": req.Action})
}

func (s *Server) deleteTorrent(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	t, ok := s.ownTask(c)
	if !ok {
		return
	}
	ih := t.InfoHash
	s.removeTask(ih)
	if err := s.store.DeleteTask(cl.UserID, ih); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	s.store.Audit(cl.Username, "delete_task", ih, "")
	c.JSON(http.StatusOK, gin.H{"deleted": true, "info_hash": ih})
}

// downloadFile 取回已完成任务的文件：单文件直接流式返回，多文件打包 zip。
func (s *Server) downloadFile(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	t, ok := s.ownTask(c)
	if !ok {
		return
	}
	ih := t.InfoHash
	_ = cl

	p := s.progress(ih)
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "下载引擎中不存在该任务"})
		return
	}
	if p.Status != "complete" {
		c.JSON(http.StatusConflict, gin.H{"error": "任务尚未完成（当前状态：" + p.Status + "）"})
		return
	}
	name, entries, err := s.eng.FileEntries(ih)
	if err != nil || len(entries) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到已完成文件"})
		return
	}
	if name == "" {
		name = ih
	}

	// 单文件：直接返回原文件
	if len(entries) == 1 {
		c.FileAttachment(entries[0].Path, filepath.Base(entries[0].Path))
		return
	}

	// 多文件：流式打包 zip（不落盘）
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name)+".zip")
	zw := zip.NewWriter(c.Writer)
	for _, en := range entries {
		w, err := zw.Create(en.Name)
		if err != nil {
			continue
		}
		f, err := os.Open(en.Path)
		if err != nil {
			continue
		}
		io.Copy(w, f)
		f.Close()
	}
	zw.Close()
}

// ============ 管理员：黑名单 / 审计 / 扫描 ============

func (s *Server) addBlockedHash(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	var req struct {
		InfoHash string `json:"info_hash"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.InfoHash = strings.ToLower(strings.TrimSpace(req.InfoHash))
	if len(req.InfoHash) != 40 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "info_hash 需为 40 位十六进制"})
		return
	}
	if err := s.store.AddBlockedHash(req.InfoHash, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败"})
		return
	}
	s.store.Audit(cl.Username, "block_add_hash", req.InfoHash, req.Reason)
	c.JSON(http.StatusCreated, gin.H{"blocked": req.InfoHash})
}

func (s *Server) listBlockedHashes(c *gin.Context) {
	list, err := s.store.ListBlockedHashes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hashes": list})
}

func (s *Server) removeBlockedHash(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	ih := strings.ToLower(c.Param("infoHash"))
	if err := s.store.RemoveBlockedHash(ih); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	s.store.Audit(cl.Username, "block_remove_hash", ih, "")
	c.JSON(http.StatusOK, gin.H{"removed": ih})
}

func (s *Server) addBlockedKeyword(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	var req struct {
		Keyword string `json:"keyword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	if req.Keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "关键词不能为空"})
		return
	}
	if err := s.store.AddBlockedKeyword(req.Keyword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败"})
		return
	}
	s.store.Audit(cl.Username, "block_add_keyword", req.Keyword, "")
	c.JSON(http.StatusCreated, gin.H{"blocked": req.Keyword})
}

func (s *Server) listBlockedKeywords(c *gin.Context) {
	list, err := s.store.ListBlockedKeywords()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"keywords": list})
}

func (s *Server) removeBlockedKeyword(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	kw := c.Query("keyword")
	if kw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 keyword 参数"})
		return
	}
	if err := s.store.RemoveBlockedKeyword(kw); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	s.store.Audit(cl.Username, "block_remove_keyword", kw, "")
	c.JSON(http.StatusOK, gin.H{"removed": kw})
}

func (s *Server) listAudit(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	entries, err := s.store.ListAudit(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit": entries})
}

// enforceBlacklist 扫描所有任务，移除命中哈希/关键词黑名单的任务（管理员手动触发）。
//
// 这是全站清理动作，允许跨用户删除——但必须留在管理员组里，
// 且每次删除都写审计，否则就是一个「管理员版越权删任务」。
func (s *Server) enforceBlacklist(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	tasks, err := s.store.ListAllTasks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "扫描失败"})
		return
	}
	var removed []string
	for _, t := range tasks {
		bh, _ := s.store.IsHashBlocked(t.InfoHash)
		_, kh, _ := s.store.MatchBlockedKeyword(t.Name + " " + t.Magnet)
		if bh || kh {
			s.removeTask(t.InfoHash)
			s.store.DeleteTaskByHash(t.InfoHash)
			removed = append(removed, t.InfoHash)
		}
	}
	s.store.Audit(cl.Username, "scan_remove", "", strings.Join(removed, ","))
	c.JSON(http.StatusOK, gin.H{"removed": removed})
}

// metrics 输出 Prometheus 文本格式指标（仅管理员，见路由注册处）。
func (s *Server) metrics(c *gin.Context) {
	tasks, _ := s.store.ListAllTasks()
	var active, complete int
	for _, t := range tasks {
		switch t.Status {
		case "active", "resolving", "paused":
			active++
		case "complete":
			complete++
		}
	}
	users, _ := s.store.UserCount()
	c.Header("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(c.Writer, "# HELP horizon_active_tasks 活跃下载任务数\n# TYPE horizon_active_tasks gauge\nhorizon_active_tasks %d\n", active)
	fmt.Fprintf(c.Writer, "# HELP horizon_complete_tasks 已完成任务数\n# TYPE horizon_complete_tasks gauge\nhorizon_complete_tasks %d\n", complete)
	fmt.Fprintf(c.Writer, "# HELP horizon_total_tasks 任务总数\n# TYPE horizon_total_tasks gauge\nhorizon_total_tasks %d\n", len(tasks))
	fmt.Fprintf(c.Writer, "# HELP horizon_users 用户总数\n# TYPE horizon_users gauge\nhorizon_users %d\n", users)
}
