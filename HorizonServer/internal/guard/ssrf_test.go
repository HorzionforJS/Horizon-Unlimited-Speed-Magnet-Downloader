package guard

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	cases := []struct {
		url  string
		fail bool
	}{
		{"http://example.com/file.torrent", false},
		{"https://example.com/file.torrent", false},
		{"http://127.0.0.1/admin", true},
		{"http://localhost/x", true},
		{"http://10.0.0.5/x", true},
		{"http://172.16.0.1/x", true},
		{"http://192.168.1.1/x", true},
		{"http://169.254.169.254/latest/meta-data", true},
		{"http://0.0.0.0/x", true},
		{"http://[::1]/x", true},
		// 新增覆盖：这些在只依赖 net.IP 自带方法时会被漏掉
		{"http://100.64.0.1/x", true},        // CGNAT
		{"http://224.0.0.1/x", true},         // 组播
		{"http://198.18.0.1/x", true},        // benchmark
		{"http://192.0.0.1/x", true},         // IETF 协议分配
		{"http://[::ffff:10.0.0.1]/x", true}, // IPv4-mapped IPv6
		{"http://[fc00::1]/x", true},         // IPv6 ULA
		{"ftp://example.com/file", true},
		{"file:///etc/passwd", true},
	}
	for _, c := range cases {
		err := ValidateURL(c.url)
		if c.fail && err == nil {
			t.Errorf("期望拒绝 %s，但通过了", c.url)
		}
		if !c.fail && err != nil {
			t.Errorf("期望放行 %s，但被拒: %v", c.url, err)
		}
	}
}

func TestIsForbiddenIP(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "10.1.2.3", "100.64.5.5", "127.0.0.1", "169.254.169.254",
		"172.20.0.1", "192.0.0.5", "192.168.5.5", "198.18.1.1", "224.0.0.1",
		"255.255.255.255", "::1", "fe80::1", "fc00::1", "ff02::1",
	}
	for _, s := range blocked {
		if !IsForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s 应被判定为禁止地址", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "104.16.0.1", "2606:4700::1111"}
	for _, s := range allowed {
		if IsForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s 不应被判定为禁止地址", s)
		}
	}
	if !IsForbiddenIP(nil) {
		t.Errorf("nil IP 应视为禁止")
	}
}

// SafeDialContext 必须直接拒绝内网地址，而不是把连接交给内核。
func TestSafeDialContextRejectsInternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, addr := range []string{"127.0.0.1:80", "169.254.169.254:80", "[::1]:80"} {
		if conn, err := SafeDialContext(ctx, "tcp", addr); err == nil {
			conn.Close()
			t.Errorf("SafeDialContext(%s) 应当被拒绝，却连上了", addr)
		}
	}
}

// SafeHTTPClient 必须拒绝指向内网的跳转，且不使用环境变量代理。
func TestSafeHTTPClientRedirectGuard(t *testing.T) {
	c := SafeHTTPClient(3*time.Second, 3)
	if c.Timeout != 3*time.Second {
		t.Errorf("timeout 应为 3s，实际 %v", c.Timeout)
	}
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect 不能为空，否则可被 302 绕到内网")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 应为 *http.Transport，实际 %T", c.Transport)
	}
	if tr.Proxy != nil {
		t.Error("不应使用环境变量代理：代理会让请求绕开本进程的 IP 校验")
	}
	if tr.DialContext == nil {
		t.Error("DialContext 必须挂上 SafeDialContext")
	}

	internal, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err := c.CheckRedirect(internal, nil); err == nil {
		t.Error("CheckRedirect 应拒绝指向云元数据地址的跳转")
	}
	public, _ := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err := c.CheckRedirect(public, nil); err != nil {
		t.Errorf("CheckRedirect 不应拒绝公网跳转: %v", err)
	}
	tooMany := make([]*http.Request, 5)
	if err := c.CheckRedirect(public, tooMany); err == nil {
		t.Error("跳转次数超上限时应报错")
	}
}
