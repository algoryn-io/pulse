package pulse

import (
	"context"
	"errors"
	"math"
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
	mu            sync.Mutex
	state         cbState
	failures      int
	successes     int
	total         int
	windowStart   time.Time
	openedAt      time.Time
	threshold     float64
	window        time.Duration
	timeout       time.Duration
	probeInFlight bool // true while exactly one probe request is executing in half-open state
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
//
// In half-open state only one probe request is permitted at a time; concurrent
// callers receive false until the probe completes and record() transitions the
// circuit to closed or back to open.
func (cb *circuitBreaker) allow(now time.Time) bool {
	switch cb.state {
	case cbOpen:
		if now.Sub(cb.openedAt) >= cb.timeout {
			cb.state = cbHalfOpen
			cb.probeInFlight = true
			return true
		}
		return false
	case cbHalfOpen:
		// Gate all concurrent arrivals: only the single probe already in flight
		// is allowed. Others are rejected until the probe resolves.
		if cb.probeInFlight {
			return false
		}
		cb.probeInFlight = true
		return true
	default: // cbClosed
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
		// Release the probe slot before transitioning so that if the circuit
		// re-opens the next timeout cycle can issue a new probe.
		cb.probeInFlight = false
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
//
// When the circuit opens it stays open for timeout; after that it transitions
// to half-open and allows exactly one probe request through. If the probe
// succeeds the circuit closes; if it fails the circuit re-opens and the
// timeout resets. Concurrent arrivals in half-open state are rejected with
// ErrCircuitOpen until the probe resolves.
func WithCircuitBreaker(threshold float64, window, timeout time.Duration) Middleware {
	if math.IsNaN(threshold) || threshold < 0 || threshold > 1 {
		panic("pulse: circuit breaker threshold must be between 0 and 1")
	}
	if window <= 0 {
		panic("pulse: circuit breaker window must be positive")
	}
	if timeout <= 0 {
		panic("pulse: circuit breaker timeout must be positive")
	}
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
