// Package torsearch 提供「中文片名 -> 英文检索词 -> 相关性过滤」的种子搜索能力。
//
// 实现依据均为实测结论：
//   - The Pirate Bay（apibay.org）索引几乎只含英文片名。中文关键词会被当成无效词，
//     从而倒回满屏无关的热门种子（实测搜「云雀叫天录」返回《蜘蛛侠》《Project Hail Mary》，
//     搜「流浪地球」「三体」等一律返回 100 条且含关键词中文的为 0 条）。
//   - 但用翻译得到的英文片名检索是有效的：实测「流浪地球」-> "The Wandering Earth"
//     返回 40 条且全部相关，"Ne Zha" 返回 39 条，"Ip Man" 返回 100 条。
//   - apibay 的 cat=0（全部）与「逐个影视分类」覆盖的**不是**同一批种子，实测各有独占
//     （2026-09-29 复测）：「Infernal Affairs」分类并集 40 条里 12 条是 cat=0 没有的，
//     而 cat=0 的 79 条里有 51 条分类并集没有；「Dune Part Two」分类并集 0 条、cat=0 有 97 条。
//     因此「全部」下两者都要打——早先「cat=0 对多词查询返回 0 条」的结论是上游抖动，
//     不是参数语义，别再据此把 cat=0 排除掉。
//   - torrents-csv 的 order_by 被忽略（四种排序返回内容 md5 完全相同），
//     所以「多拿结果」只能靠换检索词，不能靠换排序，见 csvVariants。
package torsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTPBBase      = "https://apibay.org"
	defaultCSVBase      = "https://torrents-csv.com"
	defaultTransmartURL = "https://transmart.qq.com/api/imt"
	transmartClientKey  = "browser-chrome-110.0.0"
	maxResults          = 300

	// csvPageSize 是 torrents-csv 单次请求期望拿回的条数。
	// 实测该站上限约 25 条：给更大的值不报错，但也不会多给，
	// 所以「更多结果」靠同一关键词换排序各取一遍，见 fetchCSV。
	csvPageSize = 25
)

// ErrEmptyQuery 表示调用方没有提供关键词。
var ErrEmptyQuery = errors.New("缺少搜索关键词")

// Item 与 apibay 返回结构保持一致。
// apibay 把数字也编码成字符串，这里原样保留，避免改变既有客户端解析逻辑。
type Item struct {
	Name     string `json:"name"`
	NameZh   string `json:"name_zh,omitempty"` // 片名中文（技术标签保留英文），翻译失败时为空
	InfoHash string `json:"info_hash"`
	Category string `json:"category,omitempty"`
	Size     string `json:"size,omitempty"`
	Seeders  string `json:"seeders,omitempty"`
	Leechers string `json:"leechers,omitempty"`
	NumFiles string `json:"num_files,omitempty"`
	Status   string `json:"status,omitempty"`
	// Added 是种子发布时间（Unix 秒）。apibay 返回 added，torrents-csv 返回
	// created_unix，两路都归一到这个字段；0 表示上游没给。
	Added string `json:"added,omitempty"`
}

// Result 是一次检索的完整结果，含翻译与过滤过程信息，便于客户端给出明确反馈。
type Result struct {
	Query        string `json:"query"`         // 用户原始关键词
	SearchTerm   string `json:"search_term"`   // 实际用于检索的关键词
	Translated   string `json:"translated"`    // 翻译结果（空表示未翻译）
	TranslatedOK bool   `json:"translated_ok"` // 是否成功翻译
	Results      []Item `json:"results"`
	FilteredOut  int    `json:"filtered_out"`  // 与片名无关而被丢弃的条数
	NameZhCount  int    `json:"name_zh_count"` // 回填了中文片名的条数
	Limit        int    `json:"limit"`         // 最终返回的最大条数（0 表示未指定）
	// Sources 是本次真正贡献了结果的索引源；SourceErrors 是试过但失败的源。
	// 两个都回给客户端，是为了让「结果变少了」可解释：是源头挂了，还是本来就没有。
	Sources      []string `json:"sources,omitempty"`
	SourceErrors []string `json:"source_errors,omitempty"`
	Hint         string   `json:"hint,omitempty"`
}

// Config 控制检索与翻译行为。
type Config struct {
	TPBBase          string
	TransmartURL     string
	DisableTranslate bool          // true 时不做中英转换（HORIZON_TRANSLATE_ENABLED=false）
	TranslateTTL     time.Duration // 翻译结果缓存时长
	HTTPTimeout      time.Duration
	TranslateTimeout time.Duration // 单次翻译请求超时（结果片名回显不该拖慢搜索）
	SearchBudget     time.Duration // 整条搜索链路（含翻译与分类检索）的总预算，防止累计耗时超过客户端等待
	MaxResultCount   int           // 分类检索的最大总条数，每个分类拿多少靠得出来
	SearchCatsAll    []string      // cat=0（全部）时归并检索的分类
	CSVBase          string        // torrents-csv 索引站地址；留空且未禁用时用内置默认值
	DisableCSV       bool          // true 时只用 apibay
	CSVTimeout       time.Duration // 单次 csv 请求超时
}

func (c Config) withDefaults() Config {
	out := c
	if out.TPBBase == "" {
		out.TPBBase = defaultTPBBase
	}
	if out.TransmartURL == "" {
		out.TransmartURL = defaultTransmartURL
	}
	if out.TranslateTTL <= 0 {
		out.TranslateTTL = 24 * time.Hour
	}
	if out.HTTPTimeout <= 0 {
		out.HTTPTimeout = 15 * time.Second
	}
	if out.TranslateTimeout <= 0 {
		// 实测一次批量翻译（含冷启动）约 6~9 秒；给 12 秒余量，超时就放弃中文回显，
		// 不影响搜索结果本身。
		out.TranslateTimeout = 12 * time.Second
	}
	if out.SearchBudget <= 0 {
		// 端到端预算。客户端等 25 秒，服务端必须在它之前干干净净地交业务结果，
		// 否则会出现「服务端还在等上游、客户端已经放弃」的白等，用户看到的是搜索失败。
		out.SearchBudget = 20 * time.Second
	}
	if out.MaxResultCount <= 0 {
		// apibay 单个分类最多返回 100 条；只要第一个分类就拿到足够的候选，
		// 后续分类就不必再请求（宁可少要一点广度，也不让用户多等）。
		out.MaxResultCount = 120
	}
	if len(out.SearchCatsAll) == 0 {
		// apibay 的电影/剧集分类；「全部」只覆盖影视类，符合本应用的用途。
		out.SearchCatsAll = []string{"207", "208", "205"}
	}
	if out.CSVBase == "" && !out.DisableCSV {
		out.CSVBase = defaultCSVBase
	}
	if out.CSVTimeout <= 0 {
		// csv 常态 0.8s 左右，给到 8 秒是容忍上游抖动。
		// 真卡住时靠这个超时把它拖出主链路，不影响 apibay 那一路的结果。
		out.CSVTimeout = 8 * time.Second
	}
	out.TPBBase = strings.TrimRight(out.TPBBase, "/")
	out.TransmartURL = strings.TrimRight(out.TransmartURL, "/")
	return out
}

// ConfigFromEnv 从环境变量读取配置。
func ConfigFromEnv() Config {
	c := Config{
		TPBBase:      os.Getenv("HORIZON_TPB_BASE"),
		TransmartURL: os.Getenv("HORIZON_TRANSLATE_URL"),
		CSVBase:      os.Getenv("HORIZON_CSV_BASE"),
	}
	if v := strings.TrimSpace(os.Getenv("HORIZON_TRANSLATE_ENABLED")); v != "" {
		c.DisableTranslate = strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "no")
	}
	if v := strings.TrimSpace(os.Getenv("HORIZON_CSV_ENABLED")); v != "" {
		c.DisableCSV = strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "no")
	}
	if v := strings.TrimSpace(os.Getenv("HORIZON_TRANSLATE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.TranslateTTL = d
		}
	}
	return c
}

type cacheEntry struct {
	text string
	at   time.Time
}

// Searcher 负责翻译与检索；并发安全，可长期复用。
//
// cache 缓存「中->英」（检索用），cacheRev 缓存「英->中」（结果片名回显用）。
// 两者分开，避免不同方向的同一条文本互相覆盖。
type Searcher struct {
	cfg      Config
	http     *http.Client
	mu       sync.Mutex
	cache    map[string]cacheEntry
	cacheRev map[string]cacheEntry
}

// New 创建检索器。
func New(cfg Config) *Searcher {
	c := cfg.withDefaults()
	return &Searcher{cfg: c, http: &http.Client{Timeout: c.HTTPTimeout}, cache: map[string]cacheEntry{}, cacheRev: map[string]cacheEntry{}}
}

var (
	defMu sync.RWMutex
	defS  *Searcher
)

// Default 返回进程级默认检索器（首次调用时按环境变量初始化）。
func Default() *Searcher {
	defMu.RLock()
	s := defS
	defMu.RUnlock()
	if s != nil {
		return s
	}
	defMu.Lock()
	defer defMu.Unlock()
	if defS == nil {
		defS = New(ConfigFromEnv())
	}
	return defS
}

// SetDefault 替换默认检索器，供测试注入。
func SetDefault(s *Searcher) {
	defMu.Lock()
	defS = s
	defMu.Unlock()
}

// Search 使用默认检索器检索。
func Search(ctx context.Context, q, cat string) (*Result, error) {
	return Default().Search(ctx, q, cat)
}

// Search 检索种子：中文关键词先翻译成英文片名，再按相关性过滤无关结果。
func (s *Searcher) Search(ctx context.Context, q, cat string) (*Result, error) {
	return s.SearchLimit(ctx, q, cat, 0)
}

// SearchLimit 同 Search，但可以限定返回条数（limit<=0 表示用默认上限）。
//
// 限制条数不只是为了少传数据：每多返回一条，就多一条片名要翻译。
// 搜「avatar」这种宽泛的词最多能命中 200 条，全部翻一遍要 10 秒以上，而用户根本
// 不会翻到第 200 条。
func (s *Searcher) SearchLimit(ctx context.Context, q, cat string, limit int) (*Result, error) {
	query := strings.TrimSpace(q)
	if query == "" {
		return nil, ErrEmptyQuery
	}
	res := &Result{Query: query, Results: []Item{}}

	// 翻译与分类检索共用一个总预算：否则「翻译 12s + 3 个分类 × 15s」最坏可达 57 秒，
	// 远超客户端的等待上限，会演变成用户眼里的「搜索失败」。
	if s.cfg.SearchBudget > 0 {
		var cancelBudget context.CancelFunc
		ctx, cancelBudget = context.WithTimeout(ctx, s.cfg.SearchBudget)
		defer cancelBudget()
	}

	term := query
	if HasCJK(query) && !s.cfg.DisableTranslate {
		en, err := s.Translate(ctx, query)
		switch {
		case err != nil:
			res.Hint = "中文关键词自动翻译失败，已按原词检索。建议改用英文片名，或使用云端下载。"
		case en != "" && !strings.EqualFold(en, query):
			term = en
			res.Translated = en
			res.TranslatedOK = true
		}
	}
	res.SearchTerm = term

	// 分类检索只占总预算的一部分，保证「中文片名回显」能拿到剩下的时间。
	// 否则上游一拖，就会把后面的翻译挤没，用户只能看到一堆英文种子名。
	fetchCtx := ctx
	if s.cfg.SearchBudget > 0 {
		var cancelFetch context.CancelFunc
		fetchCtx, cancelFetch = context.WithTimeout(ctx, s.cfg.SearchBudget*6/10)
		defer cancelFetch()
	}

	// ---- 多源并发检索 ----
	//
	// 这里同时打 apibay 与 torrents-csv 两条路，再合并去重。
	// 单源时代的问题很实在：apibay 只索引英文片名，且长尾片源偏少，
	// 中文片名一多就会出现「同一部片只有两三条种子」。csv 库更杂、更新更快，
	// 两边合起来覆盖面明显变宽。
	//
	// 并发而不是串行，是因为两条路各自 0.8s 左右，串起来就是 1.6s；
	// 而且任何一条挂了都不能拖住另一条。
	type srcResult struct {
		name  string
		items []Item
		err   error
	}

	cats := s.catsFor(cat)
	// 除了逐个分类，再打一次 cat=0（全站）。
	//
	// 实测 apibay 的 cat=0 与「分类并集」覆盖的不是同一批种子，两边各有独占：
	//   Infernal Affairs：并集 40 条里有 12 条 cat=0 没有，cat=0 的 79 条里有 51 条并集没有
	//   Dune Part Two  ：并集 0 条，cat=0 有 97 条
	// 所以「全部」下两者都要打，只取其一都会漏掉一大片。
	// 用户显式选了某个分类时不做这一步：那时用户要的就是这个分类。
	fetchAll := strings.TrimSpace(cat) == "" || strings.TrimSpace(cat) == "0"
	slots := len(cats) + 1
	if fetchAll {
		slots++
	}
	var wg sync.WaitGroup
	ch := make(chan srcResult, slots)

	for _, c := range cats {
		wg.Add(1)
		go func(catID string) {
			defer wg.Done()
			got, err := s.fetch(fetchCtx, term, catID)
			ch <- srcResult{name: "tpb:" + catID, items: got, err: err}
		}(c)
	}

	if fetchAll {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.fetch(fetchCtx, term, "0")
			ch <- srcResult{name: "tpb:all", items: got, err: err}
		}()
	}

	if s.csvEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.fetchCSV(ctx, term, cat)
			ch <- srcResult{name: "csv", items: got, err: err}
		}()

		// 中文原词也查一遍 csv。
		//
		// csv 与 apibay 的库不一样：apibay 基本只收英文片名，csv 里却有一批
		// 「[DBD-Raws][流浪地球][1080P]」「[电影天堂]流浪地球-2019.mp4」这种
		// 纯中文名的种子，用翻译后的英文词一条都查不到。实测「流浪地球」由此
		// 多出 3 条，全是 apibay 里没有的。
		if !strings.EqualFold(strings.TrimSpace(query), strings.TrimSpace(term)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := s.fetchCSV(ctx, query, cat)
				ch <- srcResult{name: "csv:zh", items: got, err: err}
			}()
		}
	}

	go func() { wg.Wait(); close(ch) }()

	var items []Item
	var lastErr error
	fetched := false
	for r := range ch {
		if r.err != nil {
			lastErr = r.err
			res.SourceErrors = append(res.SourceErrors, r.name+": "+r.err.Error())
			continue
		}
		fetched = true
		if len(r.items) > 0 {
			res.Sources = append(res.Sources, r.name)
		}
		items = append(items, r.items...)
	}
	sort.Strings(res.Sources)
	sort.Strings(res.SourceErrors)

	// 一个源都没跑通且带着错误：这才是真的失败，报错给客户端。
	// 注意「跑通但没结果」不算失败——那只是这个关键词没片源。
	if !fetched && lastErr != nil {
		if ctx.Err() != nil || fetchCtx.Err() != nil {
			res.Hint = "本次检索超时，尚未获取到结果，请稍后重试。"
			return res, nil
		}
		return nil, lastErr
	}

	items = dedupe(items)
	toks := tokens(term)
	if len(toks) == 0 {
		toks = tokens(query)
	}

	kept := make([]Item, 0, len(items))
	for _, it := range items {
		if relevant(it.Name, toks) {
			kept = append(kept, it)
		}
	}
	res.FilteredOut = len(items) - len(kept)

	// 排序：先按做种数，其次按发布时间。
	//
	// 纯按做种排会让「三年前的老片源」永远压着「上个月的新片源」——做种数天然
	// 偏向旧种子（积累时间长）。而本站用户经常就是想找刚出的片。
	// 这里用分档而不是加权求和：做种数差一个数量级才算「更热」，
	// 否则让新的排前面。避免出现「1 个做种的 4K 新片」压过「300 做种的老片」。
	sort.Slice(kept, func(i, j int) bool {
		si, sj := atoi(kept[i].Seeders), atoi(kept[j].Seeders)
		if seedTier(si) != seedTier(sj) {
			return si > sj
		}
		return atoi(kept[i].Added) > atoi(kept[j].Added)
	})
	want := maxResults
	if limit > 0 && limit < want {
		want = limit
	}
	if len(kept) > want {
		kept = kept[:want]
	}
	// 回填中文片名。放在排序截断之后：只翻译真正要返回给客户端的那一批，
	// 避免为即将丢弃的结果白花翻译配额。
	// 用独立超时：翻译只是「锦上添花」，不该拖慢或拖垮搜索本身。
	if s.cfg.TranslateTimeout > 0 {
		tctx, cancel := context.WithTimeout(ctx, s.cfg.TranslateTimeout)
		s.enrichNameZh(tctx, kept)
		cancel()
	} else {
		s.enrichNameZh(ctx, kept)
	}

	res.Results = kept
	res.Limit = want
	for i := range kept {
		if kept[i].NameZh != "" {
			res.NameZhCount++
		}
	}
	res.Hint = hintFor(res)
	return res, nil
}

// nameZhChunkSize 是每批翻译的条数。
//
// 一次全量翻译要么全成、要么全败：超时一秒就连一个中文名都拿不到。
// 分批之后，已完成的批次会保留（且进入缓存），用户至少能看到前面几十条的中文名，
// 而不是整页英文。实测单批翻译耗时几乎不随条数增长，批 40 条比批 8 条慢不到一倍。
const nameZhChunkSize = 40

// enrichNameZh 为结果回填中文片名（NameZh）。
//
// 关键点：绝不能把整个种子名丢给翻译。「The.Wandering.Earth.2019.DUBBED.1080p.WEBRip」
// 这种串会把画质、封装、组名一起翻烂（实测会得到「流浪地球2019配音1080p」之类没法看的东西）。
// 正确做法是先切出片名去翻，再把年份/季集/画质等技术标签原样拼回来。
//
// 翻译整批失败不影响搜索结果，只是没有中文回显。
func (s *Searcher) enrichNameZh(ctx context.Context, items []Item) {
	if s.cfg.DisableTranslate || len(items) == 0 {
		return
	}
	for start := 0; start < len(items); start += nameZhChunkSize {
		end := start + nameZhChunkSize
		if end > len(items) {
			end = len(items)
		}
		// 每批都先看总预算：额度用完就停，已回填的中文名保留。
		if ctx.Err() != nil {
			return
		}
		batch := items[start:end]
		titles := make([]string, len(batch))
		for i := range batch {
			titles[i] = titleOf(batch[i].Name)
		}
		zh, err := s.TranslateBatch(ctx, titles)
		if err != nil || len(zh) != len(batch) {
			// 这一批失败（超时）就不再继续了，继续只会再拖一次超时。
			return
		}
		for i := range batch {
			batch[i].NameZh = joinNameZh(zh[i], batch[i].Name)
		}
	}
}

func hintFor(res *Result) string {
	if res.Hint != "" {
		return res.Hint // 保留翻译失败的提示
	}
	if len(res.Results) > 0 {
		return ""
	}
	if len(res.SourceErrors) > 0 && len(res.Sources) > 0 {
		// 有结果但少了源头：告诉用户结果不完整，而不是让他以为「就这么多」。
		return "部分索引源暂时不可用（" + strings.Join(res.SourceErrors, "；") +
			"），本次结果可能偏少，稍后重试或换个关键词。"
	}
	if res.FilteredOut > 0 {
		return fmt.Sprintf("没有与「%s」相关的种子（已过滤 %d 条无关结果）。该片可能缺少英文资源，建议改用云端下载，或换个片名再试。",
			res.Query, res.FilteredOut)
	}
	return "未找到相关种子，请换个关键词再试。"
}

// catsFor 把「全部」展开为若干影视分类。
//
// 注意这只给出了分类那一路；cat=0（全站）由调用方额外补一次，见 SearchLimit。
// 两者覆盖的种子集合不同，缺一都会漏。
func (s *Searcher) catsFor(cat string) []string {
	cat = strings.TrimSpace(cat)
	if cat == "" || cat == "0" {
		return s.cfg.SearchCatsAll
	}
	return []string{cat}
}

func (s *Searcher) fetch(ctx context.Context, q, cat string) ([]Item, error) {
	u := s.cfg.TPBBase + "/q.php?q=" + url.QueryEscape(q) + "&cat=" + url.QueryEscape(cat)
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var raw []Item
	if err := json.Unmarshal(body, &raw); err != nil {
		// apibay 偶发返回对象或异常内容，按「无结果」处理。
		return nil, nil
	}
	out := make([]Item, 0, len(raw))
	for _, it := range raw {
		if it.InfoHash == "" || it.Name == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(it.Name), "No results returned") {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// csvEnabled 报告是否启用 torrents-csv 这一路。
func (s *Searcher) csvEnabled() bool {
	return !s.cfg.DisableCSV && strings.TrimSpace(s.cfg.CSVBase) != ""
}

// csvResp 是 torrents-csv /service/search 的响应。
//
// 该站字段与 apibay 不同：infohash 是小写、大小为 size_bytes 数字、
// 时间为 created_unix；seeders/leechers 是它自己抓的 scrape 结果，
// 比 apibay 的缓存值更新，同一条种子常常数字更高。
type csvResp struct {
	Torrents []struct {
		InfoHash    string `json:"infohash"`
		Name        string `json:"name"`
		SizeBytes   int64  `json:"size_bytes"`
		CreatedUnix int64  `json:"created_unix"`
		Seeders     int    `json:"seeders"`
		Leechers    int    `json:"leechers"`
		Completed   int    `json:"completed"`
	} `json:"torrents"`
}

// csvOrder 是取数用的排序方式。
//
// 曾经并发发过三种排序（seeders / created_unix / completed），指望「换排序各取
// 一遍」能多拿结果。实测（2026-09-29，四个不同关键词）三种排序返回的内容
// md5 完全相同——该站的 order_by 参数实际上被忽略，那两次请求是纯浪费。
//
// 单次上限 25 条且不能翻页（size=1000 也只回 25 条，offset/page 均无效）。
// 想扩大覆盖只能靠「多给几个检索词」，即 fetchCSV 里对同一关键词的变形各查一次。
var csvOrder = "seeders"

// csvVariants 给出同一检索词的若干变形。
//
// 该站单次最多 25 条且不能翻页，而它的检索按整串匹配，
// 所以「前缀词/长尾词」能捞到不同的结果集。实测（2026-09-29）：
//
//	The Wandering Earth -> 25 条，拆出 "Earth" 再查得到另外 25 条，并集 50 条
//	Infernal Affairs    -> 15 条，拆出 "Infernal"/"Affairs" 并集 52 条
//
// 只在整串结果偏少时才展开：热门片整串就有 25 条（已到上限），
// 再展开也只是重复，白白多加 2~4 个请求。
func csvVariants(term string) []string {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil
	}
	out := []string{term}
	words := strings.Fields(term)
	if len(words) < 2 {
		return out
	}
	seen := map[string]bool{strings.ToLower(term): true}
	// 去掉过于宽泛的冠词/介词，否则 "The" 这种词会拉回一堆无关结果
	skip := map[string]bool{"the": true, "a": true, "an": true, "of": true, "and": true, "in": true}
	add := func(w string) {
		w = strings.TrimSpace(w)
		if len(w) < 4 || skip[strings.ToLower(w)] {
			return
		}
		key := strings.ToLower(w)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, w)
	}
	// 只产出「整串」类变形，绝不产出单个词。
	//
	// 单词变形的召回确实更高（实测 "Earth" 单独一查就有 25 条），但那些结果
	// 绝大多数与目标片无关，靠相关性过滤是滤不干净的：目标片名本身就含这个词，
	// 任何含同一泛词的内容都能通过校验。拿准确度换来的这几十条，不值。
	//
	// 去首词（多为 "The"/"A"）的整串仍然是一个精确的片名，可以要。
	if len(words) > 2 && len(strings.Join(words[1:], " ")) >= 4 {
		add(strings.Join(words[1:], " "))
	}
	return out
}

// fetchCSV 从 torrents-csv 取种子并归一成 Item。
//
// 先拿整串；结果到了 25 条上限就说明还有更多，此时再按变形词补几次。
// 任何一次失败都只是少一份数据，不影响整条搜索；全部失败才返回错误。
func (s *Searcher) fetchCSV(ctx context.Context, term, cat string) ([]Item, error) {
	tctx := ctx
	if s.cfg.CSVTimeout > 0 {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, s.cfg.CSVTimeout)
		defer cancel()
	}

	first, err := s.csvQuery(tctx, term, cat)
	if err != nil {
		return nil, err
	}
	// 没到上限就没有更多了，不必再花钱。
	if len(first) < csvPageSize {
		return first, nil
	}

	variants := csvVariants(term)
	if len(variants) < 2 {
		return first, nil
	}

	type part struct {
		items []Item
		err   error
	}
	ch := make(chan part, len(variants)-1)
	for _, v := range variants[1:] {
		go func(v string) {
			got, err := s.csvQuery(tctx, v, cat)
			ch <- part{items: got, err: err}
		}(v)
	}

	out := first
	for i := 0; i < len(variants)-1; i++ {
		p := <-ch
		// 变形词的失败不影响整串的结果，静默跳过即可
		if p.err == nil {
			out = append(out, p.items...)
		}
	}
	return out, nil
}

func (s *Searcher) csvQuery(ctx context.Context, term, cat string) ([]Item, error) {
	u := s.cfg.CSVBase + "/service/search?q=" + url.QueryEscape(term) +
		"&size=" + strconv.Itoa(csvPageSize) +
		"&order_by=" + csvOrder + "&order=desc"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var resp csvResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil // 上游偶发返回非 JSON，按空结果处理
	}
	out := make([]Item, 0, len(resp.Torrents))
	for _, t := range resp.Torrents {
		// csv 返回的 infohash 已经是小写十六进制；统一转大写，
		// 否则同一条种子在 apibay（大写）与 csv（小写）之间去不掉重。
		h := strings.ToUpper(strings.TrimSpace(t.InfoHash))
		if len(h) != 40 || strings.TrimSpace(t.Name) == "" {
			continue
		}
		// csv 不返回分类。用户明确选了「电影/剧集」时按用户的选择标注，
		// 否则留空让客户端显示「其他」——总比瞎猜一个分类强。
		category := ""
		if cat != "" && cat != "0" {
			category = cat
		}
		out = append(out, Item{
			Name:     t.Name,
			InfoHash: h,
			Category: category,
			Size:     strconv.FormatInt(t.SizeBytes, 10),
			Seeders:  strconv.Itoa(t.Seeders),
			Leechers: strconv.Itoa(t.Leechers),
			Added:    strconv.FormatInt(t.CreatedUnix, 10),
		})
	}
	return out, nil
}

// mergeSeedStats 让同一个 info hash 只保留信息量最大的那一份。
//
// 两条路对同一条种子给的名字/做种数往往不一样：apibay 名字带分类风格前缀，
// csv 的 scrape 数字更新更高。这里按「谁做种多留谁」合并，但名字仍以先到者
// （apibay 优先）为准时更符合用户已经习惯的显示。
func mergeSeedStats(a, b Item) Item {
	ai, bi := atoi(a.Seeders), atoi(b.Seeders)
	if bi > ai {
		// 做种数用更高的那份，其余字段补空
		a.Seeders = b.Seeders
		if a.Size == "" {
			a.Size = b.Size
		}
	}
	if a.Leechers == "" {
		a.Leechers = b.Leechers
	}
	if a.Added == "" {
		a.Added = b.Added
	}
	return a
}

type transmartResp struct {
	Header struct {
		RetCode string `json:"ret_code"`
	} `json:"header"`
	AutoTranslation []string `json:"auto_translation"`
}

// Translate 把中文文本翻成英文（检索用）；结果按 TranslateTTL 缓存。
func (s *Searcher) Translate(ctx context.Context, text string) (string, error) {
	out, err := s.translate(ctx, text, "zh", "en", s.cache)
	return out, err
}

// translate 是翻译的通用实现，按 (src,tgt) 方向与对应缓存工作。
//
// 用 Map 传缓存而不是直接用字段，是为了让「中->英」（检索）与「英->中」（结果片名）
// 各自独立缓存、互不覆盖。
func (s *Searcher) translate(ctx context.Context, text, src, tgt string, cache map[string]cacheEntry) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	s.mu.Lock()
	if e, ok := cache[text]; ok && time.Since(e.at) < s.cfg.TranslateTTL {
		s.mu.Unlock()
		return e.text, nil
	}
	s.mu.Unlock()

	payload := map[string]interface{}{
		"header": map[string]string{
			"fn":         "auto_translation",
			"client_key": transmartClientKey,
		},
		"type":           "plain",
		"model_category": "normal",
		"source":         map[string]interface{}{"lang": src, "text_list": []string{text}},
		"target":         map[string]interface{}{"lang": tgt},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body, err := s.post(ctx, s.cfg.TransmartURL, buf)
	if err != nil {
		return "", err
	}
	var resp transmartResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Header.RetCode != "" && resp.Header.RetCode != "succ" {
		return "", fmt.Errorf("翻译接口返回 %s", resp.Header.RetCode)
	}
	if len(resp.AutoTranslation) == 0 {
		return "", nil
	}
	out := cleanTranslated(resp.AutoTranslation[0])

	s.mu.Lock()
	cache[text] = cacheEntry{text: out, at: time.Now()}
	pruneCacheLocked(cache, s.cfg.TranslateTTL)
	s.mu.Unlock()
	return out, nil
}

// TranslateBatch 批量把英文文本翻成中文，一次请求完成。
//
// 逐条调用会让「一次搜索 40 条结果」变成 40 次请求，既慢又容易触发上游限流；
// 实测一次请求带 20 条只需约 1.3 秒。返回的切片与入参一一对应，
// 长度不匹配或整体失败时返回 err，由调用方决定是否跳过中文回显。
func (s *Searcher) TranslateBatch(ctx context.Context, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([]string, len(texts))
	var missing []string
	var missingIdx []int

	s.mu.Lock()
	for i, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if e, ok := s.cacheRev[t]; ok && time.Since(e.at) < s.cfg.TranslateTTL {
			out[i] = e.text
			continue
		}
		missing = append(missing, t)
		missingIdx = append(missingIdx, i)
	}
	s.mu.Unlock()

	if len(missing) == 0 {
		return out, nil
	}

	payload := map[string]interface{}{
		"header": map[string]string{
			"fn":         "auto_translation",
			"client_key": transmartClientKey,
		},
		"type":           "plain",
		"model_category": "normal",
		"source":         map[string]interface{}{"lang": "en", "text_list": missing},
		"target":         map[string]interface{}{"lang": "zh"},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return out, err
	}
	body, err := s.post(ctx, s.cfg.TransmartURL, buf)
	if err != nil {
		return out, err
	}
	var resp transmartResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return out, err
	}
	if resp.Header.RetCode != "" && resp.Header.RetCode != "succ" {
		return out, fmt.Errorf("翻译接口返回 %s", resp.Header.RetCode)
	}
	if len(resp.AutoTranslation) != len(missing) {
		return out, fmt.Errorf("翻译接口返回 %d 条，期望 %d 条", len(resp.AutoTranslation), len(missing))
	}

	now := time.Now()
	s.mu.Lock()
	for k, idx := range missingIdx {
		zh := cleanTranslated(resp.AutoTranslation[k])
		out[idx] = zh
		s.cacheRev[missing[k]] = cacheEntry{text: zh, at: now}
	}
	pruneCacheLocked(s.cacheRev, s.cfg.TranslateTTL)
	s.mu.Unlock()
	return out, nil
}

func (s *Searcher) get(ctx context.Context, rawurl string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "horizon-downloader/1.0")
	req.Header.Set("Accept", "application/json")
	return s.do(req)
}

func (s *Searcher) post(ctx context.Context, rawurl string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawurl, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	return s.do(req)
}

func (s *Searcher) do(req *http.Request) ([]byte, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 HTTP %d", resp.StatusCode)
	}
	return data, nil
}

var (
	reParen   = regexp.MustCompile(`[（(][^）)]*[）)]`)
	reBracket = regexp.MustCompile(`\[[^\]]*\]`)
	reSpace   = regexp.MustCompile(`\s+`)
	reWord    = regexp.MustCompile(`[A-Za-z0-9]{3,}`)
	reNonWord = regexp.MustCompile(`[^a-z0-9]+`)

	// 片名与技术标签的切分规则（见 splitSeedName）。
	reLeadTag = regexp.MustCompile(`^\s*\[[^\]]*\]\s*`)                                   // 开头的 [TGx] / [LEAK] 之类组名标记
	reYearAt  = regexp.MustCompile(`[.\s(\[]((?:19|20)\d{2})[.\s)\]]`)                    // 分隔符包围的 4 位年份
	reSeason  = regexp.MustCompile(`(?i)[.\s_](S\d{1,2}(?:[.\s]?E\d{1,3})?)(?:[.\s_]|$)`) // S01E07 / S14E10
	reQuality = regexp.MustCompile(`(?i)[.\s(\[](?:2160p|1080p|720p|480p|4k|uhd|hdrip|webrip|web-?dl|web|bluray|blu-?ray|brrip|bdrip|dvdrip|hdtv|remux|hdcam|cam|ts|x264|x265|h\.?264|h\.?265|hevc|avc|aac|ac3|eac3|ddp?5|dts|truehd|atmos|hdr10?|dv|dolby|10bit|8bit|dual|dubbed|subbed|proper|repack|extended|remastered|complete|internal)\b`)
	reSep     = regexp.MustCompile(`[._\-]+`)
	reCJK     = regexp.MustCompile(`[\p{Han}]`) // 仅用于判断译文是否真的含中文
)

// splitSeedName 把种子名切成「片名 / 季集标记 / 剩余技术标签」三部分。
//
// 之所以要切，是因为整串翻译会把画质与组名一起翻烂。切分顺序是先找季集
// （剧集名的边界比年份可靠）、再找年份、最后退到画质词。
func splitSeedName(raw string) (title, marker, tail string) {
	s := reLeadTag.ReplaceAllString(strings.TrimSpace(raw), "")

	if m := reSeason.FindStringSubmatchIndex(s); m != nil {
		return normalizeName(s[:m[0]]), strings.ToUpper(reSpace.ReplaceAllString(s[m[2]:m[3]], "")), normalizeName(s[m[1]:])
	}
	if loc := reYearAt.FindStringIndex(s); loc != nil {
		return normalizeName(s[:loc[0]]), "", normalizeName(s[loc[0]:])
	}
	if loc := reQuality.FindStringIndex(s); loc != nil {
		return normalizeName(s[:loc[0]]), "", normalizeName(s[loc[0]:])
	}
	return normalizeName(s), "", ""
}

// normalizeName 把点/下划线/连字符换成空格并压缩空白，用于展示。
func normalizeName(s string) string {
	s = reSep.ReplaceAllString(s, " ")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.Trim(s, " -_.·|")
}

// tailNoise 是技术标签里对用户没有信息量的碎片（体积、音轨编号、组名后缀）。
var tailNoise = map[string]bool{
	"mb": true, "gb": true, "b": true, "kb": true,
	"5": true, "1": true, "2": true, "0": true, "7": true,
}

// tidyTail 去掉标签里的碎片，让「2019 DUBBED 1080p WEBRip 1400MB DD5 1 x264 GalaxyRG」
// 收敛成「2019 DUBBED 1080p WEBRip x264」。
func tidyTail(s string) string {
	fields := reSpace.Split(strings.TrimSpace(s), -1)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			continue
		}
		low := strings.ToLower(f)
		// 纯数字体积（1400MB / 1.4GB）丢掉
		if regexp.MustCompile(`^\d+(\.\d+)?(mb|gb|kb|b)$`).MatchString(low) {
			continue
		}
		// 音轨编号被切出来的孤立数字（DD5.1 -> "5" "1"）丢掉
		if tailNoise[low] {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// titleOf 返回用于翻译的片名部分（供批量翻译）。
func titleOf(raw string) string {
	title, _, _ := splitSeedName(raw)
	return title
}

// joinNameZh 把译好的中文片名与原种子名里的技术标签拼成展示用的中文标题。
//
// 例：「流浪地球」+ 原串 -> 「流浪地球 2019 DUBBED 1080p WEBRip x264」。
// 译文为空、没有中文、或与原文相同时返回空串，表示「这条不必显示中文」。
func joinNameZh(titleZh, raw string) string {
	titleZh = strings.TrimSpace(titleZh)
	if titleZh == "" {
		return ""
	}
	title, marker, tail := splitSeedName(raw)
	if title == "" {
		return ""
	}
	// 译文没翻出中文（如 "Futurama" -> "Futurama"）就不要冒充中文名
	if !reCJK.MatchString(titleZh) {
		return ""
	}
	// 译名与原名相同也跳过
	if strings.EqualFold(strings.TrimSpace(titleZh), title) {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleZh)
	if marker != "" {
		b.WriteString("  ")
		b.WriteString(marker)
	}
	if tt := tidyTail(tail); tt != "" {
		b.WriteString("  ")
		b.WriteString(tt)
	}
	return b.String()
}

// cleanTranslated 去掉翻译接口附带的括号注释与句末标点，例如
// "The Wandering Earth (电影名)" -> "The Wandering Earth"、"Nezha (a mythological person's name)" -> "Nezha"。
// 导出为 CleanTranslated，供接口层生成给用户看的检索词时复用同一套清洗规则。
func cleanTranslated(s string) string {
	s = reParen.ReplaceAllString(s, " ")
	s = reBracket.ReplaceAllString(s, " ")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.TrimRight(s, " .,;:!?、，。；：！？"))
}

// CleanTranslated 见 cleanTranslated。
func CleanTranslated(s string) string { return cleanTranslated(s) }

// HasCJK 判断是否含中日韩统一表意文字（需要走翻译）。
func HasCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0xF900 && r <= 0xFAFF) {
			return true
		}
	}
	return false
}

// stopWords 是画质/封装等技术词，不能作为「是否与片名相关」的判据。
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "its": true,
	"movie": true, "film": true, "part": true, "full": true, "complete": true,
	"bluray": true, "webrip": true, "web": true, "hdrip": true, "dvdrip": true,
	"hdtv": true, "brrip": true, "remux": true, "proper": true, "repack": true,
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true,
	"aac": true, "ac3": true, "dts": true, "ddp": true, "mp4": true, "mkv": true,
	"subs": true, "sub": true, "dual": true, "chinese": true, "mandarin": true,
	"1080p": true, "720p": true, "2160p": true, "480p": true, "4k": true,
	"season": true, "vol": true, "s01": true, "s02": true,
}

// tokens 提取用于相关性判断的关键词。
func tokens(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range reWord.FindAllString(strings.ToLower(s), -1) {
		if stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// relevant 判断种子名是否与检索词相关（全部词元都要命中）。
// 先把种子名去掉非字母数字再匹配，这样 "Ne Zha" 也能命中 "Nezha"。
func relevant(name string, toks []string) bool {
	if len(toks) == 0 {
		return true
	}
	// 种子名含中文时不做英文词过滤。
	//
	// reWord 只切得出英文词，中文片名在英文关键词下会被整条判成「无关」而丢掉。
	// 而 DBD-Raws、字幕组这类源恰恰是「[流浪地球][1080P]」这种中文名，
	// 正是中文用户最想要的那批。既然无从判断，就不要丢。
	if reCJK.MatchString(name) {
		return true
	}
	norm := reNonWord.ReplaceAllString(strings.ToLower(name), "")
	// 必须命中**全部**词元，而不是任意一个。
	//
	// 「任一命中」看起来更宽松、召回更高，实际上会被单个泛词带崩。真实回归
	// （2026-09-29）：为了让 torrents-csv 多吐结果，对「The Wandering Earth」
	// 追加了变形词查询（csvVariants），其中 "Earth" 这个单词命中了一大片无关内容——
	// 111 条结果里 57 条是 "Earth 3D Suite"、"Orb On the Movements of the Earth"、
	// "Alien Earth" 这类，相关性等于没有。
	//
	// 反面代价也要认：查「Dune Part Two」不会再返回 2021 年的《Dune》。这是
	// 刻意的取舍——用户搜什么片就给什么片，比多给两倍的近义词更有用。
	for _, t := range toks {
		if !strings.Contains(norm, t) {
			return false
		}
	}
	return true
}

func dedupe(items []Item) []Item {
	pos := map[string]int{}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		k := strings.ToLower(it.InfoHash)
		if k == "" {
			continue
		}
		if at, ok := pos[k]; ok {
			// 同一条种子出现在多个来源：合并做种数等信息，而不是简单丢弃后到者。
			out[at] = mergeSeedStats(out[at], it)
			continue
		}
		pos[k] = len(out)
		out = append(out, it)
	}
	return out
}

// pruneCacheLocked 清掉已过期的缓存条目。
//
// 缓存键来自用户输入与上游种子名，长期运行下是无界增长。只在写入路径做
// 惰性清理就够了：调用频率跟写入频率同阶，不需要额外的定时协程。
// 调用前必须已持有 s.mu。
func pruneCacheLocked(cache map[string]cacheEntry, ttl time.Duration) {
	if len(cache) < 256 {
		return
	}
	now := time.Now()
	for k, e := range cache {
		if now.Sub(e.at) >= ttl {
			delete(cache, k)
		}
	}
}

// seedTier 把做种数压成数量级档位，用于「热度优先、同档看新旧」的排序。
func seedTier(n int) int {
	switch {
	case n <= 0:
		return 0
	case n < 5:
		return 1
	case n < 20:
		return 2
	case n < 100:
		return 3
	case n < 500:
		return 4
	default:
		return 5
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
