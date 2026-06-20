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
	phases          []scheduler.Phase
	scenario        func(context.Context) (int, error)
	maxConcurrency  int
	saturation      SaturationPolicy
	reportInterval  time.Duration
	onLiveSnapshot  func(metrics.Snapshot)
}

// Options contains execution settings for Engine.
type Options struct {
	MaxConcurrency int
	Saturation     SaturationPolicy
	ReportInterval time.Duration
	// OnLiveSnapshot, when non-nil, is called at the end of each reporting
	// interval with the metrics observed during that window. It is invoked
	// from a background goroutine and must not block. Only active when
	// ReportInterval > 0.
	OnLiveSnapshot func(metrics.Snapshot)
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
	return NewWithOptions(phases, scenario, Options{
		MaxConcurrency: maxConcurrency,
		Saturation:     saturation,
	})
}

// NewWithOptions creates an engine with explicit execution settings.
func NewWithOptions(phases []scheduler.Phase, scenario func(context.Context) (int, error), options Options) *Engine {
	return &Engine{
		phases:         phases,
		scenario:       scenario,
		maxConcurrency: options.MaxConcurrency,
		saturation:     options.Saturation,
		reportInterval: options.ReportInterval,
		onLiveSnapshot: options.OnLiveSnapshot,
	}
}

// Run executes each phase in sequence through the scheduler.
// Scenario errors are recorded in metrics and do not stop the run.
// A non-nil error indicates scheduler failure or context cancellation.
func (e *Engine) Run(ctx context.Context) (metrics.Result, error) {
	aggregator := metrics.NewAggregator()
	defer aggregator.Close()
	startedAt := time.Now()
	snapshots := newSnapshotCollector(startedAt, e.reportInterval)
	defer snapshots.close()
	limiter := internal.NewLimiter(e.maxConcurrency)

	var wg sync.WaitGroup
	var scheduled atomic.Int64
	var started atomic.Int64
	var dropped atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64

	wrappedScenario := func(ctx context.Context) error {
		now := time.Now()
		scheduled.Add(1)
		snapshots.recordScheduled(now)

		switch e.saturation {
		case SaturationPolicyDrop:
			if !limiter.TryAcquire() {
				dropped.Add(1)
				snapshots.recordDropped(now)
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
		snapshots.recordStarted(time.Now(), currentActive)
		go func() {
			defer wg.Done()
			defer limiter.Release()
			defer active.Add(-1)

			executionStartedAt := time.Now()
			statusCode, err := e.scenario(ctx)
			latency := time.Since(executionStartedAt)
			aggregator.Record(latency, statusCode, err)
			snapshots.recordCompleted(time.Now(), latency, statusCode, err)
		}()

		return nil
	}

	// Start a background goroutine that emits live snapshots at each reporting
	// interval. This lets callers (e.g. a dashboard server) observe metrics as
	// the run progresses without waiting for completion.
	if e.onLiveSnapshot != nil && e.reportInterval > 0 {
		liveCtx, liveCancel := context.WithCancel(ctx)
		defer liveCancel()
		go func() {
			ticker := time.NewTicker(e.reportInterval)
			defer ticker.Stop()
			for {
				select {
				case <-liveCtx.Done():
					return
				case t := <-ticker.C:
					snap := snapshots.liveSnapshot(t)
					if snap.Duration > 0 {
						e.onLiveSnapshot(snap)
					}
				}
			}
		}()
	}

	for _, phase := range e.phases {
		if err := scheduler.Run(ctx, phase, wrappedScenario); err != nil {
			wg.Wait()
			return withLoadStats(aggregator.Result(time.Since(startedAt)), snapshots, &scheduled, &started, &dropped, &maxActive), err
		}
	}

	wg.Wait()
	return withLoadStats(aggregator.Result(time.Since(startedAt)), snapshots, &scheduled, &started, &dropped, &maxActive), nil
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
	snapshots *snapshotCollector,
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
	result.Snapshots = snapshots.snapshots(result.Duration)
	return result
}
