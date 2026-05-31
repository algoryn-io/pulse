package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"algoryn.io/pulse/internal"
	"algoryn.io/pulse/metrics"
	"algoryn.io/pulse/scheduler"
)

// SaturationPolicy controls what happens when all execution slots are in use.
type SaturationPolicy string

const (
	// SaturationPolicyDrop preserves the configured arrival rate by discarding
	// arrivals that cannot start immediately.
	SaturationPolicyDrop SaturationPolicy = "drop"
	// SaturationPolicyBlock waits for capacity, applying backpressure to the
	// scheduler. This preserves the behavior of earlier Pulse versions.
	SaturationPolicyBlock SaturationPolicy = "block"
)

// Engine executes a test definition.
type Engine struct {
	phases         []scheduler.Phase
	scenario       func(context.Context) (int, error)
	maxConcurrency int
	saturation     SaturationPolicy
}

// New creates an engine for the given execution inputs.
// It retains the legacy blocking behavior. Use NewWithSaturationPolicy for an
// explicit policy.
func New(phases []scheduler.Phase, scenario func(context.Context) (int, error), maxConcurrency int) *Engine {
	return NewWithSaturationPolicy(phases, scenario, maxConcurrency, SaturationPolicyBlock)
}

// NewWithSaturationPolicy creates an engine with an explicit saturation policy.
func NewWithSaturationPolicy(
	phases []scheduler.Phase,
	scenario func(context.Context) (int, error),
	maxConcurrency int,
	saturation SaturationPolicy,
) *Engine {
	return &Engine{
		phases:         phases,
		scenario:       scenario,
		maxConcurrency: maxConcurrency,
		saturation:     saturation,
	}
}

// Run executes each phase in sequence through the scheduler.
// Scenario errors are recorded in metrics and do not stop the run.
// A non-nil error indicates scheduler failure or context cancellation.
func (e *Engine) Run(ctx context.Context) (metrics.Result, error) {
	aggregator := metrics.NewAggregator()
	defer aggregator.Close()
	startedAt := time.Now()
	limiter := internal.NewLimiter(e.maxConcurrency)

	var wg sync.WaitGroup
	var scheduled atomic.Int64
	var started atomic.Int64
	var dropped atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64

	wrappedScenario := func(ctx context.Context) error {
		scheduled.Add(1)

		switch e.saturation {
		case SaturationPolicyDrop:
			if !limiter.TryAcquire() {
				dropped.Add(1)
				return nil
			}
		default:
			if err := limiter.Acquire(ctx); err != nil {
				return err
			}
		}

		wg.Add(1)
		started.Add(1)
		currentActive := active.Add(1)
		updateMax(&maxActive, currentActive)
		go func() {
			defer wg.Done()
			defer limiter.Release()
			defer active.Add(-1)

			executionStartedAt := time.Now()
			statusCode, err := e.scenario(ctx)
			aggregator.Record(time.Since(executionStartedAt), statusCode, err)
		}()

		return nil
	}

	for _, phase := range e.phases {
		if err := scheduler.Run(ctx, phase, wrappedScenario); err != nil {
			wg.Wait()
			return withLoadStats(aggregator.Result(time.Since(startedAt)), &scheduled, &started, &dropped, &maxActive), err
		}
	}

	wg.Wait()
	return withLoadStats(aggregator.Result(time.Since(startedAt)), &scheduled, &started, &dropped, &maxActive), nil
}

func updateMax(max *atomic.Int64, candidate int64) {
	for {
		current := max.Load()
		if candidate <= current || max.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func withLoadStats(
	result metrics.Result,
	scheduled *atomic.Int64,
	started *atomic.Int64,
	dropped *atomic.Int64,
	maxActive *atomic.Int64,
) metrics.Result {
	result.Scheduled = scheduled.Load()
	result.Started = started.Load()
	result.Dropped = dropped.Load()
	if result.Scheduled > 0 {
		result.DroppedRate = float64(result.Dropped) / float64(result.Scheduled)
	}
	result.Completed = result.Total
	result.MaxActive = maxActive.Load()
	return result
}
