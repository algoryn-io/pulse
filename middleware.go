package pulse

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// Middleware wraps a Scenario to add behavior before or after execution.
type Middleware func(Scenario) Scenario

// ErrInjected is returned by WithErrorRate when a fault is injected.
var ErrInjected = errors.New("pulse: injected fault")

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
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if rand.Float64() < rate {
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
	return func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			if rand.Float64() < rate {
				return 0, ErrInjected
			}

			return next(ctx)
		}
	}
}
