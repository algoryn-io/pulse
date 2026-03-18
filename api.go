package pulse

import (
	"context"
	"errors"
	"time"

	"github.com/jmgo38/Pulse/engine"
	"github.com/jmgo38/Pulse/model"
	"github.com/jmgo38/Pulse/scheduler"
)

var (
	errNoPhases               = errors.New("pulse: at least one phase is required")
	errNilScenario            = errors.New("pulse: scenario must not be nil")
	errNonPositivePhase       = errors.New("pulse: phase duration must be positive")
	errNonPositiveArrivalRate = errors.New("pulse: phase arrival rate must be positive")
)

// Scenario is the user-defined workload executed by Pulse.
type Scenario func(ctx context.Context) error

// PhaseType describes how a phase should be executed.
type PhaseType = model.PhaseType

const (
	// PhaseTypeConstant represents a constant arrival-rate phase.
	PhaseTypeConstant = model.PhaseTypeConstant
)

// Phase defines the minimal execution shape for the MVP.
type Phase struct {
	Type        PhaseType
	Duration    time.Duration
	ArrivalRate int
}

// Config holds execution configuration for a test.
type Config struct {
	Phases []Phase
}

// Test is the root public input for a Pulse run.
type Test struct {
	Config   Config
	Scenario Scenario
}

// LatencyStats contains aggregate latency data.
type LatencyStats struct {
	Min  time.Duration
	Mean time.Duration
	P50  time.Duration
	P95  time.Duration
	P99  time.Duration
	Max  time.Duration
}

// Result contains the aggregated outcome of a test run.
type Result struct {
	Total   int64
	Failed  int64
	Latency LatencyStats
}

// Run validates the test definition and executes it through the engine.
func Run(test Test) (Result, error) {
	if err := validateTest(test); err != nil {
		return Result{}, err
	}

	execution := engine.New(toSchedulerPhases(test.Config.Phases), test.Scenario)

	if err := execution.Run(context.Background()); err != nil {
		return Result{}, err
	}

	return Result{}, nil
}

func validateTest(test Test) error {
	if len(test.Config.Phases) == 0 {
		return errNoPhases
	}

	if test.Scenario == nil {
		return errNilScenario
	}

	for _, phase := range test.Config.Phases {
		if phase.Duration <= 0 {
			return errNonPositivePhase
		}

		if phase.ArrivalRate <= 0 {
			return errNonPositiveArrivalRate
		}
	}

	return nil
}

func toSchedulerPhases(phases []Phase) []scheduler.Phase {
	schedulerPhases := make([]scheduler.Phase, len(phases))
	for i := range phases {
		schedulerPhases[i] = scheduler.Phase{
			Type:        phases[i].Type,
			Duration:    phases[i].Duration,
			ArrivalRate: phases[i].ArrivalRate,
		}
	}

	return schedulerPhases
}
