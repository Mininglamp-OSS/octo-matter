package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/gin-gonic/gin"
)

// RateLimiter is a trivial in-memory cooldown limiter keyed by (user_id, scope).
// Zero value is not usable; build via NewRateLimiter.
type RateLimiter struct {
	cooldown  time.Duration
	last      sync.Map // map[string]time.Time
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// evictionInterval is how often stale keys are swept. We keep an entry for up
// to `cooldown + 60s` so short in-flight requests never race eviction.
const evictionInterval = 30 * time.Second

func NewRateLimiter(cooldown time.Duration) *RateLimiter {
	r := &RateLimiter{cooldown: cooldown, done: make(chan struct{})}
	r.wg.Add(1)
	go r.evictLoop()
	return r
}

// Close stops the background eviction goroutine. Safe to call multiple times.
// After Close, Allow continues to work but stale keys are no longer swept.
func (r *RateLimiter) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
		r.wg.Wait()
	})
}

// evictLoop periodically removes entries that are well past their cooldown so
// the map doesn't grow unbounded under high-cardinality keys.
func (r *RateLimiter) evictLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(evictionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-(r.cooldown + 60*time.Second))
			r.last.Range(func(k, v interface{}) bool {
				if ts, ok := v.(time.Time); ok && ts.Before(cutoff) {
					r.last.CompareAndDelete(k, v)
				}
				return true
			})
		}
	}
}

// Allow reports whether the composite key is allowed to proceed. Uses
// LoadOrStore + CompareAndSwap so concurrent callers sharing a key race
// atomically — exactly one of them will succeed.
func (r *RateLimiter) Allow(key string) bool {
	now := time.Now()
	prev, loaded := r.last.LoadOrStore(key, now)
	if !loaded {
		return true
	}
	ts, ok := prev.(time.Time)
	if !ok {
		r.last.Store(key, now)
		return true
	}
	if now.Sub(ts) < r.cooldown {
		return false
	}
	// CAS so two racing callers past the cooldown don't both succeed.
	return r.last.CompareAndSwap(key, prev, now)
}

// Middleware returns a Gin handler that rejects when Allow returns false.
// keyFn builds the composite key from the request context.
func (r *RateLimiter) Middleware(keyFn func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		k := keyFn(c)
		if k == "" {
			c.Next()
			return
		}
		if !r.Allow(k) {
			i18n.RespondError(c, http.StatusTooManyRequests, "RATE_LIMITED", i18n.KeyRateLimitCooldown,
				map[string]any{"Cooldown": int(r.cooldown.Seconds())}, nil)
			return
		}
		c.Next()
	}
}
