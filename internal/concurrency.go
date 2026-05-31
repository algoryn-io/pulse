package internal

import "context"

// Limiter bounds concurrent executions using a buffered channel semaphore.
type Limiter struct {
	slots chan struct{}
}

// NewLimiter creates a limiter with the provided capacity.
func NewLimiter(limit int) *Limiter {
	if limit <= 0 {
		limit = 1
	}

	return &Limiter{
		slots: make(chan struct{}, limit),
	}
}

// Acquire reserves an execution slot or returns when the context is canceled.
func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire reserves an execution slot without blocking. It returns false
// when all slots are in use.
func (l *Limiter) TryAcquire() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees an execution slot.
func (l *Limiter) Release() {
	<-l.slots
}
