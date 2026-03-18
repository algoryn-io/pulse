package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jmgo38/Pulse/model"
)

var (
	ErrUnsupportedPhaseType   = errors.New("scheduler: unsupported phase type")
	ErrNonPositiveArrivalRate = errors.New("scheduler: phase arrival rate must be positive")
)

// Phase contains the scheduling inputs for a single phase.
type Phase struct {
	Type        model.PhaseType
	Duration    time.Duration
	ArrivalRate int
}

// Run executes the supported scheduling strategy for a phase.
func Run(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	if phase.ArrivalRate <= 0 {
		return ErrNonPositiveArrivalRate
	}

	switch phase.Type {
	case model.PhaseTypeConstant:
		return runConstant(ctx, phase, scenario)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPhaseType, phase.Type)
	}
}

func runConstant(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	interval := time.Second / time.Duration(phase.ArrivalRate)
	ticker := time.NewTicker(interval)
	timer := time.NewTimer(phase.Duration)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			if err := scenario(ctx); err != nil {
				return err
			}
		}
	}
}
