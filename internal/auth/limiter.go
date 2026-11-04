package auth

import (
	"sync"
	"time"
)

// limiter 是进程内的失败计数。单实例够用；多实例部署时应换成共享存储，
// 但 mdserve 的定位是"一个人分享一个目录"，不值得引入 Redis。
type limiter struct {
	mu       sync.Mutex
	entries  map[string]*attempt
	attempts int
	window   time.Duration
	lockout  time.Duration
}

type attempt struct {
	fails  int
	first  time.Time
	locked time.Time
}

func newLimiter(attempts int, window, lockout time.Duration) *limiter {
	return &limiter{
		entries:  map[string]*attempt{},
		attempts: attempts,
		window:   window,
		lockout:  lockout,
	}
}

// allow 返回是否放行，以及还需要等待多久。
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if now.Before(e.locked) {
		return false, e.locked.Sub(now)
	}
	if now.Sub(e.first) > l.window {
		delete(l.entries, key)
		return true, 0
	}
	if e.fails >= l.attempts {
		e.locked = now.Add(l.lockout)
		e.fails = 0
		e.first = now
		return false, l.lockout
	}
	return true, 0
}

func (l *limiter) fail(key string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || now.Sub(e.first) > l.window {
		l.entries[key] = &attempt{fails: 1, first: now}
		l.sweepLocked(now)
		return
	}
	e.fails++
	if e.fails >= l.attempts {
		e.locked = now.Add(l.lockout)
		e.fails = 0
		e.first = now
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// sweepLocked 顺手清理过期条目，避免长期运行后 map 无限增长。
func (l *limiter) sweepLocked(now time.Time) {
	if len(l.entries) < 4096 {
		return
	}
	for k, e := range l.entries {
		if now.Sub(e.first) > l.window && now.After(e.locked) {
			delete(l.entries, k)
		}
	}
}
