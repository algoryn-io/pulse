package engine

import (
	"context"
	"sync"
	"time"

	"github.com/jmgo38/Pulse/internal"
	"github.com/jmgo38/Pulse/metrics"
	"github.com/jmgo38/Pulse/scheduler"
	"github.com/jmgo38/Pulse/transport"
)

// Engine executes a test definition.
type Engine struct {
	phases         []scheduler.Phase
	scenario       func(context.Context) error
	maxConcurrency int
}

// New creates an engine for the given execution inputs.
func New(phases []scheduler.Phase, scenario func(context.Context) error, maxConcurrency int) *Engine {
	return &Engine{
		phases:         phases,
		scenario:       scenario,
		maxConcurrency: maxConcurrency,
	}
}

// Run executes each phase in sequence through the scheduler.
func (e *Engine) Run(ctx context.Context) (metrics.Result, error) {
	aggregator := metrics.NewAggregator()
	startedAt := time.Now()
	limiter := internal.NewLimiter(e.maxConcurrency)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	setFirstErr := func(err error) {
		if err == nil {
			return
		}

		errMu.Lock()
		defer errMu.Unlock()
		if firstErr != nil {
			return
		}

		firstErr = err
		cancel()
	}

	getFirstErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return firstErr
	}

	wrappedScenario := func(ctx context.Context) error {
		if err := limiter.Acquire(ctx); err != nil {
			return err
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer limiter.Release()

			var statusCode int
			ctx = transport.ContextWithResponseStatus(ctx, &statusCode)

			executionStartedAt := time.Now()
			err := e.scenario(ctx)
			aggregator.Record(time.Since(executionStartedAt), statusCode, err)
			setFirstErr(err)
		}()

		return nil
	}

	for _, phase := range e.phases {
		if err := scheduler.Run(runCtx, phase, wrappedScenario); err != nil {
			wg.Wait()
			if scenarioErr := getFirstErr(); scenarioErr != nil {
				return aggregator.Result(time.Since(startedAt)), scenarioErr
			}

			return aggregator.Result(time.Since(startedAt)), err
		}
	}

	wg.Wait()
	if err := getFirstErr(); err != nil {
		return aggregator.Result(time.Since(startedAt)), err
	}

	return aggregator.Result(time.Since(startedAt)), nil
}
