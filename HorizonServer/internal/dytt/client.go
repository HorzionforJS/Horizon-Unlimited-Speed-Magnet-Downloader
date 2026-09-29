package dytt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"horizon/internal/guard"
)

// 站点对同一 IP 的高频请求会返回 HTTP 400，因此内置最小请求间隔 + 指数退避重试。
//
// 超时这三个数是按「客户端最多等得到多久」倒推的，不是拍出来的：
// 客户端读超时 20s，服务端必须**明显早于**它返回，否则用户看到的永远是
// HTTP 层超时（客户端只能报「无法连接云端服务…操作超时」），
// 真正的原因（源站挂了 / 被限流）一句都传不回去。
//
// 最坏情况 = 单次超时 × 重试次数 + 退避总和：
// 之前是 15s × 3 + 1.5s ≈ 46.5s，比客户端那 20s 还长，必然白等。
// 现在 5s × 2 + 0.5s ≈ 10.5s，留出余量给客户端与其它链路。
//
// 重试只对**可恢复错误**（超时、连接失败、限流）生效；404/权限之类的
// 确定性错误立刻返回，重试纯属浪费时间。
const (
	defaultTimeout     = 5 * time.Second
	defaultMinInterval = 400 * time.Millisecond
	defaultRetries     = 2
	defaultCallTimeout = 12 * time.Second
	maxBodyBytes       = 8 << 20
	userAgent          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
)

// ErrNotFound 表示目标资源不存在（404）。
type ErrNotFound struct{ URL string }

func (e ErrNotFound) Error() string { return "资源不存在: " + e.URL }

// ErrRateLimited 表示被上游限流（该站点用 400 表示请求过频）。
type ErrRateLimited struct {
	URL    string
	Status int
}

func (e ErrRateLimited) Error() string {
	return fmt.Sprintf("被目标站点限流（HTTP %d），请降低频率或稍后重试", e.Status)
}

// HTTPError 表示其它非预期状态码。
type HTTPError struct {
	URL    string
	Status int
}

func (e HTTPError) Error() string { return fmt.Sprintf("上游返回 HTTP %d: %s", e.Status, e.URL) }

// Config 控制抓取行为。
type Config struct {
	BaseURL     string
	Referer     string
	HTTPClient  *http.Client
	MinInterval time.Duration
	Retries     int
	// Timeout 是单次 HTTP 请求超时；留空用 defaultTimeout。
	Timeout time.Duration
	// CallTimeout 是一次 get 调用（含全部重试与退避）的总时限；留空用 defaultCallTimeout。
	CallTimeout time.Duration
}

func (c Config) withDefaults() Config {
	out := c
	if strings.TrimSpace(out.BaseURL) == "" {
		out.BaseURL = "https://dytt.org.cn"
	}
	out.BaseURL = strings.TrimRight(out.BaseURL, "/")
	if out.Referer == "" {
		out.Referer = "https://home.dytiantang.com.cn/"
	}
	if out.Timeout <= 0 {
		out.Timeout = defaultTimeout
	}
	if out.CallTimeout <= 0 {
		out.CallTimeout = defaultCallTimeout
	}
	if out.HTTPClient == nil {
		// 默认客户端必须走 SSRF 防护：内容源页面里的链接是第三方可控数据，
		// 裸 http.Client 既不校验解析到的 IP，也会无限制跟随 302。
		out.HTTPClient = guard.SafeHTTPClient(out.Timeout, 5)
	}
	if out.MinInterval <= 0 {
		out.MinInterval = defaultMinInterval
	}
	if out.Retries <= 0 {
		out.Retries = defaultRetries
	}
	return out
}

// Client 是电影天堂元数据抓取客户端，可安全并发使用。
type Client struct {
	cfg  Config
	mu   sync.Mutex
	last time.Time
}

// New 构造客户端。
func New(cfg Config) *Client { return &Client{cfg: cfg.withDefaults()} }

// BaseURL 返回内容站地址。
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

func (c *Client) throttle(ctx context.Context) {
	c.mu.Lock()
	wait := c.cfg.MinInterval - time.Since(c.last)
	if wait > 0 {
		c.last = time.Now().Add(wait)
	} else {
		c.last = time.Now()
	}
	c.mu.Unlock()
	if wait <= 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ErrUpstreamDown 表示内容源连不上（超时 / 连接失败 / 网关错误），
// 与「限流」区分开：前者只需等待，后者要降低频率。
type ErrUpstreamDown struct {
	URL string
	Err error
}

func (e ErrUpstreamDown) Error() string {
	return "内容源暂时不可用: " + e.Err.Error()
}
func (e ErrUpstreamDown) Unwrap() error { return e.Err }

// retryable 判断错误是否值得重试。
//
// 404（资源不存在）、参数错、解析失败之类的确定性错误重试没有意义：
// 同样的请求再发一次只会得到同样的结果，却要多赔上两次超时。
// 只有「超时 / 连接失败 / 限流 / 5xx」才可能自愈。
func retryable(err error) bool {
	var rate ErrRateLimited
	if errors.As(err, &rate) {
		return true
	}
	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status >= 500
	}
	var notFound ErrNotFound
	if errors.As(err, &notFound) {
		return false
	}
	var down ErrUpstreamDown
	if errors.As(err, &down) {
		return true
	}
	return false
}

// get 拉取页面并解码为 UTF-8 字符串，内置限速与重试。
//
// 整次调用（含重试与退避）受 CallTimeout 约束：源站整体挂掉时，
// 三次各自超时会把请求拖到几十秒，远超客户端等待上限，
// 结果就是用户在客户端上只看到一句「操作超时」，什么诊断信息都拿不到。
func (c *Client) get(ctx context.Context, rawURL string) (string, error) {
	if c.cfg.CallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.CallTimeout)
		defer cancel()
	}

	var lastErr error
	for attempt := 0; attempt < c.cfg.Retries; attempt++ {
		if attempt > 0 {
			// 指数退避 + 抖动，避免多个请求同时重试再次触发限流
			backoff := time.Duration(attempt*attempt) * 500 * time.Millisecond
			backoff += time.Duration(rand.Intn(250)) * time.Millisecond
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", lastErr
			case <-timer.C:
			}
		}
		body, err := c.once(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) {
			return "", err
		}
		// 总时限用完就别再试下一次了，把已经知道的原因带回去。
		if ctx.Err() != nil {
			break
		}
	}
	return "", lastErr
}

func (c *Client) once(ctx context.Context, rawURL string) (string, error) {
	c.throttle(ctx)

	// 只允许抓取配置的内容源主机：rawURL 有相对链接拼接与用户传入的 target
	// 两条来源，必须在发请求前收敛到已知主机，避免被当成任意 URL 抓取器。
	if !c.hostAllowed(rawURL) {
		return "", fmt.Errorf("拒绝抓取非内容源地址: %s", rawURL)
	}
	if err := guard.ValidateURL(rawURL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", c.cfg.Referer)
	// 显式声明不接收压缩：嗅探图片真实格式（WebP 转 JPEG）依赖原始字节，
	// 一旦上游 gzip 压缩，magic bytes 就对不上，转码会整个失效。
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		// 连不上（超时 / 连接被拒 / TLS 失败）单独归类：
		// 上层据此给出「源站不可用」这种用户能理解的提示，而不是抛原始网络错误。
		return "", ErrUpstreamDown{URL: rawURL, Err: err}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrNotFound{URL: rawURL}
	case resp.StatusCode == http.StatusBadRequest:
		// 该站点用 400 表示限流
		return "", ErrRateLimited{URL: rawURL, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrRateLimited{URL: rawURL, Status: resp.StatusCode}
	case resp.StatusCode >= 400:
		return "", HTTPError{URL: rawURL, Status: resp.StatusCode}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	return decodeHTML(raw, resp.Header.Get("Content-Type")), nil
}

// decodeHTML 依据 Content-Type 与页面内 meta 声明解码；站点主要为 utf-8，兜底 GB18030。
func decodeHTML(raw []byte, contentType string) string {
	charset := strings.ToLower(charsetFrom(contentType))
	if charset == "" {
		head := raw
		if len(head) > 4096 {
			head = head[:4096]
		}
		charset = strings.ToLower(charsetFrom(string(head)))
	}
	if strings.Contains(charset, "gb") || strings.Contains(charset, "18030") {
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
			return string(out)
		}
	}
	return string(bytes.ToValidUTF8(raw, []byte("")))
}

// charsetFrom 从 Content-Type 或 <meta> 片段里提取 charset 值。
func charsetFrom(s string) string {
	lower := strings.ToLower(s)
	idx := strings.Index(lower, "charset")
	if idx < 0 {
		return ""
	}
	rest := s[idx+len("charset"):]
	rest = strings.TrimLeft(rest, " \t\r\n")
	if strings.HasPrefix(rest, "=") {
		rest = rest[1:]
	}
	rest = strings.TrimLeft(rest, " \t\r\n\"'")
	end := strings.IndexAny(rest, "\"' \t\r\n;/>")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// absURL 把相对链接转成绝对地址（Client 便捷方法）。
// hostAllowed 判断 URL 的主机是否等于配置的内容源主机（或其子域）。
//
// 这是「抓取范围」的第一道闸：allowlist 之外的地址一律不抓，
// 免得内容源页面里塞一个外链就把本服务变成任意 URL 抓取器。
func (c *Client) hostAllowed(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	want := baseHost(c.cfg.BaseURL)
	got := strings.ToLower(u.Hostname())
	if want == "" || got == "" {
		return false
	}
	return got == want || strings.HasSuffix(got, "."+want)
}

// baseHost 取站点前缀的主机名。
func baseHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (c *Client) absURL(href string) string { return absURL(c.cfg.BaseURL, href) }

// absURL 按给定站点前缀把相对链接转成绝对地址。
func absURL(baseURL, href string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "/") {
		return baseURL + href
	}
	return baseURL + "/" + strings.TrimPrefix(href, "./")
}

// resolveDetailURL 接受详情页 URL、/p/ID/ 路径或纯数字 ID。
func (c *Client) resolveDetailURL(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	allDigits := true
	for _, r := range target {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return c.cfg.BaseURL + "/p/" + target + "/", true
	}
	if strings.HasPrefix(target, "/") {
		return c.cfg.BaseURL + ensureTrailingSlash(target), true
	}
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		// 允许 home.dytiantang.com.cn 外壳地址：内容其实在 dytt.org.cn 上
		if !strings.Contains(u.Path, "/p/") {
			return "", false
		}
		return c.cfg.BaseURL + ensureTrailingSlash(u.Path), true
	}
	if idx := strings.Index(target, "/p/"); idx >= 0 {
		rest := target[idx:]
		if end := strings.IndexAny(rest, "?#"); end >= 0 {
			rest = rest[:end]
		}
		return c.cfg.BaseURL + ensureTrailingSlash(rest), true
	}
	return "", false
}

func ensureTrailingSlash(p string) string {
	q := ""
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		q, p = p[i:], p[:i]
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + q
}
