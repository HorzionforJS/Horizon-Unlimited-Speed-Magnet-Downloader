package dytt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取样本失败 %s: %v", name, err)
	}
	return string(b)
}

func TestParseListPage(t *testing.T) {
	c := New(Config{BaseURL: "https://dytt.org.cn"})
	items, pi, err := c.parsePage(loadFixture(t, "list_page.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(items) < 30 {
		t.Fatalf("卡片数量过少: %d", len(items))
	}
	first := items[0]
	if first.Title != "外星人揭秘日" {
		t.Errorf("首条标题 = %q，期望 外星人揭秘日", first.Title)
	}
	if first.ID != 266587 {
		t.Errorf("首条 ID = %d，期望 266587", first.ID)
	}
	if !strings.HasPrefix(first.Poster, "https://") {
		t.Errorf("封面地址异常: %q", first.Poster)
	}
	if first.Score != 1.0 {
		t.Errorf("评分 = %v，期望 1.0", first.Score)
	}
	if first.Status != "HD" {
		t.Errorf("状态 = %q，期望 HD", first.Status)
	}
	if first.Category != "动作" {
		t.Errorf("类型 = %q，期望 动作", first.Category)
	}

	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.URL] {
			t.Fatalf("出现重复链接: %s", it.URL)
		}
		seen[it.URL] = true
		if it.Title == "" || it.URL == "" {
			t.Fatalf("存在空标题或空链接的卡片: %+v", it)
		}
	}

	if pi.Page != 1 || pi.TotalPages != 678 {
		t.Errorf("分页 = %d/%d，期望 1/678", pi.Page, pi.TotalPages)
	}
	if !pi.HasNext || pi.NextPage != 2 {
		t.Errorf("下一页信息异常: hasNext=%v next=%d", pi.HasNext, pi.NextPage)
	}
}

func TestIndexPageUpdateDate(t *testing.T) {
	items, _, err := New(Config{BaseURL: "https://dytt.org.cn"}).parsePage(loadFixture(t, "index_page.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("首页未解析到任何卡片")
	}
	first := items[0]
	if first.Title != "云雀叫天录" {
		t.Errorf("首条标题 = %q", first.Title)
	}
	if first.UpdatedAt != "2026-09-28" {
		t.Errorf("更新时间 = %q，期望 2026-09-28", first.UpdatedAt)
	}
	if first.Status != "更新至第5集" {
		t.Errorf("状态 = %q", first.Status)
	}
	// 更新日期不能出现在题材标签里
	for _, tag := range first.Tags {
		if strings.Contains(tag, "2026-") {
			t.Errorf("题材标签混入了日期: %q", tag)
		}
	}
}

func TestParseDetailPage(t *testing.T) {
	c := New(Config{BaseURL: "https://dytt.org.cn"})
	d := c.parseDetail(loadFixture(t, "detail_page.html"), "https://dytt.org.cn/p/266003/")

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"标题", d.Title, "云雀叫天录"},
		{"类型", d.Category, "国产"},
		{"地区", d.Region, "中国大陆"},
		{"语言", d.Language, "汉语普通话"},
		{"首播", d.FirstAired, "2026-09-28"},
	}
	for _, ck := range checks {
		if ck.got != ck.want {
			t.Errorf("%s = %q，期望 %q", ck.name, ck.got, ck.want)
		}
	}
	if d.ID != 266003 {
		t.Errorf("ID = %d，期望 266003", d.ID)
	}
	if !strings.HasPrefix(d.Poster, "https://") {
		t.Errorf("封面 = %q", d.Poster)
	}
	if d.Score != 2.0 {
		t.Errorf("评分 = %v，期望 2.0", d.Score)
	}
	if d.Year != 2026 {
		t.Errorf("年份 = %d，期望 2026", d.Year)
	}
	if !strings.Contains(d.Summary, "孟金福") {
		t.Errorf("简介未包含关键内容: %q", d.Summary)
	}
	if len(d.Summary) < 50 {
		t.Errorf("简介过短: %d 字", len(d.Summary))
	}

	if !contains(d.Actors, "张一山") || !contains(d.Actors, "六小龄童") {
		t.Errorf("演员解析异常: %v", d.Actors)
	}
	if len(d.Genres) == 0 || d.Genres[0] != "国产" {
		t.Errorf("类型解析异常: %v", d.Genres)
	}

	if len(d.Episodes) < 40 {
		t.Errorf("剧集数量过少: %d", len(d.Episodes))
	} else {
		ep := d.Episodes[0]
		if ep.Name != "第1集" {
			t.Errorf("首集名称 = %q", ep.Name)
		}
		if !strings.HasPrefix(ep.URL, "https://dytt.org.cn/v/") {
			t.Errorf("首集链接 = %q", ep.URL)
		}
		if ep.Source != "线路二在线播放" {
			t.Errorf("首集线路 = %q", ep.Source)
		}
	}
	if d.EpisodeNum != len(d.Episodes) {
		t.Errorf("集数统计不一致: %d vs %d", d.EpisodeNum, len(d.Episodes))
	}

	if len(d.PlaySources) < 2 {
		t.Errorf("播放线路过少: %v", d.PlaySources)
	}
	for _, s := range d.PlaySources {
		if s.Count <= 0 {
			t.Errorf("线路 %q 集数为 0（应过滤空线路）", s.Name)
		}
		if strings.HasSuffix(s.Name, ":") || strings.HasSuffix(s.Name, "：") {
			t.Errorf("线路名称未清理: %q", s.Name)
		}
	}

	if len(d.Related) == 0 {
		t.Error("相关推荐为空")
	}
	for _, r := range d.Related {
		if !strings.HasPrefix(r.URL, "https://dytt.org.cn/p/") {
			t.Errorf("相关推荐链接异常: %q", r.URL)
		}
	}
}

func TestMagnetQueries(t *testing.T) {
	d := MovieDetail{Title: "云雀叫天录", Year: 2026}
	q := buildMagnetQueries(d)
	if len(q) < 3 {
		t.Fatalf("关键词过少: %v", q)
	}
	found := false
	for _, s := range q {
		if s == "云雀叫天录 2026 1080p 磁力" {
			found = true
		}
		// 站点不提供磁力链接，这里只能是关键词，不能是磁力地址
		if strings.HasPrefix(s, "magnet:") {
			t.Errorf("不应生成磁力链接: %q", s)
		}
	}
	if !found {
		t.Errorf("缺少预期关键词，实际: %v", q)
	}

	// 无年份时不应留下多余空格
	d2 := MovieDetail{Title: "某片"}
	for _, s := range buildMagnetQueries(d2) {
		if strings.Contains(s, "  ") {
			t.Errorf("关键词存在多余空格: %q", s)
		}
	}
}

func TestTitleVariants(t *testing.T) {
	v := titleVariants("探长埃利斯 第二季")
	if !contains(v, "探长埃利斯 第二季") {
		t.Errorf("缺少原标题: %v", v)
	}
	if !contains(v, "探长埃利斯") {
		t.Errorf("未去除季号: %v", v)
	}
	v2 := titleVariants("某某：重启")
	if !contains(v2, "某某") {
		t.Errorf("未去除副标题: %v", v2)
	}
}

func TestNormalizeCategory(t *testing.T) {
	cases := map[string]string{
		"": "3", "movie": "3", "电影": "3", "tv": "2", "剧集": "2",
		"short": "21", "短剧": "21", "3": "3", "21": "21",
	}
	for in, want := range cases {
		got, err := NormalizeCategory(in)
		if err != nil || got != want {
			t.Errorf("NormalizeCategory(%q) = %q,%v，期望 %q", in, got, err, want)
		}
	}
	if _, err := NormalizeCategory("music"); err == nil {
		t.Error("非法分类应当报错")
	}
}

func TestResolveDetailURL(t *testing.T) {
	c := New(Config{BaseURL: "https://dytt.org.cn"})
	for _, in := range []string{
		"266003", "/p/266003/", "https://dytt.org.cn/p/266003/",
		"https://home.dytiantang.com.cn/p/266003/", "https://dytt.org.cn/p/266003",
	} {
		got, ok := c.resolveDetailURL(in)
		if !ok {
			t.Errorf("%q 应当可解析", in)
			continue
		}
		if got != "https://dytt.org.cn/p/266003/" {
			t.Errorf("%q -> %q，期望标准详情地址", in, got)
		}
	}
	for _, bad := range []string{"", "不是链接", "https://dytt.org.cn/t/3/"} {
		if _, ok := c.resolveDetailURL(bad); ok {
			t.Errorf("%q 不应被解析为详情地址", bad)
		}
	}
}

func TestImageHostAllowlist(t *testing.T) {
	allow := []string{"https://img.picbf.com/a.jpg", "https://img.lzipic.com/x/y.webp", "http://pic.youkupic.com/a.png"}
	for _, u := range allow {
		if !imageHostAllowed(hostOf(u)) {
			t.Errorf("%s 应当被放行", u)
		}
	}
	deny := []string{"https://evil.example.com/a.jpg", "https://img.picbf.com.evil.com/a.jpg", "http://127.0.0.1:8080/a"}
	for _, u := range deny {
		if imageHostAllowed(hostOf(u)) {
			t.Errorf("%s 不应当被放行（SSRF 防护）", u)
		}
	}
}

func TestDecodeHTMLGB18030(t *testing.T) {
	// "中文" 的 GB18030 编码
	gb := []byte{0xD6, 0xD0, 0xCE, 0xC4}
	got := decodeHTML(gb, "text/html; charset=gb2312")
	if got != "中文" {
		t.Errorf("GB18030 解码结果 = %q，期望 中文", got)
	}
	// meta 声明 gbk 时，正文应为 GBK 字节
	head := append([]byte("<meta charset=\"gbk\">"), gb...)
	got = decodeHTML(head, "text/html")
	if !strings.Contains(got, "中文") {
		t.Errorf("按 meta 声明解码失败: %q", got)
	}
	utf := decodeHTML([]byte("中文标题"), "text/html; charset=utf-8")
	if utf != "中文标题" {
		t.Errorf("UTF-8 解码结果 = %q", utf)
	}
}

func TestAbsURL(t *testing.T) {
	c := New(Config{BaseURL: "https://dytt.org.cn"})
	cases := map[string]string{
		"/p/1/":             "https://dytt.org.cn/p/1/",
		"//img.x.com/a.jpg": "https://img.x.com/a.jpg",
		"https://a.com/b":   "https://a.com/b",
		"":                  "",
	}
	for in, want := range cases {
		if got := c.absURL(in); got != want {
			t.Errorf("absURL(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestServiceCachesAndParses(t *testing.T) {
	calls := 0
	client := New(Config{
		BaseURL: "https://dytt.org.cn",
		HTTPClient: mockHTTP(t, &calls, map[string]string{
			"/t/3/": loadFixture(t, "list_page.html"),
		}),
		MinInterval: 1, // 1ns，测试不需要限速
	})
	svc := NewService(client, 60*1e9)

	p1, err := svc.ListPage(context.Background(), "movie", 1)
	if err != nil {
		t.Fatalf("ListPage 失败: %v", err)
	}
	if p1.Count == 0 || p1.FromCache {
		t.Errorf("首次请求异常: count=%d fromCache=%v", p1.Count, p1.FromCache)
	}
	p2, err := svc.ListPage(context.Background(), "movie", 1)
	if err != nil {
		t.Fatalf("ListPage 第二次失败: %v", err)
	}
	if !p2.FromCache {
		t.Error("第二次请求应当命中缓存")
	}
	if calls != 1 {
		t.Errorf("上游请求次数 = %d，期望 1（第二次走缓存）", calls)
	}
}

func TestServiceSearchAndDetailValidation(t *testing.T) {
	client := New(Config{BaseURL: "https://dytt.org.cn"})
	svc := NewService(client, 0)
	if _, err := svc.Search(context.Background(), "   "); err == nil {
		t.Error("空关键词应当报错")
	}
	if _, err := svc.Detail(context.Background(), "bad-target"); err == nil {
		t.Error("非法目标应当报错")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
