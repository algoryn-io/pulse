package pulse

import (
	"context"
	"errors"
	"math"
	"time"
)

// Middleware wraps a Scenario to add behavior before or after execution.
type Middleware func(Scenario) Scenario

// ErrInjected is returned by WithErrorRate when a fault is injected.
var (
	ErrInjected = errors.New("pulse: injected fault")
	// ErrBulkheadFull is returned by WithBulkhead when the concurrency
	// limit is reached and the context expires before a slot opens.
	ErrBulkheadFull = errors.New("pulse: bulkhead full")
)

// Chain applies middlewares to a Scenario in order.
// The first middleware is the outermost wrapper.
func Chain(middlewares ...Middleware) func(Scenario) Scenario {
	return func(s Scenario) Scenario {
		for i := len(middlewares) - 1; i >= 0; i-- {
			s = middlewares[i](s)
		}
		return s
	}
}

// Apply wraps a Scenario with the given middlewares.
func Apply(scenario Scenario, middlewares ...Middleware) Scenario {
	return Chain(middlewares...)(scenario)
}

// WithLatency returns a Middleware that adds artificial latency to
// a percentage of requests.
func WithLatency(d time.Duration, rate float64) Middleware {
	validateDuration("latency", d)
	validateRate(rate)
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if randomFloat64() < rate {
				timer := time.NewTimer(d)
				defer timer.Stop()

				select {
				case <-timer.C:
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}

			return next(ctx)
		}
	}
}

// WithErrorRate returns a Middleware that causes a percentage of requests
// to fail without calling the underlying Scenario.
func WithErrorRate(rate float64) Middleware {
	validateRate(rate)
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if randomFloat64() < rate {
				return 0, ErrInjected
			}

			return next(ctx)
		}
	}
}

// WithJitter returns a Middleware that adds random latency between
// min and max to a percentage of requests.
func WithJitter(min, max time.Duration, rate float64) Middleware {
	validateDuration("minimum jitter", min)
	validateDuration("maximum jitter", max)
	validateRate(rate)
	if max < min {
		panic("pulse: maximum jitter must not be less than minimum jitter")
	}
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if randomFloat64() < rate {
				d := min
				if max > min {
					d = min + time.Duration(randomInt64N(int64(max-min)))
				}

				timer := time.NewTimer(d)
				defer timer.Stop()

				select {
				case <-timer.C:
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			return next(ctx)
		}
	}
}

// WithTimeout returns a Middleware that enforces a maximum duration
// for each scenario execution.
func WithTimeout(d time.Duration) Middleware {
	if d <= 0 {
		panic("pulse: timeout must be positive")
	}
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx)
		}
	}
}

// WithStatusCode returns a Middleware that forces a specific HTTP status
// code to be returned for a percentage of requests, without calling
// the underlying Scenario.
func WithStatusCode(code int, rate float64) Middleware {
	validateRate(rate)
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if randomFloat64() < rate {
				return code, ErrInjected
			}
			return next(ctx)
		}
	}
}

// WithRetry returns a Middleware that retries a failed scenario
// up to n times with a fixed backoff between attempts.
//
// Retries are skipped immediately when the context is already canceled or
// expired after a failed attempt, returning the context error. This prevents
// spurious retries when a deadline fires mid-run.
func WithRetry(n int, backoff time.Duration) Middleware {
	if n < 0 {
		panic("pulse: retry count must not be negative")
	}
	validateDuration("retry backoff", backoff)
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			var (
				status int
				err    error
			)
			for i := 0; i <= n; i++ {
				status, err = next(ctx)
				if err == nil {
					return status, nil
				}
				// Do not retry when the context has been canceled or exceeded its
				// deadline — the error is not transient and further attempts will
				// fail immediately with the same context state.
				if ctx.Err() != nil {
					return status, ctx.Err()
				}
				if i < n {
					timer := time.NewTimer(backoff)
					select {
					case <-timer.C:
						// timer fired; continue to next attempt
					case <-ctx.Done():
						timer.Stop()
						return 0, ctx.Err()
					}
				}
			}
			return status, err
		}
	}
}

// WithBulkhead returns a Middleware that limits the number of concurrent
// executions of a scenario.
func WithBulkhead(maxConcurrent int) Middleware {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	sem := make(chan struct{}, maxConcurrent)

	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return 0, ErrBulkheadFull
			}
			defer func() { <-sem }()
			return next(ctx)
		}
	}
}

func validateRate(rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic("pulse: middleware rate must be between 0 and 1")
	}
}

func validateDuration(name string, d time.Duration) {
	if d < 0 {
		panic("pulse: " + name + " must not be negative")
	}
}
