package guard

import "strings"

// hexLen 是磁力链接里 info hash 的十六进制长度（sha1 = 20 字节）。
const hexLen = 40

// ExtractInfoHash 从磁力链接里提取 40 位小写十六进制 info hash；
// 无法提取时返回空串。兼容 "magnet:?xt=urn:btih:..." 与 "magnet:?xt=btih:..."。
//
// 两个必须守住的点：
//  1. 长度与字符集必须严格匹配。调用方是拿返回值去和黑名单里的 40 位 hash
//     做等值比较的，任何截断/乱码都会变成「另一个 hash」，于是本该拦住的
//     资源被放行。所以宁可返回空串走「无法判定」分支。
//  2. 不能只看第一个 "btih:"。`dn=btih:deadbeef` 这种参数值里出现 btih
//     完全合法，先命中它就会取到错的串；因此逐个候选位置尝试，
//     直到找到真正合规的 40 位 hash。
func ExtractInfoHash(magnetURI string) string {
	low := strings.ToLower(magnetURI)
	for from := 0; ; {
		i := strings.Index(low[from:], "btih:")
		if i < 0 {
			return ""
		}
		start := from + i + len("btih:")
		if h, ok := readHex40(low, start); ok {
			return h
		}
		from = start
		if from >= len(low) {
			return ""
		}
	}
}

// readHex40 从 s[start:] 读取一个恰好 40 位、且不被更长十六进制串包围的 hash。
func readHex40(s string, start int) (string, bool) {
	if start+hexLen > len(s) {
		return "", false
	}
	for j := start; j < start+hexLen; j++ {
		if !isHex(s[j]) {
			return "", false
		}
	}
	// 第 41 位如果还是十六进制，说明这是一个更长的串（如 base32 或畸形链接），
	// 截断前 40 位会得到错误 hash，必须判为不可用。
	if start+hexLen < len(s) && isHex(s[start+hexLen]) {
		return "", false
	}
	return s[start : start+hexLen], true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
