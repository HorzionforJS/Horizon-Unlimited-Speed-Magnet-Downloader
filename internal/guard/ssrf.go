package guard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ValidateURL 校验用户提交的 URL，防止 SSRF：
// 仅允许 http/https，拒绝回环、私有（RFC1918）、链路本地、未指定地址及云元数据端点。
// 对域名做 DNS 解析并对每个解析结果校验。
//
// 注意：这个函数只做「解析期」校验，本身不能单独防住 SSRF——
// DNS 在校验和真正请求之间可以被重新解析（DNS rebinding / TOCTOU）。
// 真正的防线是 SafeDialContext：它在建立连接时对实际 IP 再校验一次。
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("仅允许 http/https 协议，收到 %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("缺少主机名")
	}

	if ip := net.ParseIP(host); ip != nil {
		if isForbidden(ip) {
			return fmt.Errorf("禁止访问内网/保留地址: %s", ip)
		}
		return nil
	}

	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("域名解析失败: %v", err)
	}
	for _, a := range addrs {
		if isForbidden(a) {
			return fmt.Errorf("目标 %s 解析到禁止访问的内网/保留地址 %s", host, a)
		}
	}
	return nil
}

// forbiddenNets 是需要显式拦截的保留网段。
//
// net.IP 自带的方法只覆盖一部分：IsPrivate() 只管 RFC1918 与 IPv6 ULA，
// IsUnspecified() 只管全零。像 CGNAT(100.64/10)、组播、benchmark 段、
// 文档段以及 NAT64 前缀这些都不会被上面几个方法判到，但打过去同样可能
// 落到内网或运营商网络中继上，所以必须显式列出来。
var forbiddenNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",       // 本网络
		"10.0.0.0/8",      // RFC1918
		"100.64.0.0/10",   // CGNAT
		"127.0.0.0/8",     // 回环
		"169.254.0.0/16",  // 链路本地（含 169.254.169.254 云元数据）
		"172.16.0.0/12",   // RFC1918
		"192.0.0.0/24",    // IETF 协议分配
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 中继
		"192.168.0.0/16",  // RFC1918
		"198.18.0.0/15",   // benchmark
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // 组播
		"240.0.0.0/4",     // 保留（含 255.255.255.255）
		"::/128",          // 未指定
		"::1/128",         // 回环
		"64:ff9b::/96",    // NAT64，可能映射到内网 IPv4
		"fc00::/7",        // IPv6 ULA
		"fe80::/10",       // IPv6 链路本地
		"ff00::/8",        // IPv6 组播
		"2001:db8::/32",   // 文档用
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

func isForbidden(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate() {
		return true
	}
	// IPv4-mapped IPv6（::ffff:10.0.0.1 这类）要按 IPv4 再判一次，
	// 否则可能绕过只针对 v4 网段的规则。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range forbiddenNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// IsForbiddenIP 供其它包复用同一套判定。
func IsForbiddenIP(ip net.IP) bool { return isForbidden(ip) }

// SafeDialContext 在建立连接的那一刻对「实际要连的 IP」再校验一次。
//
// 这是 SSRF 防护的关键：ValidateURL 只能保证「解析时」是公网地址，
// 攻击者可以用一个先解析到公网、稍后再解析到 127.0.0.1 的域名绕过它。
// 这里改成自己解析、自己校验、按校验过的 IP 直连，
// 既堵住 TOCTOU，也避免 Transport 再走一次 DNS。
func SafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("地址解析失败: %w", err)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("域名解析失败: %w", err)
	}
	var lastErr error
	for _, ip := range ips {
		if isForbidden(ip) {
			lastErr = fmt.Errorf("目标 %s 解析到禁止访问的内网/保留地址 %s", host, ip)
			continue
		}
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("目标 %s 没有可用地址", host)
	}
	return nil, lastErr
}

// SafeHTTPClient 返回一个所有连接都经过 SSRF 校验、且限制跳转的 HTTP 客户端。
//
// 任何「URL 来自用户或第三方页面」的抓取都应该用它，而不是裸 http.Client：
// 裸客户端既不做 IP 校验，默认还会无限制跟随 302/307，
// 于是「第一跳合法、第二跳指向内网」就能绕过白名单。
//
// 不读 HTTP_PROXY 等环境变量代理：代理会让请求由代理端发起，
// 本进程的 IP 校验对代理的实际连接没有任何约束力。
func SafeHTTPClient(timeout time.Duration, maxRedirects int) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if maxRedirects <= 0 {
		maxRedirects = 5
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           SafeDialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("跳转次数超过 %d 次", maxRedirects)
			}
			return ValidateURL(req.URL.String())
		},
	}
}
