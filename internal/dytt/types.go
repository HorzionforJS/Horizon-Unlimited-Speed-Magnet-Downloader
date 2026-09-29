// Package dytt 抓取电影天堂（dytt.org.cn）的影视元数据：封面、名称、简介，
// 并给出用于磁力检索的精确关键词。
//
// 说明：home.dytiantang.com.cn 只是一个外壳页面，它用全屏 iframe 加载 dytt.org.cn，
// 首页卡片是写死的占位数据（封面为 CSS 渐变）。真实内容源是 dytt.org.cn，
// 因此这里直接对接内容源，减少一跳并避免解析到假数据。
package dytt

// MovieSummary 是列表页/搜索页的一张卡片。
type MovieSummary struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Poster    string   `json:"poster"`
	Score     float64  `json:"score"` // 0 表示无评分
	Status    string   `json:"status"`
	Category  string   `json:"category"`
	Tags      []string `json:"tags"`
	UpdatedAt string   `json:"updated_at"`
}

// Episode 是一集（或一条播放线路中的一项）。
type Episode struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Source string `json:"source"`
}

// PlaySource 是一条播放线路。
type PlaySource struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MovieDetail 是详情页的完整元数据。
type MovieDetail struct {
	ID          int64          `json:"id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Poster      string         `json:"poster"`
	Summary     string         `json:"summary"`
	Score       float64        `json:"score"`
	Year        int            `json:"year"`
	Category    string         `json:"category"`
	Actors      []string       `json:"actors"`
	Genres      []string       `json:"genres"`
	Region      string         `json:"region"`
	Language    string         `json:"language"`
	FirstAired  string         `json:"first_aired"`
	UpdatedAt   string         `json:"updated_at"`
	Episodes    []Episode      `json:"episodes"`
	EpisodeNum  int            `json:"episode_count"`
	PlaySources []PlaySource   `json:"play_sources"`
	Related     []MovieSummary `json:"related"`

	// MagnetQueries 是用于在磁力搜索引擎/下载器里检索的精确关键词。
	// 源站只提供在线播放，不含 magnet 链接，故这里不伪造链接。
	MagnetQueries []string `json:"magnet_queries"`
}

// FetchState 说明这份数据是怎么来的。
//
// Stale 必须单独暴露出来：源站故障时接口会拿过期缓存顶上，
// 客户端若把它当实时数据展示，用户就会看到「三天没更新」而以为站点挂了。
type FetchState struct {
	FromCache bool `json:"from_cache"` // 命中未过期的内存缓存
	Stale     bool `json:"stale"`      // 源站不可用，回退到过期缓存
}

// Page 是列表页的一页结果。
type Page struct {
	Category   string         `json:"category"`
	Page       int            `json:"page"`
	TotalPages int            `json:"total_pages"`
	HasNext    bool           `json:"has_next"`
	NextPage   int            `json:"next_page"`
	Count      int            `json:"count"`
	Items      []MovieSummary `json:"items"`
	FromCache  bool           `json:"from_cache"`
	Stale      bool           `json:"stale"`
}
