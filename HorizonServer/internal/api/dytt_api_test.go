package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"horizon/internal/dytt"
	"horizon/internal/torsearch"
)

// dyttStubClient 返回内容站的离线样本，避免单元测试依赖外网（外网测试放在
// dytt 包外的 smokescript 里单独跑）。
func dyttStubClient(t *testing.T) *dytt.Client {
	t.Helper()
	detail, err := os.ReadFile(filepath.Join("..", "dytt", "testdata", "detail_page.html"))
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}
	list, err := os.ReadFile(filepath.Join("..", "dytt", "testdata", "list_page.html"))
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}
	transport := stubTransport{
		detail: string(detail),
		list:   string(list),
	}
	return dytt.New(dytt.Config{
		BaseURL:     "https://dytt.org.cn",
		MinInterval: 1,
		Retries:     1,
		HTTPClient:  &http.Client{Transport: transport},
	})
}

type stubTransport struct {
	detail string
	list   string
}

func (s stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := s.list
	if strings.Contains(r.URL.Path, "/p/") {
		body = s.detail
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

// newRouterWithDytt 与 newTestServer 相同，但注入离线元数据服务。
func newRouterWithDytt(t *testing.T) *gin.Engine {
	t.Helper()
	st, am := newTestServerParts(t)
	svc := dytt.NewService(dyttStubClient(t), 0)
	return NewRouter(st, nil, am, WithDyttService(svc))
}

// ============ 用例 ============

// 未启用元数据抓取时，/api/v1/dytt/* 必须稳定返回 503，而不是 panic 或 404。
// 这条覆盖“部署时关掉外呼”的场景，也保证路由始终注册着。
func TestDyttRoutesDisabledReturns503(t *testing.T) {
	r := newTestServer(t)
	for _, path := range []string{
		"/api/v1/dytt/list",
		"/api/v1/dytt/latest",
		"/api/v1/dytt/search?q=test",
		"/api/v1/dytt/detail?id=1",
		"/api/v1/dytt/cover?url=https://img.picbf.com/a.jpg",
	} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d, want 503 (body=%s)", path, w.Code, w.Body.String())
		}
	}
}

// 参数校验：非法参数应在接口层被拦下，不透传给上游。
func TestDyttParamValidation(t *testing.T) {
	r := newRouterWithDytt(t)
	for _, path := range []string{
		"/api/v1/dytt/search",
		"/api/v1/dytt/search?q=%20%20",
		"/api/v1/dytt/detail",
		"/api/v1/dytt/list?page=0",
		"/api/v1/dytt/list?page=abc",
		"/api/v1/dytt/cover",
		"/api/v1/dytt/cover?url=ftp://x/a.jpg",
	} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400 (body=%s)", path, w.Code, w.Body.String())
		}
	}
}

// 封面代理必须拦住非白名单域名，避免被当成 SSRF 跳板。
func TestDyttCoverRejectsForeignHost(t *testing.T) {
	r := newRouterWithDytt(t)
	for _, path := range []string{
		"/api/v1/dytt/cover?url=http://169.254.169.254/latest/meta-data/",
		"/api/v1/dytt/cover?url=file:///c:/windows/win.ini",
		"/api/v1/dytt/cover?url=https://127.0.0.1:8080/x.jpg",
	} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400 (body=%s)", path, w.Code, w.Body.String())
		}
	}
}

// 详情接口返回客户端海报墙所需的字段：封面、名称、简介、磁力检索关键词。
func TestDyttDetailShape(t *testing.T) {
	r := newRouterWithDytt(t)
	req := httptest.NewRequest("GET", "/api/v1/dytt/detail?id=266003", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, key := range []string{"\"poster\"", "\"title\"", "\"summary\"", "\"magnet_queries\"", "\"search_keyword\""} {
		if !strings.Contains(body, key) {
			t.Errorf("详情响应缺少字段 %s", key)
		}
	}
	if !strings.Contains(body, "云雀叫天录") {
		t.Errorf("详情响应缺少片名: %s", body[:min(200, len(body))])
	}
}

// 搜索接口能返回卡片列表，且每张卡片都有可点开的详情链接。
func TestDyttSearchShape(t *testing.T) {
	r := newRouterWithDytt(t)
	req := httptest.NewRequest("GET", "/api/v1/dytt/search?q=%E7%81%AB%E7%A7%8D", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "\"items\"") || !strings.Contains(body, "\"poster\"") {
		t.Errorf("搜索结果结构异常: %s", body[:min(200, len(body))])
	}
}

// ============ 中文检索相关（本轮修复） ============

// newRouterWithDyttAndSearch 注入离线元数据服务 + 可控的种子检索器。
// tpbHandler 会收到所有 apibay 请求；transHandler 收到翻译请求（nil 表示翻译直接失败）。
func newRouterWithDyttAndSearch(t *testing.T, tpbHandler, transHandler http.HandlerFunc) *gin.Engine {
	t.Helper()
	st, am := newTestServerParts(t)
	svc := dytt.NewService(dyttStubClient(t), 0)

	var tpbSrv, transSrv *httptest.Server
	if tpbHandler != nil {
		tpbSrv = httptest.NewServer(tpbHandler)
	}
	if transHandler != nil {
		transSrv = httptest.NewServer(transHandler)
	}
	t.Cleanup(func() {
		if tpbSrv != nil {
			tpbSrv.Close()
		}
		if transSrv != nil {
			transSrv.Close()
		}
	})

	// DisableCSV：接口层用例只关心「中文翻译 + 过滤」这条链路，
	// 不该真的去打 torrents-csv.com（否则结果条数随外网变化，用例会飘）。
	cfg := torsearch.Config{HTTPTimeout: 5 * time.Second, DisableCSV: true}
	if tpbSrv != nil {
		cfg.TPBBase = tpbSrv.URL
	}
	if transSrv != nil {
		cfg.TransmartURL = transSrv.URL
	}
	return NewRouter(st, nil, am,
		WithDyttService(svc),
		WithTorrentSearcher(torsearch.New(cfg)),
	)
}

// 中文关键词必须翻译成英文后再检索，并过滤掉无关结果。
// 这是本次修复的核心问题：直接用中文查 apibay 会返回满屏无关热门种子。
func TestSearchTranslatesChineseAndFilters(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "Wandering") {
			writeJSONBody(w, []map[string]string{
				{"name": "The.Wandering.Earth.2019.1080p", "info_hash": "aa11", "seeders": "46", "size": "100"},
				{"name": "Spider-Man.Brand.New.Day.2026.1080p", "info_hash": "cc33", "seeders": "6351", "size": "200"},
			})
			return
		}
		// 中文原词检索：模拟 apibay 倒回无关热门种子
		writeJSONBody(w, []map[string]string{
			{"name": "Spider-Man.Brand.New.Day.2026.1080p", "info_hash": "cc33", "seeders": "6351", "size": "200"},
		})
	}
	trans := func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(w, map[string]interface{}{
			"header":           map[string]string{"ret_code": "succ"},
			"auto_translation": []string{"The Wandering Earth (电影名)"},
		})
	}
	r := newRouterWithDyttAndSearch(t, tpb, trans)

	req := httptest.NewRequest("GET", "/api/v1/search?q="+url.QueryEscape("流浪地球")+"&cat=207", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		SearchTerm   string `json:"search_term"`
		Translated   string `json:"translated"`
		TranslatedOK bool   `json:"translated_ok"`
		FilteredOut  int    `json:"filtered_out"`
		Count        int    `json:"count"`
		Results      []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !resp.TranslatedOK || resp.Translated != "The Wandering Earth" {
		t.Errorf("应翻译为 The Wandering Earth，实际 %q (ok=%v)", resp.Translated, resp.TranslatedOK)
	}
	if resp.SearchTerm != "The Wandering Earth" {
		t.Errorf("search_term 应为翻译结果，实际 %q", resp.SearchTerm)
	}
	if resp.Count != 1 || resp.FilteredOut != 1 {
		t.Fatalf("应保留 1 条、过滤 1 条，实际 count=%d filtered=%d", resp.Count, resp.FilteredOut)
	}
	for _, it := range resp.Results {
		if strings.Contains(it.Name, "Spider-Man") {
			t.Error("无关结果必须被过滤，否则用户会把《蜘蛛侠》误当成目标影片")
		}
	}
}

// 详情接口应给出可用于英文索引站点检索的英文片名与检索词。
func TestDyttDetailReturnsEnglishTitle(t *testing.T) {
	trans := func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(w, map[string]interface{}{
			"header":           map[string]string{"ret_code": "succ"},
			"auto_translation": []string{"Skylark Calls the Sky (剧名)"},
		})
	}
	r := newRouterWithDyttAndSearch(t, func(w http.ResponseWriter, req *http.Request) {
		writeJSONBody(w, []map[string]string{})
	}, trans)

	req := httptest.NewRequest("GET", "/api/v1/dytt/detail?id=266003", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	en, _ := resp["english_title"].(string)
	if strings.TrimSpace(en) == "" {
		t.Fatalf("详情响应缺少 english_title: %s", w.Body.String()[:min(220, len(w.Body.String()))])
	}
	if strings.ContainsAny(en, "（）()") {
		t.Errorf("english_title 应已清洗掉括号注释，实际 %q", en)
	}
	// search_keyword 必须是纯片名（英文优先），不能是带「2026 1080p 磁力」后缀的长串：
	// 那种长串会被机器翻译逐字转换而失真，实测搜不到任何资源。
	kw, _ := resp["search_keyword"].(string)
	if strings.Contains(kw, "磁力") || strings.Contains(kw, "1080p") {
		t.Errorf("search_keyword 不该带后缀模板，实际 %q", kw)
	}
	if kw != en {
		t.Errorf("有英文片名时 search_keyword 应等于它，实际 %q vs %q", kw, en)
	}
}

// 翻译不可用时，详情接口必须仍然 200，并把 search_keyword 退化为中文片名。
func TestDyttDetailFallsBackWhenTranslateFails(t *testing.T) {
	r := newRouterWithDyttAndSearch(t, func(w http.ResponseWriter, req *http.Request) {
		writeJSONBody(w, []map[string]string{})
	}, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	req := httptest.NewRequest("GET", "/api/v1/dytt/detail?id=266003", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("翻译失败不该影响详情接口，got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if kw, _ := resp["search_keyword"].(string); strings.TrimSpace(kw) == "" {
		t.Error("翻译失败时应退化为中文片名，而不是空检索词")
	}
}

// 种子搜索接口缺少关键词时必须 400。
func TestSearchRequiresQuery(t *testing.T) {
	r := newRouterWithDyttAndSearch(t, func(w http.ResponseWriter, req *http.Request) {
		writeJSONBody(w, []map[string]string{})
	}, nil)
	req := httptest.NewRequest("GET", "/api/v1/search", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("search 无关键词 = %d, want 400", w.Code)
	}
}

// writeJSONBody 写一个 JSON 响应体。
func writeJSONBody(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
