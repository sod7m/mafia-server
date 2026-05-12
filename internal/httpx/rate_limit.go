package httpx

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type FixedWindowLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]bucket
}

type bucket struct {
	count   int
	resetAt time.Time
}

func NewFixedWindowLimiter(limit int, window time.Duration) *FixedWindowLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}

	return &FixedWindowLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]bucket),
	}
}

func (l *FixedWindowLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Evict expired buckets to prevent unbounded memory growth.
	for k, b := range l.buckets {
		if now.After(b.resetAt) {
			delete(l.buckets, k)
		}
	}

	entry, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = bucket{
			count:   1,
			resetAt: now.Add(l.window),
		}
		return true
	}

	if entry.count >= l.limit {
		return false
	}

	entry.count++
	l.buckets[key] = entry
	return true
}

func RateLimitKeyByIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}

	return strings.TrimSpace(r.RemoteAddr)
}
