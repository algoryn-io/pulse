package internal

import (
	"sync"
	"time"
)

// TokenBucket is a non-blocking token bucket rate limiter.
//
// Tokens are refilled continuously at refillRate tokens per second up to
// capacity. Each call to Allow consumes one token if available and returns
// true; otherwise it returns false immediately without blocking.
//
// The caller controls the clock by passing the current time to Allow,
// which makes the bucket deterministic and easy to test.
type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	tokens     float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewTokenBucket creates a full token bucket with the given capacity and
// refill rate (tokens per second).
//
// Panics if capacity <= 0 or refillRate <= 0.
func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
	if capacity <= 0 {
		panic("tokenbucket: capacity must be positive")
	}
	if refillRate <= 0 {
		panic("tokenbucket: refillRate must be positive")
	}
	return &TokenBucket{
		capacity:   float64(capacity),
		tokens:     float64(capacity), // start full
		refillRate: refillRate,
		lastRefill: time.Time{}, // zero — initialized on first Allow call
	}
}

// NewDrainedTokenBucket is like NewTokenBucket but starts with zero tokens.
// Refill rate and capacity are unchanged; emissions pace from the first Allow
// instead of consuming an initial full bucket in one burst.
func NewDrainedTokenBucket(capacity int, refillRate float64) *TokenBucket {
	tb := NewTokenBucket(capacity, refillRate)
	tb.mu.Lock()
	tb.tokens = 0
	tb.mu.Unlock()
	return tb
}

// Allow refills tokens based on elapsed time since the last call, then
// consumes one token if available. It returns true when the request is
// allowed, false when the bucket is empty.
//
// now must be monotonically non-decreasing across calls from the same
// goroutine; passing a time earlier than the previous call is a no-op for
// the refill step (elapsed is clamped to zero).
func (tb *TokenBucket) Allow(now time.Time) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// On the very first call, anchor lastRefill without adding tokens.
	if tb.lastRefill.IsZero() {
		tb.lastRefill = now
	}

	elapsed := now.Sub(tb.lastRefill)
	if elapsed > 0 {
		tb.tokens += elapsed.Seconds() * tb.refillRate
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
		tb.lastRefill = now
	}

	if tb.tokens < 1 {
		return false
	}

	tb.tokens--
	return true
}
