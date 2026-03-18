package engine

import (
	"context"
	"time"

	"github.com/jmgo38/Pulse/metrics"
	"github.com/jmgo38/Pulse/scheduler"
)

// Engine executes a test definition.
type Engine struct {
	phases   []scheduler.Phase
	scenario func(context.Context) error
}

// New creates an engine for the given execution inputs.
func New(phases []scheduler.Phase, scenario func(context.Context) error) *Engine {
	return &Engine{
		phases:   phases,
		scenario: scenario,
	}
}

// Run executes each phase in sequence through the scheduler.
func (e *Engine) Run(ctx context.Context) (metrics.Result, error) {
	aggregator := metrics.NewAggregator()
	startedAt := time.Now()

	wrappedScenario := func(ctx context.Context) error {
		executionStartedAt := time.Now()
		err := e.scenario(ctx)
		aggregator.Record(time.Since(executionStartedAt), err != nil)
		return err
	}

	for _, phase := range e.phases {
		if err := scheduler.Run(ctx, phase, wrappedScenario); err != nil {
			return aggregator.Result(time.Since(startedAt)), err
		}
	}

	return aggregator.Result(time.Since(startedAt)), nil
}
