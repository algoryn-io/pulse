package pulse

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open and
// requests are being rejected to simulate cascading failures.
var ErrCircuitOpen = errors.New("pulse: circuit open")

// cbState represents the state of the circuit breaker.
type cbState int

const (
	cbClosed cbState = iota
	cbOpen
	cbHalfOpen
)

// circuitBreaker holds the state for a single WithCircuitBreaker instance.
type circuitBreaker struct {
	mu          sync.Mutex
	state       cbState
	failures    int
	successes   int
	total       int
	windowStart time.Time
	openedAt    time.Time
	threshold   float64
	window      time.Duration
	timeout     time.Duration
}

func newCircuitBreaker(threshold float64, window, timeout time.Duration) *circuitBreaker {
	return &circuitBreaker{
		state:       cbClosed,
		threshold:   threshold,
		window:      window,
		timeout:     timeout,
		windowStart: time.Now(),
	}
}

// allow reports whether the request should proceed.
// Must be called with cb.mu held.
func (cb *circuitBreaker) allow(now time.Time) bool {
	switch cb.state {
	case cbOpen:
		if now.Sub(cb.openedAt) >= cb.timeout {
			cb.state = cbHalfOpen
			return true
		}
		return false
	case cbHalfOpen:
		return true
	default:
		return true
	}
}

// record records the result of a request and transitions state if needed.
// Must be called with cb.mu held.
func (cb *circuitBreaker) record(success bool, now time.Time) {
	if now.Sub(cb.windowStart) >= cb.window {
		cb.failures = 0
		cb.successes = 0
		cb.total = 0
		cb.windowStart = now
	}

	cb.total++
	if success {
		cb.successes++
	} else {
		cb.failures++
	}

	switch cb.state {
	case cbHalfOpen:
		if success {
			cb.state = cbClosed
			cb.failures = 0
			cb.successes = 0
			cb.total = 0
			cb.windowStart = now
		} else {
			cb.state = cbOpen
			cb.openedAt = now
		}
	case cbClosed:
		if cb.total >= 5 {
			rate := float64(cb.failures) / float64(cb.total)
			if rate >= cb.threshold {
				cb.state = cbOpen
				cb.openedAt = now
			}
		}
	}
}

// WithCircuitBreaker returns a Middleware that simulates cascading failures
// by opening a circuit when the error rate within a time window exceeds
// the threshold.
func WithCircuitBreaker(threshold float64, window, timeout time.Duration) Middleware {
	cb := newCircuitBreaker(threshold, window, timeout)

	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			now := time.Now()

			cb.mu.Lock()
			allowed := cb.allow(now)
			cb.mu.Unlock()

			if !allowed {
				return 0, ErrCircuitOpen
			}

			status, err := next(ctx)

			cb.mu.Lock()
			cb.record(err == nil, time.Now())
			cb.mu.Unlock()

			return status, err
		}
	}
}
