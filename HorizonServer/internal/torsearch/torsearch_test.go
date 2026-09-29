package torsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// newMock 起一个假的上游：/q.php 按关键词返回种子，/translate 返回翻译。
func newMock(t *testing.T, tpbHandler, transHandler http.HandlerFunc) (*Searcher, func()) {
	t.Helper()
	var tpbSrv, transSrv *httptest.Server
	if tpbHandler != nil {
		tpbSrv = httptest.NewServer(tpbHandler)
	}
	if transHandler != nil {
		transSrv = httptest.NewServer(transHandler)
	}
	// DisableCSV：单测不该依赖外网。
	// csv 源默认是开着的，不关掉的话每个用例都会真的去打 torrents-csv.com，
	// 结果随上游变化、用例随机失败。需要测 csv 的用例用 newMockCSV。
	cfg := Config{HTTPTimeout: 5 * time.Second, DisableCSV: true}
	if tpbSrv != nil {
		cfg.TPBBase = tpbSrv.URL
	}
	if transSrv != nil {
		cfg.TransmartURL = transSrv.URL
	}
	s := New(cfg)
	return s, func() {
		if tpbSrv != nil {
			tpbSrv.Close()
		}
		if transSrv != nil {
			transSrv.Close()
		}
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestCleanTranslated(t *testing.T) {
	cases := map[string]string{
		"The Wandering Earth (电影名)":            "The Wandering Earth",
		"Nezha (a mythological person's name)": "Nezha",
		"war Wolf":                             "war Wolf",
		"The Three-Body Problem":               "The Three-Body Problem",
		"Infernal Affairs [无间道]":               "Infernal Affairs",
		"  The   Wandering   Earth  ":          "The Wandering Earth",
	}
	for in, want := range cases {
		if got := cleanTranslated(in); got != want {
			t.Errorf("cleanTranslated(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestHasCJK(t *testing.T) {
	if !HasCJK("流浪地球") {
		t.Error("中文应判定为需要翻译")
	}
	if !HasCJK("Ne Zha 哪吒") {
		t.Error("中英混排应判定为需要翻译")
	}
	if HasCJK("The Wandering Earth") {
		t.Error("纯英文不应判定为需要翻译")
	}
}

func TestTokensAndRelevant(t *testing.T) {
	toks := tokens("The Wandering Earth 1080p BluRay x264")
	if len(toks) == 0 {
		t.Fatal("应提取到关键词")
	}
	for _, w := range []string{"1080p", "bluray", "x264"} {
		for _, g := range toks {
			if g == w {
				t.Errorf("技术词 %q 不应作为相关性判据", w)
			}
		}
	}
	// 无关种子必须被排除，这是本次修复的核心：中文关键词曾导致满屏无关结果。
	if relevant("Spider-Man.Brand.New.Day.2026.1080p", toks) {
		t.Error("蜘蛛侠不应被判定为与《流浪地球》相关")
	}
	if !relevant("The.Wandering.Earth.2019.DUBBED.1080p", toks) {
		t.Error("《流浪地球》应被判定为相关")
	}
	// 空格差异不应影响匹配
	if !relevant("Nezha.2019.1080p", tokens("Ne Zha")) {
		t.Error("Ne Zha 应能匹配 Nezha")
	}
}

// 核心回归：中文关键词 -> 翻译 -> 只返回相关结果，无关结果被过滤。
func TestSearchTranslatesAndFilters(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		cat := r.URL.Query().Get("cat")
		if strings.Contains(q, "Wandering") && cat == "207" {
			writeJSON(w, []Item{
				{Name: "The.Wandering.Earth.2019.1080p", InfoHash: "aaaa1111", Seeders: "46"},
				{Name: "The.Wandering.Earth.II.2023.1080p", InfoHash: "bbbb2222", Seeders: "44"},
				{Name: "Spider-Man.Brand.New.Day.2026.1080p", InfoHash: "cccc3333", Seeders: "6351"},
			})
			return
		}
		writeJSON(w, []Item{})
	}
	trans := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"header":           map[string]string{"ret_code": "succ"},
			"auto_translation": []string{"The Wandering Earth (电影名)"},
		})
	}
	s, cleanup := newMock(t, tpb, trans)
	defer cleanup()

	res, err := s.Search(context.Background(), "流浪地球", "0")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if !res.TranslatedOK || res.Translated != "The Wandering Earth" {
		t.Errorf("应翻译为 The Wandering Earth，实际 %q (ok=%v)", res.Translated, res.TranslatedOK)
	}
	if res.SearchTerm != "The Wandering Earth" {
		t.Errorf("检索词应为翻译结果，实际 %q", res.SearchTerm)
	}
	if len(res.Results) != 2 {
		t.Fatalf("应只保留 2 条相关结果，实际 %d 条", len(res.Results))
	}
	for _, it := range res.Results {
		if strings.Contains(it.Name, "Spider-Man") {
			t.Error("无关结果《蜘蛛侠》必须被过滤")
		}
	}
	if res.FilteredOut != 1 {
		t.Errorf("应过滤 1 条，实际 %d", res.FilteredOut)
	}
	// 按做种数降序
	if res.Results[0].Seeders != "46" {
		t.Errorf("应按做种数降序，第一条 seeders=%s", res.Results[0].Seeders)
	}
}

// 「全部」必须同时覆盖「逐个影视分类」和「cat=0 全站」。
//
// 两者不是包含关系，实测各有独占：
//
//	Infernal Affairs：分类并集 40 条里 12 条 cat=0 没有；cat=0 的 79 条里 51 条并集没有
//	Dune Part Two  ：分类并集 0 条，cat=0 有 97 条
//
// 所以只打分类会漏掉一大批，只打 cat=0 也会漏。（cat=0 单独用不可靠的说法
// 来自更早的一次实测，那次是上游抖动，不是参数语义问题。）
func TestCatAllCoversBothCategoriesAndGlobal(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	tpb := func(w http.ResponseWriter, r *http.Request) {
		cat := r.URL.Query().Get("cat")
		mu.Lock()
		seen = append(seen, cat)
		mu.Unlock()
		switch cat {
		case "208": // 只有剧集分类有这条
			writeJSON(w, []Item{{Name: "Infernal.Affairs.S01.1080p", InfoHash: "dddd4444", Seeders: "9"}})
		case "0": // 只有 cat=0 有这条
			writeJSON(w, []Item{{Name: "Infernal.Affairs.2002.2160p", InfoHash: "eeee5555", Seeders: "8"}})
		default:
			writeJSON(w, []Item{})
		}
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	res, err := s.Search(context.Background(), "Infernal Affairs", "0")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()

	var hasCat, hasAll bool
	for _, c := range got {
		if c == "0" {
			hasAll = true
		} else {
			hasCat = true
		}
	}
	if !hasCat {
		t.Errorf("应逐个分类检索，实际请求了 %v", got)
	}
	if !hasAll {
		t.Errorf("应额外检索 cat=0（分类并集覆盖不到它独有的种子），实际请求了 %v", got)
	}
	// 两条都要在结果里：证明是合并而不是二选一。
	if len(res.Results) != 2 {
		t.Fatalf("应合并两边结果共 2 条，实际 %d 条: %+v", len(res.Results), res.Results)
	}
	hashSet := map[string]bool{}
	for _, it := range res.Results {
		hashSet[it.InfoHash] = true
	}
	for _, want := range []string{"dddd4444", "eeee5555"} {
		if !hashSet[want] {
			t.Errorf("结果应包含 %s，实际 %+v", want, hashSet)
		}
	}
}

// 用户显式指定分类时不该再去打 cat=0：那会混进用户没要的分类。
func TestExplicitCatDoesNotQueryGlobal(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	tpb := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query().Get("cat"))
		mu.Unlock()
		writeJSON(w, []Item{{Name: "Infernal.Affairs.2002.1080p", InfoHash: "ffff6666", Seeders: "7"}})
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	if _, err := s.Search(context.Background(), "Infernal Affairs", "207"); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	for _, c := range got {
		if c != "207" {
			t.Errorf("显式分类下只应请求 cat=207，实际 %v", got)
		}
	}
}

func TestSearchDedupesAcrossCats(t *testing.T) {
	dup := Item{Name: "Infernal.Affairs.2002.1080p", InfoHash: "EEEE5555", Seeders: "7"}
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{dup}) // 每个分类都返回同一条
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	res, err := s.Search(context.Background(), "Infernal Affairs", "0")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Results) != 1 {
		t.Errorf("跨分类重复的种子应去重，实际 %d 条", len(res.Results))
	}
}

// 翻译失败时必须降级为原词检索，并给出提示，而不是报错或返回垃圾。
func TestTranslateFailureFallsBack(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{{Name: "Nezha.2019.1080p", InfoHash: "ffff6666", Seeders: "3"}})
	}
	trans := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	s, cleanup := newMock(t, tpb, trans)
	defer cleanup()

	res, err := s.Search(context.Background(), "哪吒", "207")
	if err != nil {
		t.Fatalf("翻译失败不应导致搜索报错: %v", err)
	}
	if res.TranslatedOK {
		t.Error("翻译失败时 translated_ok 应为 false")
	}
	if res.Hint == "" {
		t.Error("翻译失败时应给出提示")
	}
}

func TestTranslateDisabled(t *testing.T) {
	var transCalled bool
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{{Name: "The.Wandering.Earth.2019.1080p", InfoHash: "gggg7777", Seeders: "5"}})
	}
	trans := func(w http.ResponseWriter, r *http.Request) {
		transCalled = true
		writeJSON(w, map[string]interface{}{"header": map[string]string{"ret_code": "succ"}, "auto_translation": []string{"X"}})
	}
	s, cleanup := newMock(t, tpb, trans)
	s.cfg.DisableTranslate = true
	defer cleanup()

	res, err := s.Search(context.Background(), "流浪地球", "207")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if transCalled {
		t.Error("关闭翻译后不应调用翻译接口")
	}
	if res.TranslatedOK {
		t.Error("关闭翻译后 translated_ok 应为 false")
	}
}

func TestTranslateCaches(t *testing.T) {
	var calls int
	trans := func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, map[string]interface{}{"header": map[string]string{"ret_code": "succ"}, "auto_translation": []string{"Infernal Affairs"}})
	}
	tpb := func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []Item{}) }
	s, cleanup := newMock(t, tpb, trans)
	defer cleanup()

	for i := 0; i < 3; i++ {
		if _, err := s.Translate(context.Background(), "无间道"); err != nil {
			t.Fatalf("翻译失败: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("翻译结果应被缓存，实际调用了 %d 次", calls)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	s, cleanup := newMock(t, func(w http.ResponseWriter, r *http.Request) {}, nil)
	defer cleanup()
	if _, err := s.Search(context.Background(), "   ", "0"); err != ErrEmptyQuery {
		t.Errorf("空关键词应返回 ErrEmptyQuery，实际 %v", err)
	}
}

func TestSearchNoResultsHint(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{{Name: "Spider-Man.Brand.New.Day.2026.1080p", InfoHash: "hhhh8888", Seeders: "6351"}})
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	res, err := s.Search(context.Background(), "Infernal Affairs", "207")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("无关结果应全部过滤，实际保留 %d 条", len(res.Results))
	}
	if res.Hint == "" {
		t.Error("无相关结果时应给出提示，避免用户误以为搜到的是目标影片")
	}
}

// 相关性要求命中全部词元，不能只命中一个。
//
// 真实回归（2026-09-29）：为了让 torrents-csv 多吐结果，对「The Wandering Earth」
// 追加了变形词查询。只要「任一命中」就算相关的话，111 条结果里会混进 57 条无关的——
// "Earth 3D Suite"、"Orb On the Movements of the Earth"、"Alien Earth"…
// 根因是 "Earth" 这类泛词本身在目标片名里，事后无法靠过滤区分。
func TestRelevantRequiresAllTokens(t *testing.T) {
	toks := tokens("The Wandering Earth") // "the" 是停用词，剩 wandering/earth
	if len(toks) != 2 {
		t.Fatalf("预期 2 个词元，实际 %v", toks)
	}
	if !relevant("The.Wandering.Earth.2019.1080p", toks) {
		t.Error("两个词元都命中的结果必须保留")
	}
	if !relevant("The Wandering Earth II 2023 1080p", toks) {
		t.Error("空格分隔的写法也要命中")
	}
	// 这两条都是真实出现过的噪声：只命中 "earth"。
	if relevant("Earth.3D.Suite.v326.Pre-Activated.2025", toks) {
		t.Error("只命中 earth 的软件资源必须被过滤")
	}
	if relevant("Orb.On.the.Movements.of.the.Earth.1080p.HEVC", toks) {
		t.Error("只命中 earth 的无关影片必须被过滤")
	}
	if relevant("Spider-Man.Brand.New.Day.2026.1080p", toks) {
		t.Error("完全无关的结果必须被过滤")
	}

	// 「Dune Part Two」不会再带出 2021 年的《Dune》：这是刻意的取舍。
	dune := tokens("Dune Part Two") // part 是停用词
	if !relevant("Dune.Part.Two.2024.1080p", dune) {
		t.Error("完整片名应命中")
	}
	if relevant("Dune.2021.2160p", dune) {
		t.Error("片名不完整的近义结果会被过滤（刻意取舍，保证搜索精确）")
	}
}

// csvVariants 只产出有意义的长词，不能把 "The"/"of" 这类词丢去检索。
func TestCSVVariantsSkipsStopWords(t *testing.T) {
	got := csvVariants("The Wandering Earth")
	for _, v := range got {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "the", "of", "and", "a", "an", "in":
			t.Errorf("不应把停用词 %q 当成检索词: %v", v, got)
		}
		if len(v) < 4 {
			t.Errorf("过短的词会拉回大量噪声: %q in %v", v, got)
		}
	}
	if len(got) == 0 || got[0] != "The Wandering Earth" {
		t.Errorf("第一个应当是原检索词，实际 %v", got)
	}
	if len(got) > 4 {
		t.Errorf("变形词最多 4 个（含原词），实际 %d: %v", len(got), got)
	}
	// 单词关键词没有变形可用
	if got := csvVariants("Interstellar"); len(got) != 1 {
		t.Errorf("单词关键词不应展开，实际 %v", got)
	}
}
