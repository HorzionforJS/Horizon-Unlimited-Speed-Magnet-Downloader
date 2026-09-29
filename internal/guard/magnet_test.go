package guard

import "testing"

func TestExtractInfoHash(t *testing.T) {
	const h = "63dde13a0ebeabf4ee7e682563184ade0cdbe3c4"
	cases := []struct {
		in   string
		want string
	}{
		{"magnet:?xt=urn:btih:63DDE13A0EBEABF4EE7E682563184ADE0CDBE3C4&dn=test", h},
		{"magnet:?xt=btih:63DDE13A0EBEABF4EE7E682563184ADE0CDBE3C4", h},
		{"not a magnet", ""},
		{"magnet:?xt=urn:btih:", ""},
		// dn 里出现 btih 不能干扰真正的 xt 参数
		{"magnet:?dn=btih:deadbeef&xt=urn:btih:63DDE13A0EBEABF4EE7E682563184ADE0CDBE3C4", h},
		// 后面其它参数里的字面量不应被当 hash 的一部分
		{"magnet:?xt=urn:btih:63DDE13A0EBEABF4EE7E682563184ADE0CDBE3C4&xl=12345&dn=a", h},
	}
	for _, c := range cases {
		if got := ExtractInfoHash(c.in); got != c.want {
			t.Errorf("ExtractInfoHash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 黑名单只按 40 位十六进制比对，所以长度不对时必须返回空串，
// 否则「magnet:?xt=urn:btih:abc」这种残缺链接会被当成另一个 hash。
func TestExtractInfoHashRejectsBadLength(t *testing.T) {
	bad := []string{
		"magnet:?xt=urn:btih:abc", // 太短
		"magnet:?xt=urn:btih:" + "0123456789abcdef0123456789abcdef012345678", // 41 位
		"magnet:?xt=urn:btih:ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",       // 非十六进制
		"magnet:?xt=urn:btih:63dde13a0ebeabf4ee7e682563184ade0cdbe3c",        // 39 位
	}
	for _, in := range bad {
		if got := ExtractInfoHash(in); got != "" {
			t.Errorf("ExtractInfoHash(%q) 应为空，实际 %q", in, got)
		}
	}
}
