package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter 判断某个 key 是否放行（true=允许）。
type Limiter interface {
	Allow(key string) bool
}

// sweepThreshold 是触发被动清理的 map 规模。
//
// 每个不同 key（IP / 用户名）都会在 hits 里留一条记录。只保留窗口内的
// 时间戳并不足以避免内存增长：过期后时间戳被清空，但空 slice 仍然占着 key。
// 长期运行（或被扫描）时 map 会一直变大而永不收缩。
// 这里在超过阈值时顺手扫一遍，把已经完全过期的 key 删掉。
const sweepThreshold = 10000

// MemoryLimiter 固定窗口内存限流（无需外部依赖，默认）。
type MemoryLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func NewMemoryLimiter(limit int, window time.Duration) *MemoryLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &MemoryLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (m *MemoryLimiter) Allow(key string) bool {
	now := time.Now()
	cut := now.Add(-m.window)
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept []time.Time
	for _, t := range m.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= m.limit {
		m.hits[key] = kept
		return false
	}
	m.hits[key] = append(kept, now)
	if len(m.hits) > sweepThreshold {
		m.sweepLocked(cut)
	}
	return true
}

// sweepLocked 删除窗口内没有任何命中记录的 key（调用方必须已持锁）。
func (m *MemoryLimiter) sweepLocked(cut time.Time) {
	for k, times := range m.hits {
		fresh := false
		for _, t := range times {
			if t.After(cut) {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(m.hits, k)
		}
	}
}

// RedisLimiter 基于 Redis INCR+EXPIRE 的固定窗口限流（多实例部署推荐）。
type RedisLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	// fallback 在 Redis 不可用时接管，保证限流不会因为依赖故障而整体失效。
	fallback *MemoryLimiter
}

func NewRedisLimiter(addr, password string, limit int, window time.Duration) (*RedisLimiter, error) {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &RedisLimiter{
		client:   c,
		limit:    limit,
		window:   window,
		fallback: NewMemoryLimiter(limit, window),
	}, nil
}

func (r *RedisLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		// Redis 故障时不能直接放行——那等于限流整体失效，是可被利用的绕过点。
		// 退回到本地内存限流：不精确，但至少还挡得住单机暴力请求。
		return r.fallback.Allow(key)
	}
	if n == 1 {
		r.client.Expire(ctx, key, r.window)
	}
	return n <= int64(r.limit)
}
