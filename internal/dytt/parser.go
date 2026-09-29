package dytt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	reScore   = regexp.MustCompile(`(\d+(?:\.\d+)?)`)
	reDate    = regexp.MustCompile(`((?:19|20)\d{2}-\d{1,2}-\d{1,2})`)
	reYear    = regexp.MustCompile(`(?:19|20)\d{2}`)
	rePageTot = regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)
	reInfoKV  = regexp.MustCompile(`^([^:：]{1,12})[:：]\s*(.*)$`)
	reID      = regexp.MustCompile(`/p/(\d+)`)
	reSeason  = regexp.MustCompile(`[\s:：\-—_]*第[一二三四五六七八九十\d]+季$`)
	reSeasonN = regexp.MustCompile(`(?i)[\s:：\-—_]*season\s*\d+$`)
	reTail    = regexp.MustCompile(`(?i)[\s]*(?:国语|粤语|中字|高清)$`)
)

// invalidCategory 是页面导航/面包屑文案，不能当作影片类型。
var invalidCategory = map[string]bool{
	"首页": true, "电影": true, "电视剧": true, "影视剧": true, "短剧": true,
	"动漫": true, "综艺": true, "正文": true, "更多": true, "全部": true,
}

// parsePage 解析列表页/搜索页，返回卡片列表与分页信息。
func (c *Client) parsePage(body string) ([]MovieSummary, pageInfo, error) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, pageInfo{}, fmt.Errorf("解析 HTML 失败: %w", err)
	}

	var items []MovieSummary
	seen := map[string]bool{}
	for _, art := range findAllClass(root, "article", "u-movie") {
		item, ok := parseCard(art, c.cfg.BaseURL)
		if !ok || item.URL == "" || seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		items = append(items, item)
	}
	return items, parsePagination(root), nil
}

// parseCard 解析一张卡片（列表页与相关推荐共用同一结构）。
// baseURL 用于把相对链接补成绝对地址。
func parseCard(art *html.Node, baseURL string) (MovieSummary, bool) {
	link := findChild(art, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && attr(n, "href") != ""
	})
	if link == nil {
		return MovieSummary{}, false
	}
	href := attr(link, "href")
	if !strings.Contains(href, "/p/") {
		return MovieSummary{}, false
	}

	title := ""
	if h2 := findChild(art, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "h2"
	}); h2 != nil {
		title = nodeText(h2)
	}
	if title == "" {
		title = strings.TrimSpace(attr(link, "title"))
	}
	if title == "" {
		return MovieSummary{}, false
	}

	poster := ""
	for _, img := range findAll(art, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "img"
	}) {
		if poster = imageURL(img); poster != "" {
			break
		}
	}

	m := MovieSummary{
		Title:  title,
		URL:    absURL(baseURL, href),
		Poster: poster,
	}
	if mm := reID.FindStringSubmatch(href); mm != nil {
		m.ID, _ = strconv.ParseInt(mm[1], 10, 64)
	}

	// 评分与状态：外层都是 div（如 div.pingfen > span、div.zhuangtai > span）
	if n := findClass(art, "div", "pingfen"); n != nil {
		m.Score = firstFloat(nodeText(n))
	}
	if n := findClass(art, "div", "zhuangtai"); n != nil {
		m.Status = nodeText(n)
	}

	// 标签区在列表页是题材（动作/恐怖…），在首页是更新日期（2026-09-28更新）
	var tags []string
	if meta := findClass(art, "div", "meta"); meta != nil {
		for _, a := range findAll(meta, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a"
		}) {
			if t := nodeText(a); t != "" {
				tags = append(tags, t)
			}
		}
		if len(tags) == 0 {
			if t := nodeText(meta); t != "" {
				tags = []string{t}
			}
		}
	}
	for _, t := range tags {
		if d := reDate.FindString(t); d != "" {
			m.UpdatedAt = d
			continue
		}
		m.Tags = append(m.Tags, t)
	}
	if len(m.Tags) > 0 {
		m.Category = m.Tags[0]
	}
	return m, true
}

type pageInfo struct {
	Page       int
	TotalPages int
	HasNext    bool
	NextPage   int
}

// parsePagination 读取分页控件（形如 “共1/678 页” 与 “下一页” 链接）。
func parsePagination(root *html.Node) pageInfo {
	var out pageInfo
	block := findClass(root, "div", "pagination")
	if block == nil {
		return out
	}
	if m := rePageTot.FindStringSubmatch(nodeText(block)); m != nil {
		out.Page, _ = strconv.Atoi(m[1])
		out.TotalPages, _ = strconv.Atoi(m[2])
	}
	if active := findClass(block, "li", "active"); active != nil {
		if s := findChild(active, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "span"
		}); s != nil {
			if v, err := strconv.Atoi(strings.TrimSpace(nodeText(s))); err == nil {
				out.Page = v
			}
		}
	}
	for _, a := range findAll(block, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a"
	}) {
		if nodeText(a) == "下一页" {
			if h := attr(a, "href"); h != "" && !strings.HasPrefix(h, "#") {
				out.HasNext = true
			}
		}
	}
	if out.TotalPages > 0 && out.Page > 0 {
		out.HasNext = out.Page < out.TotalPages
		out.NextPage = out.Page + 1
	}
	return out
}

// parseDetail 解析详情页：封面、名称、简介、演员、线路、相关推荐。
func (c *Client) parseDetail(body, url string) MovieDetail {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return MovieDetail{URL: url}
	}

	d := MovieDetail{URL: url}
	if m := reID.FindStringSubmatch(url); m != nil {
		d.ID, _ = strconv.ParseInt(m[1], 10, 64)
	}

	if h1 := findClass(root, "h1", "article-title"); h1 != nil {
		d.Title = strings.TrimSuffix(nodeText(h1), "在线观看")
	}
	if d.Title == "" {
		if t := findChild(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "title"
		}); t != nil {
			d.Title = strings.TrimSpace(strings.Split(nodeText(t), "|")[0])
		}
	}

	if img := findSelector(root, []selTag{{"div", "video_img"}, {"img", ""}}); img != nil {
		d.Poster = imageURL(img)
	}
	if d.Poster == "" {
		for _, img := range findAll(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		}) {
			if u := imageURL(img); u != "" && strings.Contains(u, "/upload/") {
				d.Poster = u
				break
			}
		}
	}

	// 面包屑里的第一个链接是频道名（影视剧/电影…），跳过它取真正的类型
	if meta := findClass(root, "ul", "article-meta"); meta != nil {
		d.UpdatedAt = reDate.FindString(nodeText(meta))
		for _, a := range findAll(meta, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a"
		}) {
			if t := nodeText(a); t != "" && !invalidCategory[t] {
				d.Category = t
				break
			}
		}
	}

	info := parseVideoInfo(root)
	d.Actors = splitList(info["主演"], ",")
	d.Genres = splitList(info["类型"], "/")
	d.Region = info["国家/地区"]
	d.Language = info["语言"]
	d.FirstAired = firstNonEmpty(info["首播"], info["上映"])
	d.Score = firstFloat(info["评分"])
	// 年份只取首播/上映日期，绝不使用“最后更新”日期
	d.Year = firstYear(d.FirstAired)
	if d.Category == "" {
		d.Category = info["类型"]
	}
	d.Summary = parseSummary(root)

	d.Episodes, d.PlaySources = parsePlayLinks(root, c)
	if d.Episodes == nil {
		d.Episodes = []Episode{}
	}
	d.EpisodeNum = len(d.Episodes)
	d.Related = parseRelated(root, c.cfg.BaseURL)
	d.MagnetQueries = buildMagnetQueries(d)
	return d
}

// parseVideoInfo 解析 <div class="video_info"> 中由 <br> 分隔的 “键:值” 列表。
func parseVideoInfo(root *html.Node) map[string]string {
	out := map[string]string{}
	box := findClass(root, "div", "video_info")
	if box == nil {
		return out
	}
	for _, seg := range segmentsByBR(box) {
		m := reInfoKV.FindStringSubmatch(seg)
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m[1])
		val := strings.TrimSpace(m[2])
		val = strings.TrimSuffix(strings.TrimPrefix(val, "【"), "】")
		if key != "" && val != "" {
			if _, dup := out[key]; !dup {
				out[key] = val
			}
		}
	}
	return out
}

func parseSummary(root *html.Node) string {
	if n := findClass(root, "p", "jianjie"); n != nil {
		return strings.TrimPrefix(nodeText(n), "剧情简介:")
	}
	if meta := findChild(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "meta" && attr(n, "name") == "description"
	}); meta != nil {
		txt := attr(meta, "content")
		if i := strings.Index(txt, "剧情:"); i >= 0 {
			return strings.TrimSpace(txt[i+len("剧情:"):])
		}
		return strings.TrimSpace(txt)
	}
	return ""
}

// parsePlayLinks 解析各条播放线路与剧集链接。
func parsePlayLinks(root *html.Node, c *Client) ([]Episode, []PlaySource) {
	var episodes []Episode
	var sources []PlaySource
	for bi, block := range findAllClass(root, "div", "video_list_li") {
		label := ""
		for prev := block.PrevSibling; prev != nil; prev = prev.PrevSibling {
			if prev.Type == html.ElementNode && (prev.Data == "h4" || prev.Data == "h3") {
				label = strings.TrimRight(nodeText(prev), ":：")
				break
			}
		}
		if label == "" {
			label = fmt.Sprintf("线路%d", bi+1)
		}
		count := 0
		for _, a := range findAll(block, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && strings.Contains(attr(n, "href"), "/v/")
		}) {
			count++
			episodes = append(episodes, Episode{
				Index:  len(episodes) + 1,
				Name:   firstNonEmpty(nodeText(a), fmt.Sprintf("第%d集", count)),
				URL:    c.absURL(attr(a, "href")),
				Source: label,
			})
		}
		if count > 0 {
			sources = append(sources, PlaySource{Name: label, Count: count})
		}
	}
	return episodes, sources
}

func parseRelated(root *html.Node, baseURL string) []MovieSummary {
	block := findClass(root, "div", "relate")
	if block == nil {
		return nil
	}
	var out []MovieSummary
	seen := map[string]bool{}
	for _, art := range findAllClass(block, "article", "u-movie") {
		item, ok := parseCard(art, baseURL)
		if !ok || item.URL == "" || seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		out = append(out, item)
	}
	return out
}

// ============ 磁力检索关键词 ============

// buildMagnetQueries 生成用于磁力搜索的精确关键词。
// 源站不提供磁力链接，这里只产出可检索的关键词，不伪造 magnet 链接。
func buildMagnetQueries(d MovieDetail) []string {
	year := ""
	if d.Year > 0 {
		year = strconv.Itoa(d.Year)
	}
	templates := []string{
		"{title} {year} 1080p 磁力",
		"{title} 中字 磁力链接",
		"{title} {year} hd 种子",
	}
	var out []string
	seen := map[string]bool{}
	for _, title := range titleVariants(d.Title) {
		for _, tpl := range templates {
			q := strings.Join(strings.Fields(
				strings.NewReplacer("{title}", title, "{year}", year).Replace(tpl)), " ")
			if q != "" && !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
	}
	return out
}

// titleVariants 生成标题变体（原名、去季号、去副标题、去后缀）。
func titleVariants(title string) []string {
	var out []string
	add := func(s string) {
		s = strings.Join(strings.Fields(s), " ")
		if s == "" {
			return
		}
		for _, e := range out {
			if e == s {
				return
			}
		}
		out = append(out, s)
	}
	add(title)
	add(reSeason.ReplaceAllString(title, ""))
	add(reSeasonN.ReplaceAllString(title, ""))
	if i := strings.IndexAny(title, ":："); i > 0 {
		add(title[:i])
	}
	add(reTail.ReplaceAllString(title, ""))
	return out
}

// ============ HTML 工具 ============

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

func findAll(root *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if pred(n) {
			out = append(out, n)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(root)
	return out
}

func findAllClass(root *html.Node, tag, class string) []*html.Node {
	return findAll(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag && hasClass(n, class)
	})
}

func findChild(root *html.Node, pred func(*html.Node) bool) *html.Node {
	for ch := root.FirstChild; ch != nil; ch = ch.NextSibling {
		if pred(ch) {
			return ch
		}
	}
	return nil
}

func findClass(root *html.Node, tag, class string) *html.Node {
	for _, n := range findAllClass(root, tag, class) {
		return n
	}
	return nil
}

type selTag struct{ tag, class string }

// findSelector 按 “祖先(带 class) > 后代(标签)” 逐级下钻。
func findSelector(root *html.Node, path []selTag) *html.Node {
	cur := root
	for _, step := range path {
		var next *html.Node
		for _, n := range findAll(cur, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == step.tag &&
				(step.class == "" || hasClass(n, step.class))
		}) {
			next = n
			break
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return cleanText(b.String())
}

// segmentsByBR 按 <br> 切分文本片段（用于 video_info 的键值行）。
func segmentsByBR(n *html.Node) []string {
	var segs []string
	var cur strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "br" {
			segs = append(segs, cleanText(cur.String()))
			cur.Reset()
			return
		}
		if x.Type == html.TextNode {
			cur.WriteString(x.Data)
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	if s := cleanText(cur.String()); s != "" {
		segs = append(segs, s)
	}
	var out []string
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanText(s string) string {
	s = strings.NewReplacer("\u00a0", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// imageURL 优先取懒加载属性，忽略站点占位用的空 src / gif。
func imageURL(n *html.Node) string {
	for _, key := range []string{"data-original", "data-src", "data-lazy-src", "src"} {
		v := strings.TrimSpace(attr(n, key))
		if v != "" && !strings.HasSuffix(strings.ToLower(v), ".gif") {
			return v
		}
	}
	return ""
}

func firstFloat(s string) float64 {
	if m := reScore.FindStringSubmatch(s); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			return f
		}
	}
	return 0
}

func firstYear(s string) int {
	if y := reYear.FindString(s); y != "" {
		if v, err := strconv.Atoi(y); err == nil && v >= 1900 && v <= 2100 {
			return v
		}
	}
	return 0
}

func splitList(s, sep string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		if r == ',' || r == '，' || r == '、' {
			return true
		}
		return sep != "" && string(r) == sep
	})
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
