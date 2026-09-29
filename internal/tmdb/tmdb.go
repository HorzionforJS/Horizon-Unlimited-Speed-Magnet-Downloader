// Package tmdb 对接 The Movie Database，用于把电影天堂的片名/年份补全为
// 结构化元数据（简介、封面大图、评分、类型、IMDb 链接）。
//
// 完全可选：未配置 api_key 时 Enabled() 返回 false，调用方应跳过增强，
// 服务端仍然返回电影天堂自身的元数据。
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL  = "https://api.themoviedb.org/3"
	defaultLanguage = "zh-CN"
	defaultTimeout  = 12 * time.Second
	maxBodyBytes    = 4 << 20
	// ImageBaseURL 是 TMDb 图片 CDN 前缀，拼接 size + poster_path 使用。
	ImageBaseURL = "https://image.tmdb.org/t/p"
)

// Match 是一条归一化后的影视元数据。
type Match struct {
	TMDbID        int64    `json:"tmdb_id"`
	IMDbID        string   `json:"imdb_id"`
	MediaType     string   `json:"media_type"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	Year          int      `json:"year"`
	Overview      string   `json:"overview"`
	PosterURL     string   `json:"poster_url"`
	BackdropURL   string   `json:"backdrop_url"`
	Rating        float64  `json:"rating"`
	VoteCount     int      `json:"vote_count"`
	Genres        []string `json:"genres"`
	RuntimeMin    int      `json:"runtime_minutes"`
	ReleaseDate   string   `json:"release_date"`
	Status        string   `json:"status"`
	Homepage      string   `json:"homepage"`
	TMDbURL       string   `json:"tmdb_url"`
}

// Config 控制 TMDb 客户端。
type Config struct {
	APIKey     string
	BaseURL    string
	Language   string
	HTTPClient *http.Client
}

func (c Config) withDefaults() Config {
	out := c
	out.APIKey = strings.TrimSpace(out.APIKey)
	if out.BaseURL == "" {
		out.BaseURL = defaultBaseURL
	}
	out.BaseURL = strings.TrimRight(out.BaseURL, "/")
	if out.Language == "" {
		out.Language = defaultLanguage
	}
	if out.HTTPClient == nil {
		out.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	return out
}

// Client 是 TMDb 客户端。
type Client struct{ cfg Config }

// New 构造客户端。
func New(cfg Config) *Client { return &Client{cfg: cfg.withDefaults()} }

// Enabled 表示是否配置了 API Key。
func (c *Client) Enabled() bool { return c != nil && c.cfg.APIKey != "" }

// ============ 上游响应结构 ============

type apiMovie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	IMDbID        string  `json:"imdb_id"`
	Runtime       int     `json:"runtime"`
	Status        string  `json:"status"`
	Homepage      string  `json:"homepage"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	FirstAirDate string `json:"first_air_date"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	EpisodeRuns  []int  `json:"episode_run_time"`
}

type searchResp struct {
	Results []apiMovie `json:"results"`
}

type findResp struct {
	MovieResults []apiMovie `json:"movie_results"`
	TVResults    []apiMovie `json:"tv_results"`
}

// ============ 对外方法 ============

// Match 依据标题/年份/IMDb ID 找到 TMDb 条目。
// 优先按 IMDb ID 精确匹配，失败再退回按标题+年份搜索。
// 未启用时返回 (nil, false, nil)；网络异常返回 error 供调用方降级。
func (c *Client) Match(ctx context.Context, title string, year int, imdbID, mediaType string) (*Match, bool, error) {
	if !c.Enabled() {
		return nil, false, nil
	}
	if id := normalizeIMDb(imdbID); id != "" {
		m, ok, err := c.findByIMDb(ctx, id)
		if err == nil && ok {
			return m, true, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, false, nil
	}
	return c.searchByTitle(ctx, title, year, mediaType)
}

// Details 按 TMDb ID 取详情（含 IMDb ID、类型、时长）。
func (c *Client) Details(ctx context.Context, tmdbID int64, mediaType string) (*Match, error) {
	if !c.Enabled() || tmdbID <= 0 {
		return nil, nil
	}
	kind := normalizeMediaType(mediaType)
	var out apiMovie
	if err := c.getJSON(ctx, fmt.Sprintf("/%s/%d", kind, tmdbID), nil, &out); err != nil {
		return nil, err
	}
	m := c.toMatch(out, kind)
	return &m, nil
}

// ImageURL 拼出可访问的图片地址；传入相对路径（如 /abc.jpg）或完整 URL。
// size 例如 "w500"、"w780"、"original"。
func (c *Client) ImageURL(path, size string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if size == "" {
		size = "w500"
	}
	return ImageBaseURL + "/" + size + path
}

// ============ 内部实现 ============

func (c *Client) findByIMDb(ctx context.Context, imdbID string) (*Match, bool, error) {
	var out findResp
	if err := c.getJSON(ctx, "/find/"+url.PathEscape(imdbID), url.Values{"external_source": {"imdb_id"}}, &out); err != nil {
		return nil, false, err
	}
	if len(out.MovieResults) > 0 {
		m := c.toMatch(out.MovieResults[0], "movie")
		return &m, true, nil
	}
	if len(out.TVResults) > 0 {
		m := c.toMatch(out.TVResults[0], "tv")
		return &m, true, nil
	}
	return nil, false, nil
}

func (c *Client) searchByTitle(ctx context.Context, title string, year int, mediaType string) (*Match, bool, error) {
	kinds := []string{normalizeMediaType(mediaType)}
	if mediaType == "" {
		kinds = []string{"movie", "tv"}
	}
	for _, kind := range kinds {
		params := url.Values{"query": {title}}
		if year > 0 {
			if kind == "tv" {
				params.Set("first_air_date_year", strconv.Itoa(year))
			} else {
				params.Set("year", strconv.Itoa(year))
			}
		}
		var resp searchResp
		if err := c.getJSON(ctx, "/search/"+kind, params, &resp); err != nil {
			return nil, false, err
		}
		best := pickBest(resp.Results, title, year)
		if best == nil {
			continue
		}
		// 搜索结果不含 genres/imdb_id/runtime，按需补一次详情
		if detail, err := c.Details(ctx, best.ID, kind); err == nil && detail != nil {
			return detail, true, nil
		}
		m := c.toMatch(*best, kind)
		return &m, true, nil
	}
	return nil, false, nil
}

// pickBest 在同名结果中挑年份最接近的，其次取热度最高的。
func pickBest(results []apiMovie, title string, year int) *apiMovie {
	if len(results) == 0 {
		return nil
	}
	bestIdx := 0
	bestScore := -1.0
	for i := range results {
		r := results[i]
		score := float64(r.VoteCount) / 100.0
		if year > 0 {
			if ry := releaseYear(r); ry > 0 {
				diff := ry - year
				if diff < 0 {
					diff = -diff
				}
				switch {
				case diff == 0:
					score += 1000
				case diff <= 1:
					score += 100
				default:
					score -= float64(diff) * 10
				}
			}
		}
		if normTitle(r.Title) == normTitle(title) || normTitle(r.Name) == normTitle(title) {
			score += 50
		}
		if score > bestScore {
			bestScore, bestIdx = score, i
		}
	}
	return &results[bestIdx]
}

func (c *Client) toMatch(m apiMovie, kind string) Match {
	out := Match{
		TMDbID:        m.ID,
		IMDbID:        normalizeIMDb(m.IMDbID),
		MediaType:     kind,
		Title:         firstNonEmpty(m.Title, m.Name),
		OriginalTitle: firstNonEmpty(m.OriginalTitle, m.OriginalName),
		Year:          releaseYear(m),
		Overview:      strings.TrimSpace(m.Overview),
		Rating:        m.VoteAverage,
		VoteCount:     m.VoteCount,
		RuntimeMin:    m.Runtime,
		ReleaseDate:   firstNonEmpty(m.ReleaseDate, m.FirstAirDate),
		Status:        m.Status,
		Homepage:      m.Homepage,
	}
	if len(m.EpisodeRuns) > 0 {
		out.RuntimeMin = m.EpisodeRuns[0]
	}
	for _, g := range m.Genres {
		if g.Name != "" {
			out.Genres = append(out.Genres, g.Name)
		}
	}
	// 优先竖版海报；没有海报时退回剧照，避免前端空白
	out.PosterURL = firstNonEmpty(c.ImageURL(m.PosterPath, "w500"), c.ImageURL(m.BackdropPath, "w780"))
	out.BackdropURL = c.ImageURL(m.BackdropPath, "w1280")
	if out.TMDbID > 0 {
		out.TMDbURL = fmt.Sprintf("https://www.themoviedb.org/%s/%d?language=%s", kind, out.TMDbID, c.cfg.Language)
	}
	return out
}

func (c *Client) getJSON(ctx context.Context, path string, params url.Values, out interface{}) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.cfg.APIKey)
	params.Set("language", c.cfg.Language)
	endpoint := c.cfg.BaseURL + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("TMDb 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("读取 TMDb 响应失败: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("TMDb API Key 无效（HTTP 401）")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("TMDb 返回 HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析 TMDb 响应失败: %w", err)
	}
	return nil
}

func normalizeMediaType(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), "tv") {
		return "tv"
	}
	return "movie"
}

func normalizeIMDb(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || !strings.HasPrefix(strings.ToLower(id), "tt") {
		return ""
	}
	return id
}

func releaseYear(m apiMovie) int {
	date := firstNonEmpty(m.ReleaseDate, m.FirstAirDate)
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
}

func normTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), "")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
