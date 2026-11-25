package auth

import (
	"sync"
	"time"
)

// limiter counts failures per key (client address, username) in a sliding
// window and blocks the key for a fixed period once the limit is reached.
// Memory is bounded: expired entries are swept, and when the table is still
// full the oldest entry is evicted.
type limiter struct {
	mu       sync.Mutex
	entries  map[string]*limitEntry
	limit    int
	window   time.Duration
	block    time.Duration
	capacity int
	now      func() time.Time
}

type limitEntry struct {
	failures     int
	first        time.Time
	blockedUntil time.Time
}

func newLimiter(limit int, window, block time.Duration) *limiter {
	return &limiter{entries: map[string]*limitEntry{}, limit: limit, window: window, block: block, capacity: 4096, now: time.Now}
}

// blocked reports whether the key is locked and for how long.
func (l *limiter) blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return false, 0
	}
	now := l.now()
	if now.Before(e.blockedUntil) {
		return true, e.blockedUntil.Sub(now)
	}
	return false, 0
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.Sub(e.first) > l.window && !now.Before(e.blockedUntil) {
		if !ok && len(l.entries) >= l.capacity {
			l.sweep(now)
		}
		e = &limitEntry{first: now}
		l.entries[key] = e
	}
	e.failures++
	if e.failures >= l.limit {
		e.blockedUntil = now.Add(l.block)
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *limiter) sweep(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, e := range l.entries {
		if now.Sub(e.first) > l.window && !now.Before(e.blockedUntil) {
			delete(l.entries, k)
			continue
		}
		if oldestKey == "" || e.first.Before(oldest) {
			oldestKey, oldest = k, e.first
		}
	}
	if len(l.entries) >= l.capacity && oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}
