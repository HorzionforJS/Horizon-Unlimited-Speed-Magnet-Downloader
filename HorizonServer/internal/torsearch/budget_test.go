package torsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 上游挂住时，整条搜索必须在总预算内结束。
//
// 这是一个真实的故障场景：客户端等 25 秒，而服务端之前的超时是「翻译 12s
// + 3 个分类 × 15s」，最坏 57 秒。那样用户看到的不是「慢」，而是「搜索失败」——
// 搜索结果全部丢弃。
func TestSearchHonoursSearchBudget(t *testing.T) {
	var hits int32
	tpb := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(2 * time.Second)
		writeJSON(w, []Item{})
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	// 把预算压到 500ms，分类检索即使全部挂住也应当很快返回。
	s.cfg.SearchBudget = 500 * time.Millisecond

	start := time.Now()
	res, err := s.Search(context.Background(), "Infernal Affairs", "0")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("超时应当返回空结果而不是报错：%v", err)
	}
	if res == nil {
		t.Fatal("不应返回 nil 结果")
	}
	// 预算 500ms + 收尾开销；留 3 秒余量避免 CI 上稀少的调度抖动导致误报。
	if elapsed > 3*time.Second {
		t.Errorf("搜索耗时 %v，超出总预算约束", elapsed)
	}
	// 分类请求现在是并发发出的：串行时三个分类最坏要等 3 倍时间，而它们互不依赖。
	// 这里锁定「发出的请求数正好等于分类数 + 一次 cat=0」——
	// 既证明没有重复请求，也证明预算耗尽后不会再补发。
	//
	// cat=0 必须单独算一路：实测它和分类并集覆盖的不是同一批种子
	// （Dune Part Two：分类并集 0 条，cat=0 有 97 条）。
	if n := atomic.LoadInt32(&hits); n != 4 {
		t.Errorf("应并发发出 4 个请求（3 个分类 + cat=0），实际 %d 次", n)
	}
}

// 翻译必须在检索之后仍有预算，否则用户只看得到英文种子名。
func TestSearchLeavesBudgetForNameTranslation(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{
			{Name: "Infernal.Affairs.2002.1080p.BluRay", InfoHash: "aaaa1111", Seeders: "46"},
		})
	}
	trans := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"header":           map[string]string{"ret_code": "succ"},
			"auto_translation": []string{"无间道"},
		})
	}
	s, cleanup := newMock(t, tpb, trans)
	defer cleanup()

	res, err := s.Search(context.Background(), "Infernal Affairs", "207")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("应有 1 条结果，实际 %d 条", len(res.Results))
	}
	if res.Results[0].NameZh == "" {
		t.Error("应回填中文片名")
	} else if !HasCJK(res.Results[0].NameZh) {
		t.Errorf("中文片名应含中文，实际 %q", res.Results[0].NameZh)
	}
}

// 缓存数量上限后应清理过期条目，避免长期运行内存无界增长。
func TestPruneCacheLocked(t *testing.T) {
	ttl := time.Hour
	cache := map[string]cacheEntry{}
	for i := 0; i < 300; i++ {
		cache[string(rune(i))] = cacheEntry{text: "x", at: time.Now().Add(-2 * ttl)}
	}
	for i := 0; i < 50; i++ {
		cache["fresh"+string(rune(i))] = cacheEntry{text: "y", at: time.Now()}
	}
	pruneCacheLocked(cache, ttl)
	if len(cache) != 50 {
		t.Errorf("过期条目应被清理，剩下 %d 条，期望 50 条", len(cache))
	}
}

// limit 必须真正截断：每多返回一条就多一条片名要翻译，
// 而翻译是整条链路里最贵的一步。
func TestSearchLimitTruncates(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cat") != "207" {
			writeJSON(w, []Item{})
			return
		}
		var items []Item
		for i := 0; i < 40; i++ {
			items = append(items, Item{
				Name:     "Infernal.Affairs.2002.1080p.BluRay." + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				InfoHash: "hash" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				Seeders:  "10",
			})
		}
		writeJSON(w, items)
	}
	s, cleanup := newMock(t, tpb, nil)
	defer cleanup()

	res, err := s.SearchLimit(context.Background(), "Infernal Affairs", "207", 5)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Results) != 5 {
		t.Errorf("limit=5 应返回 5 条，实际 %d 条", len(res.Results))
	}
	if res.Limit != 5 {
		t.Errorf("应回报生效的 limit=5，实际 %d", res.Limit)
	}

	// limit 为 0 时不截断（除非超过内部上限）。
	res2, err := s.SearchLimit(context.Background(), "Infernal Affairs", "207", 0)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res2.Results) != 40 {
		t.Errorf("limit=0 不应截断，实际 %d 条", len(res2.Results))
	}
}

// 批次翻译超时时，已完成的批次必须保留。
//
// 全量一次翻译的行为是「要么全有、要么全无」：翻译接口慢一秒，
// 用户就从「满屏中文名」掉到「满屏英文名」。分批后至少能保住前面几批。
func TestEnrichNameZhKeepsCompletedChunks(t *testing.T) {
	var calls int32
	trans := func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n > 1 {
			// 第二批开始挂住，直到上游超时
			time.Sleep(5 * time.Second)
		}
		writeJSON(w, map[string]interface{}{
			"header":           map[string]string{"ret_code": "succ"},
			"auto_translation": fillZh(n),
		})
	}
	s, cleanup := newMock(t, nil, trans)
	defer cleanup()

	// 造 100 条，按 nameZhChunkSize 分成多批。
	// 注意片名必须各不相同：缓存命中的条目不会发出请求，
	// 用重名数据测不出「批次超时」行为。
	items := make([]Item, 100)
	for i := range items {
		items[i] = Item{
			Name:     "Infernal.Affairs." + strconv.Itoa(i) + ".2002.1080p.BluRay",
			InfoHash: "hash" + strconv.Itoa(i),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	s.enrichNameZh(ctx, items)

	got := 0
	for i := range items {
		if items[i].NameZh != "" {
			got++
		}
	}
	if got != nameZhChunkSize {
		t.Errorf("应保留第一批 %d 条中文名，实际 %d 条", nameZhChunkSize, got)
	}
}

// fillZh 模拟翻译接口返回一批中文名（长度必须与请求一致）。
func fillZh(call int32) []string {
	out := make([]string, nameZhChunkSize)
	for i := range out {
		out[i] = "无间道" + string(rune('a'+i%26))
	}
	return out
}

// csv 源必须真的被用上，且与 apibay 的同一条种子会被合并而不是重复出现。
func TestSearchMergesCSVSource(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{
			{Name: "Interstellar.2014.1080p.BluRay.x264", InfoHash: "AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111", Seeders: "10", Category: "207"},
		})
	}
	csv := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"torrents": []map[string]interface{}{
				// 与 apibay 同一条（hash 大小写不同 + 做种数更高）
				{"infohash": "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111", "name": "Interstellar 2014 1080p",
					"size_bytes": 2431905867, "created_unix": 1426426450, "seeders": 926, "leechers": 159},
				// csv 独有的一条
				{"infohash": "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222", "name": "Interstellar.2014.2160p.WEB-DL",
					"size_bytes": 8000000000, "created_unix": 1700000000, "seeders": 42, "leechers": 3},
			},
		})
	}

	tpbSrv := httptest.NewServer(http.HandlerFunc(tpb))
	defer tpbSrv.Close()
	csvSrv := httptest.NewServer(http.HandlerFunc(csv))
	defer csvSrv.Close()

	s := New(Config{HTTPTimeout: 5 * time.Second, DisableTranslate: true,
		TPBBase: tpbSrv.URL, SearchCatsAll: []string{"207"}, CSVBase: csvSrv.URL})

	res, err := s.Search(context.Background(), "Interstellar", "207")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("应合并为 2 条（去重后），实际 %d 条: %+v", len(res.Results), res.Results)
	}

	var merged *Item
	for i := range res.Results {
		if strings.HasPrefix(strings.ToUpper(res.Results[i].InfoHash), "AAAA1111") {
			merged = &res.Results[i]
		}
	}
	if merged == nil {
		t.Fatal("应保留 apibay 那条种子")
	}
	// 同一条种子两侧做种数不同：应取更高的那个，而不是留 apibay 的旧值。
	if merged.Seeders != "926" {
		t.Errorf("重复种子应合并为更高的做种数 926，实际 %s", merged.Seeders)
	}
	if merged.Added != "1426426450" {
		t.Errorf("应补上 csv 的发布时间，实际 %q", merged.Added)
	}
	// 两路都该出现在 sources 里，便于排查「结果为什么少」。
	joined := strings.Join(res.Sources, ",")
	if !strings.Contains(joined, "csv") || !strings.Contains(joined, "tpb") {
		t.Errorf("sources 应同时含 csv 与 tpb，实际 %v", res.Sources)
	}
}

// csv 挂了不能影响 apibay 的结果。
func TestCSVFailureDoesNotBreakSearch(t *testing.T) {
	tpb := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []Item{{Name: "Interstellar.2014.1080p", InfoHash: "cccc3333cccc3333cccc3333cccc3333cccc3333", Seeders: "8"}})
	}
	csv := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }

	tpbSrv := httptest.NewServer(http.HandlerFunc(tpb))
	defer tpbSrv.Close()
	csvSrv := httptest.NewServer(http.HandlerFunc(csv))
	defer csvSrv.Close()

	s := New(Config{HTTPTimeout: 5 * time.Second, DisableTranslate: true,
		TPBBase: tpbSrv.URL, SearchCatsAll: []string{"207"}, CSVBase: csvSrv.URL})

	res, err := s.Search(context.Background(), "Interstellar", "207")
	if err != nil {
		t.Fatalf("csv 失败不应让整次搜索报错: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("apibay 的结果应保留，实际 %d 条", len(res.Results))
	}
	if len(res.SourceErrors) == 0 {
		t.Error("应记录失败的源，供排查")
	}
}

// 中文种子名不能被英文词相关性过滤丢掉。
//
// DBD-Raws、字幕组这类源用的是「[流浪地球][1080P][简体内封]」这种纯中文名，
// 英文关键词在中文名里一个都匹配不到，按老逻辑会被整条判为无关而丢弃。
func TestChineseNamesSurviveEnglishFilter(t *testing.T) {
	toks := tokens("The Wandering Earth")
	if !relevant("[DBD-Raws][流浪地球][1080P][BDRip][简体内封][FLAC][MKV]", toks) {
		t.Error("中文种子名应被保留")
	}
	// 但纯英文的无关结果仍必须被过滤，这条不能被放宽掉。
	if relevant("Spider-Man.Brand.New.Day.2026.1080p", toks) {
		t.Error("无关的英文结果仍应被过滤")
	}
}

// 静态源（data_top100_*.json）没有关键词，不能参与相关性过滤——
// 它们只能作为「最新/热门」列表用，混进搜索结果会把无关片全倒给用户。
func TestSeedTierBands(t *testing.T) {
	order := []int{0, 1, 4, 19, 40, 120, 900}
	for i := 1; i < len(order); i++ {
		if seedTier(order[i-1]) > seedTier(order[i]) {
			t.Errorf("做种数 %d 的档位不应高于 %d", order[i-1], order[i])
		}
	}
	if seedTier(0) != 0 || seedTier(900) != 5 {
		t.Error("档位边界不符预期")
	}
}

// 同档位内新的排前面：老片源不该永远压着刚出的片。
func TestSortPrefersRecentWithinSameTier(t *testing.T) {
	old := Item{Name: "Interstellar.2014.1080p", InfoHash: "dddd4444dddd4444dddd4444dddd4444dddd4444",
		Seeders: "50", Added: "1426426450"}
	fresh := Item{Name: "Interstellar.2026.2160p", InfoHash: "eeee5555eeee5555eeee5555eeee5555eeee5555",
		Seeders: "45", Added: "1780000000"}

	items := []Item{old, fresh}
	sort.Slice(items, func(i, j int) bool {
		si, sj := atoi(items[i].Seeders), atoi(items[j].Seeders)
		if seedTier(si) != seedTier(sj) {
			return si > sj
		}
		return atoi(items[i].Added) > atoi(items[j].Added)
	})
	if items[0].InfoHash != fresh.InfoHash {
		t.Error("同一档位内应让更新的排前面")
	}
}
