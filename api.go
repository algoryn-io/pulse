package pulse

import (
	"context"
	"errors"
	"fmt"
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
	errNegativeErrorRate      = errors.New("pulse: threshold error rate must not be negative")
	errErrorRateAboveOne      = errors.New("pulse: threshold error rate must not be greater than 1")
	errNegativeMeanLatency    = errors.New("pulse: threshold mean latency must not be negative")
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

// Thresholds define basic pass/fail conditions for a run.
type Thresholds struct {
	ErrorRate      float64
	MaxMeanLatency time.Duration
}

// Config holds execution configuration for a test.
type Config struct {
	Phases         []Phase
	MaxConcurrency int
	Thresholds     Thresholds
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
	Total    int64
	Failed   int64
	Duration time.Duration
	Latency  LatencyStats
}

// Run validates the test definition and executes it through the engine.
func Run(test Test) (Result, error) {
	if err := validateTest(test); err != nil {
		return Result{}, err
	}

	execution := engine.New(toSchedulerPhases(test.Config.Phases), test.Scenario, test.Config.MaxConcurrency)

	metricsResult, err := execution.Run(context.Background())
	result := Result{
		Total:    metricsResult.Total,
		Failed:   metricsResult.Failed,
		Duration: metricsResult.Duration,
		Latency: LatencyStats{
			Min:  metricsResult.Latency.Min,
			Mean: metricsResult.Latency.Mean,
			Max:  metricsResult.Latency.Max,
		},
	}

	if err != nil {
		return result, errors.Join(err, evaluateThresholds(test.Config.Thresholds, result))
	}

	return result, evaluateThresholds(test.Config.Thresholds, result)
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

	if test.Config.Thresholds.ErrorRate < 0 {
		return errNegativeErrorRate
	}

	if test.Config.Thresholds.ErrorRate > 1 {
		return errErrorRateAboveOne
	}

	if test.Config.Thresholds.MaxMeanLatency < 0 {
		return errNegativeMeanLatency
	}

	return nil
}

func evaluateThresholds(thresholds Thresholds, result Result) error {
	var errs []error

	if thresholds.ErrorRate > 0 {
		var errorRate float64
		if result.Total > 0 {
			errorRate = float64(result.Failed) / float64(result.Total)
		}

		if errorRate > thresholds.ErrorRate {
			errs = append(errs, fmt.Errorf(
				"pulse: threshold error rate violated: got %.4f, limit %.4f",
				errorRate,
				thresholds.ErrorRate,
			))
		}
	}

	if thresholds.MaxMeanLatency > 0 && result.Latency.Mean > thresholds.MaxMeanLatency {
		errs = append(errs, fmt.Errorf(
			"pulse: threshold mean latency violated: got %v, limit %v",
			result.Latency.Mean,
			thresholds.MaxMeanLatency,
		))
	}

	return errors.Join(errs...)
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
