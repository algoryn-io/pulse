package internal

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterAcquireBlocksUntilRelease(t *testing.T) {
	limiter := NewLimiter(1)

	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		if err := limiter.Acquire(context.Background()); err != nil {
			t.Errorf("expected no error, got %v", err)
			return
		}
		limiter.Release()
	}()

	select {
	case <-acquired:
		t.Fatal("expected acquire to block while limiter is full")
	case <-time.After(30 * time.Millisecond):
	}

	limiter.Release()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for acquire after release")
	}
}

func TestLimiterAcquireRespectsContextCancellation(t *testing.T) {
	limiter := NewLimiter(1)

	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := limiter.Acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected %v, got %v", context.DeadlineExceeded, err)
	}

	limiter.Release()
}

func TestLimiterTryAcquireReturnsImmediatelyWhenFull(t *testing.T) {
	limiter := NewLimiter(1)
	if !limiter.TryAcquire() {
		t.Fatal("expected first TryAcquire to reserve a slot")
	}
	if limiter.TryAcquire() {
		t.Fatal("expected second TryAcquire to report a full limiter")
	}

	limiter.Release()
	if !limiter.TryAcquire() {
		t.Fatal("expected TryAcquire after Release to reserve a slot")
	}
	limiter.Release()
}
