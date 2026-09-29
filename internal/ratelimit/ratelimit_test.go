package ratelimit

import (
	"testing"
	"time"
)

func TestMemoryLimiterWindow(t *testing.T) {
	l := NewMemoryLimiter(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("超过上限后应被拒绝")
	}
	time.Sleep(70 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("窗口过期后应重新放行")
	}
	// 不同 key 互不影响
	if !l.Allow("other") {
		t.Fatal("不同 key 不应互相影响")
	}
}

// 过期的 key 必须被回收，否则长期运行会持续吃内存。
func TestMemoryLimiterSweepsExpiredKeys(t *testing.T) {
	l := NewMemoryLimiter(1, 20*time.Millisecond)
	for i := 0; i < sweepThreshold+50; i++ {
		l.Allow(string(rune('a'+i%26)) + string(rune('a'+i/26)) + "-key")
	}
	time.Sleep(40 * time.Millisecond)

	l.mu.Lock()
	before := len(l.hits)
	l.sweepLocked(time.Now().Add(-l.window))
	after := len(l.hits)
	l.mu.Unlock()
	if before == 0 {
		t.Fatal("清理前应有 key 记录")
	}
	if after != 0 {
		t.Errorf("过期 key 应被全部回收，仍有 %d 条", after)
	}
}

func TestMemoryLimiterZeroArgs(t *testing.T) {
	l := NewMemoryLimiter(0, 0)
	if !l.Allow("k") {
		t.Fatal("零值参数应被修正为可用配置，首次应放行")
	}
}
