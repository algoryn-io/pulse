package internal

import (
	"testing"
	"time"
)

// t0 is a fixed base time used across tests for readability.
var t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func TestTokenBucket_NewBucketStartsFull(t *testing.T) {
	tb := NewTokenBucket(5, 1)
	for i := 0; i < 5; i++ {
		if !tb.Allow(t0) {
			t.Fatalf("expected Allow to return true on call %d (bucket starts full)", i+1)
		}
	}
}

func TestTokenBucket_EmptyBucketReturnsFalse(t *testing.T) {
	tb := NewTokenBucket(2, 1)
	tb.Allow(t0) // consume 1
	tb.Allow(t0) // consume 2 — bucket empty
	if tb.Allow(t0) {
		t.Fatal("expected Allow to return false on exhausted bucket")
	}
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	// capacity=2, refillRate=2 tokens/sec
	// Drain the bucket fully, advance 0.5s → +1 token refilled.
	tb := NewTokenBucket(2, 2)
	tb.Allow(t0) // consume 1
	tb.Allow(t0) // consume 2 — empty

	// 0.5 s later → 2 * 0.5 = 1 token refilled
	if !tb.Allow(t0.Add(500 * time.Millisecond)) {
		t.Fatal("expected Allow to return true after partial refill")
	}
	// Second call at same timestamp — no additional refill
	if tb.Allow(t0.Add(500 * time.Millisecond)) {
		t.Fatal("expected Allow to return false: only 1 token was refilled")
	}
}

func TestTokenBucket_CapAtCapacity(t *testing.T) {
	// capacity=3, refillRate=10 tokens/sec
	// Drain fully, then advance 10s (would give 100 tokens without cap).
	tb := NewTokenBucket(3, 10)
	tb.Allow(t0) // consume 1
	tb.Allow(t0) // consume 2
	tb.Allow(t0) // consume 3 — empty

	// 10 s later → would refill 100 tokens, but cap is 3
	now := t0.Add(10 * time.Second)
	for i := 0; i < 3; i++ {
		if !tb.Allow(now) {
			t.Fatalf("expected Allow to return true on call %d (bucket refilled to capacity)", i+1)
		}
	}
	if tb.Allow(now) {
		t.Fatal("expected Allow to return false: bucket capped at capacity, fourth call must fail")
	}
}

func TestTokenBucket_CapacityOne(t *testing.T) {
	// Edge case: capacity of exactly 1 token.
	tb := NewTokenBucket(1, 1)
	if !tb.Allow(t0) {
		t.Fatal("expected first Allow to return true")
	}
	if tb.Allow(t0) {
		t.Fatal("expected second Allow to return false immediately")
	}
	// After 1 second exactly 1 token is refilled.
	if !tb.Allow(t0.Add(time.Second)) {
		t.Fatal("expected Allow to return true after 1s refill")
	}
}

func TestTokenBucket_ZeroElapsedNoRefill(t *testing.T) {
	// Calling Allow twice at the same timestamp after draining must not refill.
	tb := NewTokenBucket(1, 100)
	tb.Allow(t0) // consume the only token (also anchors lastRefill)
	if tb.Allow(t0) {
		t.Fatal("expected Allow to return false: same timestamp, no time elapsed")
	}
}

func TestTokenBucket_FractionalRefill(t *testing.T) {
	// refillRate=1 token/sec; 200ms elapsed → 0.2 tokens, not enough for 1.
	tb := NewTokenBucket(2, 1)
	tb.Allow(t0) // consume 1
	tb.Allow(t0) // consume 2 — empty

	if tb.Allow(t0.Add(200 * time.Millisecond)) {
		t.Fatal("expected Allow to return false: only 0.2 tokens refilled")
	}
	// 800ms more (total 1s) → 0.2 + 0.8 = 1.0 token available
	if !tb.Allow(t0.Add(time.Second)) {
		t.Fatal("expected Allow to return true after full second of refill")
	}
}

func TestTokenBucket_HighRateExact(t *testing.T) {
	// refillRate=10 tokens/sec; 100ms → 1 token exactly.
	tb := NewTokenBucket(10, 10)
	for i := 0; i < 10; i++ {
		tb.Allow(t0) // drain fully
	}
	if !tb.Allow(t0.Add(100 * time.Millisecond)) {
		t.Fatal("expected exactly 1 token after 100ms at 10 tokens/sec")
	}
	if tb.Allow(t0.Add(100 * time.Millisecond)) {
		t.Fatal("expected false: only 1 token refilled")
	}
}

// mustPanic is a helper that asserts f() panics with a message containing wantMsg.
func mustPanic(t *testing.T, wantMsg string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic(%q) but did not panic", wantMsg)
		}
		got, ok := r.(string)
		if !ok || got != wantMsg {
			t.Fatalf("expected panic(%q), got %v", wantMsg, r)
		}
	}()
	f()
}

func TestTokenBucket_NewTokenBucket_PanicsOnZeroCapacity(t *testing.T) {
	mustPanic(t, "tokenbucket: capacity must be positive", func() {
		NewTokenBucket(0, 1)
	})
}

func TestTokenBucket_NewTokenBucket_PanicsOnNegativeCapacity(t *testing.T) {
	mustPanic(t, "tokenbucket: capacity must be positive", func() {
		NewTokenBucket(-1, 1)
	})
}

func TestTokenBucket_NewTokenBucket_PanicsOnZeroRefillRate(t *testing.T) {
	mustPanic(t, "tokenbucket: refillRate must be positive", func() {
		NewTokenBucket(1, 0)
	})
}

func TestTokenBucket_NewTokenBucket_PanicsOnNegativeRefillRate(t *testing.T) {
	mustPanic(t, "tokenbucket: refillRate must be positive", func() {
		NewTokenBucket(1, -1)
	})
}
