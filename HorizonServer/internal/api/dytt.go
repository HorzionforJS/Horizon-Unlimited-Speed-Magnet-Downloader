package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"horizon/internal/dytt"
	"horizon/internal/torsearch"
)

// ============ 电影天堂元数据：封面 / 名称 / 简介 ============
//
// 说明：home.dytiantang.com.cn 只是一个用全屏 iframe 包住 dytt.org.cn 的外壳，
// 首页卡片是写死的占位数据。真实内容源是 dytt.org.cn，服务端已直接对接内容源。
//
// 磁力链接说明：源站只提供在线播放，不含 magnet 链接，因此这里返回
// magnet_queries（精确检索关键词）而不是伪造的磁力链接。

// dyttList 分类片单（公开接口，无需登录）。
// GET /api/v1/dytt/list?category=movie&page=1
func (s *Server) dyttList(c *gin.Context) {
	if s.dytt == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "元数据抓取未启用"})
		return
	}
	page := 1
	if v := strings.TrimSpace(c.Query("page")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page 需为正整数"})
			return
		}
		page = n
	}
	result, err := s.dytt.ListPage(c.Request.Context(), c.DefaultQuery("category", "movie"), page)
	if err != nil {
		writeDyttError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// dyttLatest 首页“每日更新”片单（公开接口）。
// GET /api/v1/dytt/latest?limit=20
func (s *Server) dyttLatest(c *gin.Context) {
	if s.dytt == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "元数据抓取未启用"})
		return
	}
	limit := 20
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	items, state, err := s.dytt.Latest(c.Request.Context(), limit)
	if err != nil {
		writeDyttError(c, err)
		return
	}
	resp := gin.H{"count": len(items), "items": items, "from_cache": state.FromCache}
	// 只在这两种情况下带提示：源站挂了用旧数据顶着。
	// 客户端据此把提示显示出来，用户不会误以为「今天没有更新」。
	if state.Stale {
		resp["stale"] = true
		resp["hint"] = "内容源暂时不可用，当前显示的是上次成功抓取的片单，可能不是最新。"
		_ = c.Error(errUpstreamStale)
	} else if state.FromCache {
		resp["from_cache"] = true
	}
	c.JSON(http.StatusOK, resp)
}

// dyttSearch 按片名搜索（公开接口）。
// GET /api/v1/dytt/search?q=火种
func (s *Server) dyttSearch(c *gin.Context) {
	if s.dytt == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "元数据抓取未启用"})
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少搜索关键词 q"})
		return
	}
	result, err := s.dytt.Search(c.Request.Context(), q)
	if err != nil {
		writeDyttError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// dyttDetail 影片详情：封面、名称、简介、演员、线路、磁力检索关键词。
// GET /api/v1/dytt/detail?id=266003[&enhance=1]
//
// enhance=1 且服务端配置了 TMDb API Key 时，额外补充高分简介/大图封面/评分。
func (s *Server) dyttDetail(c *gin.Context) {
	if s.dytt == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "元数据抓取未启用"})
		return
	}
	target := firstNonEmptyQuery(c, "id", "url", "target")
	if target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要提供 id 或 url 参数"})
		return
	}
	detail, err := s.dytt.Detail(c.Request.Context(), target)
	if err != nil {
		writeDyttError(c, err)
		return
	}

	resp := gin.H{
		"id": detail.ID, "title": detail.Title, "url": detail.URL,
		"poster": detail.Poster, "summary": detail.Summary, "score": detail.Score,
		"year": detail.Year, "category": detail.Category, "actors": detail.Actors,
		"genres": detail.Genres, "region": detail.Region, "language": detail.Language,
		"first_aired": detail.FirstAired, "updated_at": detail.UpdatedAt,
		"episodes": detail.Episodes, "episode_count": detail.EpisodeNum,
		"play_sources": detail.PlaySources, "related": detail.Related,
		"magnet_queries": detail.MagnetQueries,
		// 直接粘进下载框即可的搜索关键词（首个变体），方便客户端一键使用
		"search_keyword": firstMagnetQuery(detail.MagnetQueries, detail.Title),
	}

	if wantsEnhance(c) && s.tmdb != nil && s.tmdb.Enabled() {
		if m, ok := s.enhance(c, detail); ok {
			resp["tmdb"] = m
			// TMDb 的原始片名是最可靠的英文检索词（人工维护，优于机器翻译）。
			if v, ok := m["original_title"].(string); ok && strings.TrimSpace(v) != "" {
				resp["english_title"] = strings.TrimSpace(v)
			} else if v, ok := m["title"].(string); ok && strings.TrimSpace(v) != "" {
				resp["english_title"] = strings.TrimSpace(v)
			}
		}
	}
	// 没有 TMDb 时用翻译服务兜底给英文片名：英文索引站点（The Pirate Bay 等）
	// 几乎只收英文片名，中文片名直接检索会返回满屏无关结果。
	if _, ok := resp["english_title"]; !ok {
		if en := s.translateTitle(c.Request.Context(), detail.Title); en != "" {
			resp["english_title"] = en
		}
	}
	// 客户端「搜索磁力」应使用纯片名（英文优先）：带「2026 1080p 磁力」后缀的长串
	// 会被机器翻译逐字转换而失真，实测带后缀的关键词搜不到任何资源。
	if en, ok := resp["english_title"].(string); ok && en != "" {
		resp["search_keyword"] = en
	} else {
		resp["search_keyword"] = strings.TrimSpace(detail.Title)
	}
	c.JSON(http.StatusOK, resp)
}

// translateTitle 把中文片名翻成英文；未启用翻译或失败时返回空串（不影响主流程）。
func (s *Server) translateTitle(ctx context.Context, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	searcher := s.torsearch
	if searcher == nil {
		searcher = torsearch.Default()
	}
	// 英文片名无需翻译；只处理含中日韩文字的情况，避免多余外呼。
	if !torsearch.HasCJK(title) {
		return title
	}
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	en, err := searcher.Translate(tctx, title)
	if err != nil {
		return ""
	}
	return torsearch.CleanTranslated(en)
}

// dyttCover 封面图代理（公开接口）。
// GET /api/v1/dytt/cover?url=<封面直链>
//
// 图床对 Referer 有校验，浏览器/客户端直接引用常出现裂图；走后端代理可稳定显示。
// 只放行已知图床域名，避免被当作 SSRF 跳板。
func (s *Server) dyttCover(c *gin.Context) {
	if s.dytt == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "元数据抓取未启用"})
		return
	}
	raw := strings.TrimSpace(c.Query("url"))
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 url 参数"})
		return
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 http/https 图片地址"})
		return
	}
	result, err := s.dytt.FetchImage(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, dytt.ErrImageHostNotAllowed) || errors.Is(err, dytt.ErrInvalidImageURL) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, result.ContentType, result.Body)
}

// enhance 用 TMDb 补全简介/封面/评分；失败时静默降级，不影响主流程。
// 若 TMDb 没有中文简介（常见于冷门片），再用英文结果补一次。
func (s *Server) enhance(c *gin.Context, d dytt.MovieDetail) (gin.H, bool) {
	ctx := c.Request.Context()
	match, ok, err := s.tmdb.Match(ctx, d.Title, d.Year, "", "")
	if err != nil || !ok || match == nil {
		return nil, false
	}
	if strings.TrimSpace(match.Overview) == "" {
		if enClient := s.tmdbFallback; enClient != nil && enClient.Enabled() {
			if alt, ok2, err2 := enClient.Match(ctx, d.Title, d.Year, match.IMDbID, match.MediaType); err2 == nil && ok2 && alt != nil {
				if strings.TrimSpace(alt.Overview) != "" {
					match.Overview = alt.Overview
				}
				if match.PosterURL == "" {
					match.PosterURL = alt.PosterURL
				}
			}
		}
	}
	return gin.H{
		"tmdb_id": match.TMDbID, "imdb_id": match.IMDbID, "media_type": match.MediaType,
		"title": match.Title, "original_title": match.OriginalTitle, "year": match.Year,
		"overview": match.Overview, "poster_url": match.PosterURL, "backdrop_url": match.BackdropURL,
		"rating": match.Rating, "vote_count": match.VoteCount, "genres": match.Genres,
		"runtime_minutes": match.RuntimeMin, "release_date": match.ReleaseDate,
		"homepage": match.Homepage, "tmdb_url": match.TMDbURL,
	}, true
}

// ============ 辅助 ============

func wantsEnhance(c *gin.Context) bool {
	switch strings.ToLower(strings.TrimSpace(c.DefaultQuery("enhance", "1"))) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

func firstNonEmptyQuery(c *gin.Context, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(c.Query(k)); v != "" {
			return v
		}
	}
	return ""
}

func firstMagnetQuery(queries []string, title string) string {
	if len(queries) > 0 {
		return queries[0]
	}
	return strings.TrimSpace(title)
}

// writeDyttError 把抓取层错误映射为合适的 HTTP 状态码。
// errUpstreamStale 只用于让日志里能看出「这条 200 是旧数据顶的」，
// 响应体本身仍然是 200 + stale:true（旧数据是可用数据，不该当错误返回）。
var errUpstreamStale = errors.New("内容源不可用，已回退到过期缓存")

// writeDyttError 把抓取错误映射成客户端能看懂的状态码与文案。
//
// 关键是把「源站连不上」和「我们自己的问题」分开：前者是外部故障，
// 用户等一会儿就好；返回 502 + 一句模糊的「抓取失败」会让人以为服务坏了。
func writeDyttError(c *gin.Context, err error) {
	var rate dytt.ErrRateLimited
	var notFound dytt.ErrNotFound
	var httpErr dytt.HTTPError
	var down dytt.ErrUpstreamDown

	switch {
	case errors.As(err, &rate):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "retryable": true})
	case errors.As(err, &down):
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "内容源（电影天堂）暂时不可用，请稍后重试。这不影响磁力搜索与下载。",
			"upstream":  true,
			"retryable": true,
			"detail":    err.Error(),
		})
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &httpErr):
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "upstream": true, "retryable": true})
	case strings.Contains(err.Error(), "无法识别"), strings.Contains(err.Error(), "未知分类"), strings.Contains(err.Error(), "不能为空"):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"error": "抓取失败: " + err.Error(), "retryable": true})
	}
}
