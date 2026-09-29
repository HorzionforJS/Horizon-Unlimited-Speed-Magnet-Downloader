package torsearch

import "testing"

// TestSplitSeedName 锁定「整串不能翻」这个前提：必须能干净地切出片名，
// 否则翻译会把画质/组名一起翻烂。
func TestSplitSeedName(t *testing.T) {
	cases := []struct {
		raw    string
		title  string
		marker string
	}{
		{"The.Wandering.Earth.2019.DUBBED.1080p.WEBRip.1400MB.DD5.1.x264-GalaxyRG", "The Wandering Earth", ""},
		{"The.Wandering.Earth.II.2023.Chinese.1080p.WEB-DL.HC.H264.AAC-HHWEB", "The Wandering Earth II", ""},
		{"The Wandering Earth 2 2023 1080p (Dual) BluRay HEVC x265 5.1 BONE", "The Wandering Earth 2", ""},
		{"Infernal.Affairs.2002.CHINESE.REMASTERED.1080p.BluRay.DDP5.1.x264", "Infernal Affairs", ""},
		{"Wolf.Warrior.2015.1080p.BluRay.x264", "Wolf Warrior", ""},
		{"[TGx]Some.Movie.2020.1080p", "Some Movie", ""},
		{"Futurama S14E10 720p WEB H264-JFF", "Futurama", "S14E10"},
		{"Lanterns.S01E07.1080p.WEBRip.10Bit.DDP5.1.x265-NeoNoir", "Lanterns", "S01E07"},
		{"Big.Brother.US.S28E40.1080p.WEB.h264-EDITH", "Big Brother US", "S28E40"},
	}
	for _, c := range cases {
		gotTitle, gotMarker, _ := splitSeedName(c.raw)
		if gotTitle != c.title {
			t.Errorf("splitSeedName(%q) 片名 = %q，期望 %q", c.raw, gotTitle, c.title)
		}
		if gotMarker != c.marker {
			t.Errorf("splitSeedName(%q) 季集 = %q，期望 %q", c.raw, gotMarker, c.marker)
		}
	}
}

// TestSplitSeedNameNeverLosesQualityTag 确认技术标签没被切掉（用户要靠它选版本）。
func TestSplitSeedNameNeverLosesQualityTag(t *testing.T) {
	raw := "Infernal.Affairs.2002.CHINESE.REMASTERED.1080p.BluRay.DDP5.1.x264"
	_, _, tail := splitSeedName(raw)
	for _, want := range []string{"2002", "1080p", "BluRay", "x264"} {
		if !contains(tail, want) {
			t.Errorf("技术标签 %q 丢了，实际 tail = %q", want, tail)
		}
	}
}

func TestTidyTail(t *testing.T) {
	cases := map[string]string{
		"2019 DUBBED 1080p WEBRip 1400MB DD5 1 x264 GalaxyRG": "2019 DUBBED 1080p WEBRip DD5 x264 GalaxyRG",
		"2023 Chinese 1080p WEB DL HC H264 AAC HHWEB":         "2023 Chinese 1080p WEB DL HC H264 AAC HHWEB",
	}
	for in, want := range cases {
		if got := tidyTail(in); got != want {
			t.Errorf("tidyTail(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestJoinNameZh 是这次改动的核心行为：中文片名 + 保留的英文技术标签。
func TestJoinNameZh(t *testing.T) {
	raw := "The.Wandering.Earth.2019.DUBBED.1080p.WEBRip.1400MB.DD5.1.x264-GalaxyRG"
	got := joinNameZh("流浪地球", raw)
	want := "流浪地球  2019 DUBBED 1080p WEBRip DD5 x264 GalaxyRG"
	if got != want {
		t.Errorf("joinNameZh = %q\n期望 %q", got, want)
	}

	// 剧集：季集标记要保留
	got = joinNameZh("灯笼", "Lanterns.S01E07.1080p.WEBRip.10Bit.DDP5.1.x265-NeoNoir")
	if !contains(got, "S01E07") {
		t.Errorf("剧集标记丢了: %q", got)
	}
	if !contains(got, "灯笼") {
		t.Errorf("中文名丢了: %q", got)
	}
}

// TestJoinNameZhSkipsNonChinese 译文没有中文时不应冒充中文名。
func TestJoinNameZhSkipsNonChinese(t *testing.T) {
	for _, bad := range []string{"", "Futurama", "The Wandering Earth"} {
		if got := joinNameZh(bad, "The.Wandering.Earth.2019.1080p"); got != "" {
			t.Errorf("joinNameZh(%q, ...) = %q，期望空串", bad, got)
		}
	}
}

// TestJoinNameZhKeepsOriginalOnBadTranslation 译文与原名一致时跳过，避免显示重复信息。
func TestJoinNameZhKeepsOriginalOnBadTranslation(t *testing.T) {
	if got := joinNameZh("Futurama", "Futurama.S14E10.720p.WEB"); got != "" {
		t.Errorf("译名与原名相同时应返回空串，实际 %q", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
