package pulse

import (
	"context"
	"fmt"
)

// Sequence returns a Scenario that runs each step in order.
// It stops and returns immediately if any step returns a non-nil error.
// On success it returns the status code of the last step.
// Panics if steps is empty.
func Sequence(steps ...Scenario) Scenario {
	if len(steps) == 0 {
		panic("pulse: Sequence requires at least one step")
	}
	return func(ctx context.Context) (int, error) {
		var code int
		for _, step := range steps {
			var err error
			code, err = step(ctx)
			if err != nil {
				return code, err
			}
		}
		return code, nil
	}
}

// Step is a named scenario used with Flow.
type Step struct {
	Name string
	Do   Scenario
}

// Flow returns a Scenario that runs each Step in sequence.
// If a step returns a non-nil error, the flow stops and wraps the error with
// the step name so failures are easy to identify in result error maps.
// Panics if steps is empty or any Step.Do is nil.
func Flow(steps ...Step) Scenario {
	if len(steps) == 0 {
		panic("pulse: Flow requires at least one step")
	}
	for i, s := range steps {
		if s.Do == nil {
			panic(fmt.Sprintf("pulse: Flow step %d (%q) has a nil Do function", i, s.Name))
		}
	}
	return func(ctx context.Context) (int, error) {
		var code int
		for _, s := range steps {
			var err error
			code, err = s.Do(ctx)
			if err != nil {
				if s.Name != "" {
					return code, fmt.Errorf("%s: %w", s.Name, err)
				}
				return code, err
			}
		}
		return code, nil
	}
}
