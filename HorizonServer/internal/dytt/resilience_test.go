package dytt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ============ 源站故障时的行为 ============
//
// 背景（真实事故，2026-09-29）：dytt.org.cn 在 Cloudflare 后面返回 522，
// 服务端原本会「单次 15s × 重试 3 次 + 退避」，最坏 46.5s 才返回，
// 而客户端读超时只有 20s——用户只看到一句「无法连接云端服务…操作超时」，
// 真正的原因一句都传不回去。下面这些用例锁住修复后的行为。

// errTransport 模拟「连不上源站」。
var errTransport = errors.New("dial tcp: i/o timeout")

// scriptedTransport 按脚本决定每次请求是失败还是返回页面。
// 用一个可变的 fail 计数而不是替换整个 Transport，避免测试里访问不了内部状态。
type scriptedTransport struct {
	attempts int
	failFor  int // 前 N 次请求失败；负数表示一直失败
	body     string
	status   int
}

func (s *scriptedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.attempts++
	if s.failFor < 0 || s.attempts <= s.failFor {
		return nil, errTransport
	}
	code := s.status
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    r,
	}, nil
}

func newScripted(t *testing.T, tr *scriptedTransport, callTimeout time.Duration) *Client {
	t.Helper()
	return New(Config{
		BaseURL:     "https://dytt.org.cn",
		MinInterval: 1, // 1ns，测试不需要限速
		Retries:     3,
		Timeout:     50 * time.Millisecond,
		CallTimeout: callTimeout,
		HTTPClient:  &http.Client{Transport: tr},
	})
}

// 源站连不上时：必须回退到过期缓存，并用 stale 如实标记。
//
// 没有这条回退，客户端就是一个「加载最新更新失败」的错误弹窗，什么都看不到。
func TestServiceFallsBackToStaleCache(t *testing.T) {
	tr := &scriptedTransport{failFor: 0, body: loadFixture(t, "list_page.html")}
	client := newScripted(t, tr, 2*time.Second)
	svc := NewService(client, 1) // TTL=1ns：第一次写进去就已经过期了

	first, err := svc.ListPage(context.Background(), "movie", 1)
	if err != nil {
		t.Fatalf("首次请求应成功: %v", err)
	}
	if first.Count == 0 {
		t.Fatal("首次请求应解析出条目")
	}
	if first.Stale {
		t.Error("首次请求不该标记为过期")
	}

	// 源站彻底挂掉
	tr.failFor = -1

	second, err := svc.ListPage(context.Background(), "movie", 1)
	if err != nil {
		t.Fatalf("源站故障时应回退到过期缓存而不是报错: %v", err)
	}
	if !second.Stale {
		t.Error("回退到过期缓存时必须标记 stale，否则用户会把旧数据当实时数据")
	}
	if second.Count != first.Count {
		t.Errorf("回退的应是与上次相同的内容: got %d, want %d", second.Count, first.Count)
	}
}

// 熔断：源站故障后的一段时间内不再打上游，
// 否则每个客户端请求都要白等一个 CallTimeout 才拿到「源站不可用」。
func TestServiceTripsBreakerOnUpstreamFailure(t *testing.T) {
	tr := &scriptedTransport{failFor: -1}
	client := newScripted(t, tr, time.Second)
	svc := NewService(client, time.Minute)

	if _, err := svc.ListPage(context.Background(), "movie", 1); err == nil {
		t.Fatal("源站挂掉且无缓存时应当报错")
	}
	after := tr.attempts
	if after == 0 {
		t.Fatal("第一次应当真的去请求上游")
	}

	for i := 0; i < 3; i++ {
		if _, err := svc.ListPage(context.Background(), "movie", 1); err == nil {
			t.Fatal("熔断期内无缓存仍应报错")
		}
	}
	if tr.attempts != after {
		t.Errorf("熔断期内不应再打上游: attempts=%d, 期望仍为 %d", tr.attempts, after)
	}
}

// 熔断只针对「源站级」故障；限流与 404 说明源站是活的，不该熔断。
func TestBreakerIgnoresRateLimitAndNotFound(t *testing.T) {
	svc := NewService(New(Config{MinInterval: 1}), time.Minute)
	svc.tripBreakerLocked(ErrRateLimited{URL: "u", Status: 400})
	if !svc.breakerUntil.IsZero() {
		t.Error("限流不应触发熔断（源站是活的，只是要求降频）")
	}
	svc.tripBreakerLocked(ErrNotFound{URL: "u"})
	if !svc.breakerUntil.IsZero() {
		t.Error("404 不应触发熔断（源站是活的，只是没有这个资源）")
	}
	svc.tripBreakerLocked(ErrUpstreamDown{URL: "u", Err: errors.New("timeout")})
	if svc.breakerUntil.IsZero() {
		t.Error("源站连不上应当触发熔断")
	}
}

// 上游恢复后必须自动解除熔断，否则服务会一直停在「源站不可用」的状态不回头。
func TestServiceClearsBreakerAfterRecovery(t *testing.T) {
	tr := &scriptedTransport{failFor: 1, body: loadFixture(t, "list_page.html")}
	client := newScripted(t, tr, 2*time.Second)
	client.cfg.Retries = 1 // 关掉重试，让「失败一次」就是「调用失败」
	svc := NewService(client, 0)

	if _, err := svc.ListPage(context.Background(), "movie", 1); err == nil {
		t.Fatal("第一次应当失败（模拟源站抖动）")
	}
	if svc.breakerUntil.IsZero() {
		t.Fatal("失败一次应当触发熔断")
	}

	// 熔断期内即使源站已经好了也不该打上游。
	before := tr.attempts
	if _, err := svc.ListPage(context.Background(), "movie", 1); err == nil {
		t.Fatal("熔断期内应当直接报错")
	}
	if tr.attempts != before {
		t.Errorf("熔断期内不应打上游: attempts=%d, 期望 %d", tr.attempts, before)
	}

	// 把冷却期拨到过去（测试里不真的等一分钟），源站恢复后应成功并解除熔断。
	svc.mu.Lock()
	svc.breakerUntil = time.Now().Add(-time.Second)
	svc.mu.Unlock()

	if _, err := svc.ListPage(context.Background(), "movie", 1); err != nil {
		t.Fatalf("冷却期结束后应当成功: %v", err)
	}
	if !svc.breakerUntil.IsZero() {
		t.Error("成功一次后应解除熔断")
	}
}

// 熔断且无缓存时，错误文案要说清楚是「源站不可用」以及大概何时恢复，
// 而不是把原始的网络错误抛给用户。
func TestBreakerErrorIsActionable(t *testing.T) {
	tr := &scriptedTransport{failFor: -1}
	client := newScripted(t, tr, time.Second)
	client.cfg.Retries = 1
	svc := NewService(client, 0)

	if _, err := svc.ListPage(context.Background(), "movie", 1); err == nil {
		t.Fatal("第一次应当失败")
	}
	_, err := svc.ListPage(context.Background(), "movie", 1)
	if err == nil {
		t.Fatal("熔断期内应当报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "暂时不可用") {
		t.Errorf("错误应说明源站不可用，实际: %s", msg)
	}
	if !strings.Contains(msg, "自动重试") {
		t.Errorf("错误应说明会自动重试，实际: %s", msg)
	}
}

// 确定性错误（404）不该重试：重试只是多赔几次超时。
func TestClientDoesNotRetryNotFound(t *testing.T) {
	tr := &scriptedTransport{body: "404", status: http.StatusNotFound}
	c := newScripted(t, tr, 5*time.Second)
	if _, err := c.get(context.Background(), "https://dytt.org.cn/p/1/"); err == nil {
		t.Fatal("404 应当报错")
	}
	if tr.attempts != 1 {
		t.Errorf("404 不应重试: attempts=%d, 期望 1", tr.attempts)
	}
}

// 可恢复错误（超时）应当重试，但要被 CallTimeout 兜住。
func TestClientRetriesTimeoutWithinCallTimeout(t *testing.T) {
	tr := &scriptedTransport{failFor: -1}
	c := New(Config{
		BaseURL:     "https://dytt.org.cn",
		MinInterval: 1,
		Retries:     5,
		Timeout:     50 * time.Millisecond,
		CallTimeout: 400 * time.Millisecond,
		HTTPClient:  &http.Client{Transport: tr},
	})
	start := time.Now()
	if _, err := c.get(context.Background(), "https://dytt.org.cn/"); err == nil {
		t.Fatal("一直超时应当报错")
	}
	elapsed := time.Since(start)
	// 总时限 400ms；退避是 attempt^2*500ms + 抖动，
	// 所以第一次重试前的等待（约 0.5s）就已经越过总时限——正好证明它真的在起作用。
	if elapsed > 3*time.Second {
		t.Errorf("总时限没有被遵守: 耗时 %v", elapsed)
	}
	if tr.attempts >= 5 {
		t.Errorf("总时限应当叫停重试: attempts=%d, 期望远小于重试上限 5", tr.attempts)
	}
}

// 退避还没走完、总时限也没到，就应该老老实实重试。
func TestClientRetriesWhenBudgetAllows(t *testing.T) {
	tr := &scriptedTransport{failFor: 2, body: "<html><title>ok</title></html>"}
	c := New(Config{
		BaseURL:     "https://dytt.org.cn",
		MinInterval: 1,
		Retries:     4,
		Timeout:     50 * time.Millisecond,
		CallTimeout: 5 * time.Second,
		HTTPClient:  &http.Client{Transport: tr},
	})
	body, err := c.get(context.Background(), "https://dytt.org.cn/")
	if err != nil {
		t.Fatalf("第三次应当成功: %v", err)
	}
	if body == "" {
		t.Error("应返回页面内容")
	}
	if tr.attempts != 3 {
		t.Errorf("应当重试到第 3 次成功: attempts=%d", tr.attempts)
	}
}

// 传输层错误要归类成 ErrUpstreamDown，接口层才能给出「源站不可用」而不是原始网络错误。
func TestClientClassifiesUpstreamDown(t *testing.T) {
	tr := &scriptedTransport{failFor: -1}
	c := newScripted(t, tr, time.Second)
	_, err := c.get(context.Background(), "https://dytt.org.cn/")
	var down ErrUpstreamDown
	if !errors.As(err, &down) {
		t.Errorf("连接失败应归类为 ErrUpstreamDown，实际 %T: %v", err, err)
	}
}
