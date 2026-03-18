package engine

import (
	"context"

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
func (e *Engine) Run(ctx context.Context) error {
	for _, phase := range e.phases {
		if err := scheduler.Run(ctx, phase, e.scenario); err != nil {
			return err
		}
	}

	return nil
}
