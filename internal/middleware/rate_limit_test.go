package middleware

import (
	"sync"
	"testing"
	"time"
)

// TestRateLimiter_AllowCooldown checks the basic accept/reject pattern under a
// short cooldown.
func TestRateLimiter_AllowCooldown(t *testing.T) {
	rl := NewRateLimiter(50 * time.Millisecond)
	defer rl.Close()
	if !rl.Allow("k") {
		t.Fatalf("first call should be allowed")
	}
	if rl.Allow("k") {
		t.Fatalf("second call within cooldown should be rejected")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("k") {
		t.Fatalf("after cooldown call should be allowed")
	}
}

// TestRateLimiter_Close_StopsEvictLoop verifies Close terminates the
// background eviction goroutine. It should return promptly and be safe to call
// multiple times.
func TestRateLimiter_Close_StopsEvictLoop(t *testing.T) {
	rl := NewRateLimiter(time.Hour)
	done := make(chan struct{})
	go func() {
		rl.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Close blocked — evict goroutine never exited")
	}
	// Close is idempotent.
	rl.Close()
}

// TestRateLimiter_AllowAfterCloseDoesNotPanic guards that Close does not break
// in-flight Allow() calls (memory eviction stops, but the limiter map remains
// usable).
func TestRateLimiter_AllowAfterCloseDoesNotPanic(t *testing.T) {
	rl := NewRateLimiter(time.Hour)
	rl.Close()
	if !rl.Allow("k") {
		t.Fatalf("Allow should still work after Close")
	}
}

// TestRateLimiter_AllowConcurrent runs many goroutines through Allow with a
// shared key and asserts exactly one wins. Uses a long cooldown so no goroutine
// can ever fall outside the window, regardless of scheduler jitter on slow CI.
// Catches CAS regressions under -race.
func TestRateLimiter_AllowConcurrent(t *testing.T) {
	rl := NewRateLimiter(time.Hour)
	defer rl.Close()
	var wg sync.WaitGroup
	var allowed int64
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow("shared") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("expected exactly 1 winner under concurrent Allow, got %d", allowed)
	}
}
