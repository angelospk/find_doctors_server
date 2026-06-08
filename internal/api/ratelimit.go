package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiterConfig drives the per-IP token-bucket limiter.
type RateLimiterConfig struct {
	// RPS is the steady-state requests-per-second per client IP.
	RPS float64
	// Burst is the maximum tokens accumulated per IP.
	Burst int
	// BypassPaths skip the limiter entirely (e.g. k8s probes).
	BypassPaths []string
	// TrustProxyHeader honors X-Forwarded-For only if set to true. Otherwise
	// the remote socket address is used (default — safest for direct exposure).
	TrustProxyHeader bool
}

// RateLimiter is a goroutine-safe per-IP rate limiter using x/time/rate.
type RateLimiter struct {
	cfg     RateLimiterConfig
	mu      sync.RWMutex
	buckets map[string]*ipBucket
}

type ipBucket struct {
	lim  *rate.Limiter
	used time.Time
}

// NewRateLimiter constructs a RateLimiter with periodic eviction of idle IPs.
// The eviction goroutine stops when ctx is cancelled (pass the server's
// shutdown context so it doesn't leak).
func NewRateLimiter(ctx context.Context, cfg RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{cfg: cfg, buckets: make(map[string]*ipBucket)}
	go rl.evictLoop(ctx)
	return rl
}

func (rl *RateLimiter) evictLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-30 * time.Minute)
			// Snapshot stale keys with a short read lock, then delete.
			var stale []string
			rl.mu.RLock()
			for k, b := range rl.buckets {
				if b.used.Before(cutoff) {
					stale = append(stale, k)
				}
			}
			rl.mu.RUnlock()
			if len(stale) > 0 {
				rl.mu.Lock()
				for _, k := range stale {
					if b, ok := rl.buckets[k]; ok && b.used.Before(cutoff) {
						delete(rl.buckets, k)
					}
				}
				rl.mu.Unlock()
			}
		}
	}
}

func (rl *RateLimiter) bucket(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &ipBucket{lim: rate.NewLimiter(rate.Limit(rl.cfg.RPS), rl.cfg.Burst)}
		rl.buckets[ip] = b
	}
	b.used = time.Now()
	return b.lim
}

func (rl *RateLimiter) clientIP(r *http.Request) string {
	if rl.cfg.TrustProxyHeader {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// XFF can be "client, proxy1, proxy2" — first hop is the real client.
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (rl *RateLimiter) bypass(path string) bool {
	for _, p := range rl.cfg.BypassPaths {
		if path == p {
			return true
		}
	}
	return false
}

// Middleware returns the chained handler.
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl.bypass(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ip := rl.clientIP(r)
			if !rl.bucket(ip).Allow() {
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
