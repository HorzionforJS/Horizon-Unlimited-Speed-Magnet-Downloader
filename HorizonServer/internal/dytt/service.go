package dytt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 分类别名 -> dytt.org.cn 栏目 ID
var categoryAliases = map[string]string{
	"movie": "3", "movies": "3", "电影": "3",
	"tv": "2", "series": "2", "剧集": "2", "影视剧": "2",
	"short": "21", "shortplay": "21", "短剧": "21",
}

// NormalizeCategory 把别名（movie/电影/3）统一成栏目 ID。
func NormalizeCategory(cat string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(cat))
	if key == "" {
		return "3", nil
	}
	if id, ok := categoryAliases[key]; ok {
		return id, nil
	}
	if _, err := strconv.Atoi(key); err == nil {
		return key, nil
	}
	return "", fmt.Errorf("未知分类 %q（可用 movie/tv/short 或 3/2/21）", cat)
}

// breakerCooldown 是熔断后多久允许再试一次源站。
//
// 取值考虑了两侧的成本：太短（比如 10 秒）会让每个客户端请求都去撞一次
// 已经挂掉的源站，每次都白等一个 CallTimeout；太长（比如 10 分钟）则源站
// 恢复后要过很久才有人发现。1 分钟是「最多让用户多等一分钟」与
// 「恢复后一分钟内自动接上」的折中。
const breakerCooldown = time.Minute

// Service 组合抓取客户端与缓存。
type Service struct {
	client   *Client
	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry

	// 以下三个字段实现「源站故障期间不反复打上游」的熔断。
	// 它们只在 mu 保护下读写（见 snapshotLocked）。
	breakerUntil time.Time // 在此时间之前直接跳过上游
	breakerErr   string    // 触发熔断的原因，用于提示用户
}

type cacheEntry struct {
	body string
	at   time.Time
}

// NewService 创建服务；cacheTTL<=0 时禁用缓存。
func NewService(client *Client, cacheTTL time.Duration) *Service {
	if client == nil {
		client = New(Config{})
	}
	return &Service{client: client, cacheTTL: cacheTTL, cache: map[string]cacheEntry{}}
}

// snapshotLocked 返回当前缓存与熔断状态（调用前需持有 s.mu）。
func (s *Service) snapshotLocked() (stale map[string]cacheEntry, until time.Time, reason string) {
	stale = make(map[string]cacheEntry, len(s.cache))
	for k, v := range s.cache {
		stale[k] = v
	}
	return stale, s.breakerUntil, s.breakerErr
}

// tripBreakerLocked 记录一次源站故障（调用前需持有 s.mu）。
//
// 只在「源站级」故障时触发：超时、连接失败、5xx、网关错误。
// 限流（HTTP 400/429）不熔断——源站活着，只是嫌我们快，重试+降频才对。
func (s *Service) tripBreakerLocked(err error) {
	var rate ErrRateLimited
	if err == nil || errors.As(err, &rate) {
		return
	}
	var notFound ErrNotFound
	if errors.As(err, &notFound) {
		return
	}
	s.breakerUntil = time.Now().Add(breakerCooldown)
	s.breakerErr = err.Error()
}

// clearBreakerLocked 源站恢复后解除熔断（调用前需持有 s.mu）。
func (s *Service) clearBreakerLocked() {
	s.breakerUntil = time.Time{}
	s.breakerErr = ""
}

// BaseURL 返回内容站地址。
func (s *Service) BaseURL() string { return s.client.BaseURL() }

// ListPage 获取分类片单的指定页（含封面与名称）。
func (s *Service) ListPage(ctx context.Context, category string, page int) (Page, error) {
	catID, err := NormalizeCategory(category)
	if err != nil {
		return Page{}, err
	}
	if page < 1 {
		page = 1
	}
	path := "/t/" + catID + "/"
	if page > 1 {
		path = fmt.Sprintf("/t/%s-%d/", catID, page)
	}
	fo, err := s.fetch(ctx, s.client.BaseURL()+path, path)
	if err != nil {
		return Page{}, err
	}
	items, pi, err := s.client.parsePage(fo.body)
	if err != nil {
		return Page{}, err
	}
	cur := pi.Page
	if cur <= 0 {
		cur = page
	}
	out := Page{
		Category:   catID,
		Page:       cur,
		TotalPages: pi.TotalPages,
		HasNext:    pi.HasNext,
		Count:      len(items),
		Items:      items,
		FromCache:  fo.fromCache,
		Stale:      fo.stale,
	}
	if pi.HasNext {
		out.NextPage = cur + 1
	}
	return out, nil
}

// Search 按关键词搜索片名。
func (s *Service) Search(ctx context.Context, keyword string) (Page, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return Page{}, errors.New("搜索关键词不能为空")
	}
	path := "/u--/?wd=" + url.QueryEscape(keyword)
	fo, err := s.fetch(ctx, s.client.BaseURL()+path, "search:"+keyword)
	if err != nil {
		return Page{}, err
	}
	items, _, err := s.client.parsePage(fo.body)
	if err != nil {
		return Page{}, err
	}
	return Page{Category: "search", Page: 1, Count: len(items), Items: items,
		FromCache: fo.fromCache, Stale: fo.stale}, nil
}

// Detail 获取详情：封面、名称、简介、演员、线路与磁力检索关键词。
func (s *Service) Detail(ctx context.Context, target string) (MovieDetail, error) {
	detailURL, ok := s.client.resolveDetailURL(target)
	if !ok {
		return MovieDetail{}, fmt.Errorf("无法识别的目标 %q（可用 ID、/p/ID/ 或详情页 URL）", target)
	}
	fo, err := s.fetch(ctx, detailURL, detailURL)
	if err != nil {
		return MovieDetail{}, err
	}
	d := s.client.parseDetail(fo.body, detailURL)
	if strings.TrimSpace(d.Title) == "" {
		return MovieDetail{}, errors.New("详情页解析失败：页面结构可能已变更")
	}
	if d.Episodes == nil {
		d.Episodes = []Episode{}
	}
	if d.Related == nil {
		d.Related = []MovieSummary{}
	}
	return d, nil
}

// Latest 取首页“每日更新”的最新片单（附带缓存/过期状态）。
//
// 之所以单独返回状态而不是只给切片：客户端要能区分「这是刚抓的」和
// 「这是源站挂了、拿旧快照顶上的」，否则用户会把三天前的片单当今天更新。
func (s *Service) Latest(ctx context.Context, limit int) ([]MovieSummary, FetchState, error) {
	fo, err := s.fetch(ctx, s.client.BaseURL()+"/", "home")
	if err != nil {
		return nil, FetchState{}, err
	}
	items, _, err := s.client.parsePage(fo.body)
	if err != nil {
		return nil, FetchState{}, err
	}
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	return items[:limit], FetchState{FromCache: fo.fromCache, Stale: fo.stale}, nil
}

// Fetch 结果里的三个状态位通过 fetchOut 回传：
// fromCache（命中新鲜缓存）/ stale（回退到过期缓存）/ 正常回源。
type fetchOut struct {
	body      string
	fromCache bool
	stale     bool
}

// fetch 拉取页面：命中新鲜缓存直接返回；源站故障时回退到过期缓存。
//
// 三层保护，按顺序判断：
//  1. 新鲜缓存（TTL 内）——最快，不碰网络。
//  2. 熔断器——源站刚挂过，直接放弃，不让每个请求都白等一个 CallTimeout。
//  3. 回退过期缓存——源站故障时给上次成功抓到的快照，胜过给用户一个错误弹窗。
//
// 熔断不依赖 cacheTTL：就算运维把缓存关了，也不该让每个请求去撞一个已知挂掉的源站。
func (s *Service) fetch(ctx context.Context, rawURL, key string) (fetchOut, error) {
	var staleBody string

	if s.cacheTTL > 0 {
		s.mu.Lock()
		if e, ok := s.cache[key]; ok {
			if time.Since(e.at) < s.cacheTTL {
				s.mu.Unlock()
				return fetchOut{body: e.body, fromCache: true}, nil
			}
			// 过期了也先留着：源站一挂它就是唯一能用的数据。
			staleBody = e.body
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	until, reason := s.breakerUntil, s.breakerErr
	s.mu.Unlock()
	if time.Now().Before(until) {
		// 熔断期内不再打上游。有旧数据就给旧数据，比让用户等一个必然的超时好。
		if staleBody != "" {
			return fetchOut{body: staleBody, stale: true}, nil
		}
		// 必须是 ErrUpstreamDown：接口层据此返回 503 + upstream 标记，
		// 用普通 error 会被归到「未知故障」那一档，前端就只能拿到一句笼统的「抓取失败」。
		return fetchOut{}, ErrUpstreamDown{
			URL: s.client.BaseURL(),
			Err: fmt.Errorf("内容源仍在熔断冷却中（上次失败：%s），预计 %s 后自动重试",
				reason, time.Until(until).Round(time.Second)),
		}
	}

	body, err := s.client.get(ctx, rawURL)
	if err != nil {
		s.mu.Lock()
		s.tripBreakerLocked(err)
		s.mu.Unlock()
		if staleBody != "" {
			return fetchOut{body: staleBody, stale: true}, nil
		}
		return fetchOut{}, err
	}

	s.mu.Lock()
	s.clearBreakerLocked()
	// 缓存条目做简单上限控制，避免长期运行内存增长
	if s.cacheTTL > 0 {
		if len(s.cache) > 200 {
			cutoff := time.Now().Add(-2 * s.cacheTTL)
			for k, v := range s.cache {
				if v.at.Before(cutoff) {
					delete(s.cache, k)
				}
			}
		}
		s.cache[key] = cacheEntry{body: body, at: time.Now()}
	}
	s.mu.Unlock()

	return fetchOut{body: body}, nil
}
