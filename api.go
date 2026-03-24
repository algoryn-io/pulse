package pulse

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	errInvalidRampEndpoints   = errors.New("pulse: ramp phase from and to must be positive")
	errInvalidStepConfig      = errors.New("pulse: step phase requires positive From, To and Steps")
	errInvalidSpikeConfig     = errors.New("pulse: spike phase requires positive From, To and SpikeDuration")
	errEmptyPhaseType         = errors.New("pulse: phase type is required")
	errUnsupportedPhaseType   = errors.New("pulse: unsupported phase type")
	errNegativeErrorRate      = errors.New("pulse: threshold error rate must not be negative")
	errErrorRateAboveOne      = errors.New("pulse: threshold error rate must not be greater than 1")
	errNegativeMeanLatency    = errors.New("pulse: threshold mean latency must not be negative")
	errNegativeP95Latency     = errors.New("pulse: threshold p95 latency must not be negative")
	errNegativeP99Latency     = errors.New("pulse: threshold p99 latency must not be negative")
)

// Scenario is the user-defined workload executed by Pulse.
// The int is an HTTP or application status code; use 0 when not applicable.
type Scenario func(ctx context.Context) (statusCode int, err error)

// PhaseType describes how a phase should be executed.
type PhaseType = model.PhaseType

const (
	// PhaseTypeConstant represents a constant arrival-rate phase.
	PhaseTypeConstant = model.PhaseTypeConstant
	// PhaseTypeRamp represents a linear ramp between two arrival rates.
	PhaseTypeRamp = model.PhaseTypeRamp
	// PhaseTypeStep represents discrete steps between two arrival rates.
	PhaseTypeStep = model.PhaseTypeStep
	// PhaseTypeSpike represents a temporary spike from a base rate to a peak rate.
	PhaseTypeSpike = model.PhaseTypeSpike
)

// Phase defines the minimal execution shape for the MVP.
type Phase struct {
	Type        PhaseType
	Duration    time.Duration
	ArrivalRate int
	// From and To are the arrival rates (per second) at the start and end of a ramp or step phase.
	From int
	To   int
	// Steps is the number of discrete rate levels for PhaseTypeStep.
	Steps int
	// SpikeAt is when the spike starts; 0 means immediately.
	SpikeAt time.Duration
	// SpikeDuration is how long the spike lasts.
	SpikeDuration time.Duration
}

// IsConstant reports whether p is a constant arrival-rate phase.
func (p Phase) IsConstant() bool {
	return p.Type == PhaseTypeConstant
}

// IsRamp reports whether p is a linear ramp phase.
func (p Phase) IsRamp() bool {
	return p.Type == PhaseTypeRamp
}

// IsStep reports whether p is a stepped ramp phase.
func (p Phase) IsStep() bool {
	return p.Type == PhaseTypeStep
}

// IsSpike reports whether p is a spike phase.
func (p Phase) IsSpike() bool {
	return p.Type == PhaseTypeSpike
}

// Thresholds define basic pass/fail conditions for a run.
type Thresholds struct {
	ErrorRate      float64
	MaxMeanLatency time.Duration
	MaxP95Latency  time.Duration
	MaxP99Latency  time.Duration
}

// Config holds execution configuration for a test.
type Config struct {
	Phases         []Phase
	MaxConcurrency int
	Thresholds     Thresholds
	OnResult       ResultHook // optional; nil means no-op
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

// ThresholdOutcome records whether a configured threshold passed for a run.
type ThresholdOutcome struct {
	Pass        bool
	Description string
}

// Result contains the aggregated outcome of a test run.
type Result struct {
	Total             int64
	Failed            int64
	Duration          time.Duration
	RPS               float64
	Latency           LatencyStats
	StatusCounts      map[int]int64
	ErrorCounts       map[string]int64
	ThresholdOutcomes []ThresholdOutcome `json:"-"`
}

// ResultHook is an optional callback invoked after a test run completes.
// result contains the full aggregated metrics.
// passed is true when all configured thresholds were met.
type ResultHook func(result Result, passed bool)

// Run validates the test definition and executes it through the engine.
func Run(test Test) (Result, error) {
	if err := validateTest(test); err != nil {
		return Result{}, err
	}

	execution := engine.New(toSchedulerPhases(test.Config.Phases), test.Scenario, test.Config.MaxConcurrency)

	metricsResult, err := execution.Run(context.Background())
	result := Result{
		Total:        metricsResult.Total,
		Failed:       metricsResult.Failed,
		Duration:     metricsResult.Duration,
		RPS:          metricsResult.RPS,
		StatusCounts: metricsResult.StatusCounts,
		ErrorCounts:  metricsResult.ErrorCounts,
		Latency: LatencyStats{
			Min:  metricsResult.Latency.Min,
			Mean: metricsResult.Latency.Mean,
			P50:  metricsResult.Latency.P50,
			P95:  metricsResult.Latency.P95,
			P99:  metricsResult.Latency.P99,
			Max:  metricsResult.Latency.Max,
		},
	}

	outcomes, threshErr := evaluateThresholds(test.Config.Thresholds, result)
	result.ThresholdOutcomes = outcomes

	if test.Config.OnResult != nil {
		passed := threshErr == nil
		test.Config.OnResult(result, passed)
	}

	if err != nil {
		return result, errors.Join(err, threshErr)
	}

	return result, threshErr
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

		pt := PhaseType(strings.TrimSpace(string(phase.Type)))
		if pt == "" {
			return errEmptyPhaseType
		}

		p := Phase{Type: pt}
		switch {
		case p.IsRamp():
			if phase.From <= 0 || phase.To <= 0 {
				return errInvalidRampEndpoints
			}
		case p.IsConstant():
			if phase.ArrivalRate <= 0 {
				return errNonPositiveArrivalRate
			}
		case p.IsStep():
			if phase.From <= 0 || phase.To <= 0 || phase.Steps <= 0 {
				return errInvalidStepConfig
			}
		case p.IsSpike():
			if phase.From <= 0 || phase.To <= 0 || phase.SpikeDuration <= 0 {
				return errInvalidSpikeConfig
			}
		default:
			return errUnsupportedPhaseType
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

	if test.Config.Thresholds.MaxP95Latency < 0 {
		return errNegativeP95Latency
	}

	if test.Config.Thresholds.MaxP99Latency < 0 {
		return errNegativeP99Latency
	}

	return nil
}

func evaluateThresholds(thresholds Thresholds, result Result) ([]ThresholdOutcome, error) {
	var outcomes []ThresholdOutcome
	var errs []error

	if thresholds.ErrorRate > 0 {
		var errorRate float64
		if result.Total > 0 {
			errorRate = float64(result.Failed) / float64(result.Total)
		}

		limitStr := strconv.FormatFloat(thresholds.ErrorRate, 'f', -1, 64)
		desc := "error_rate < " + limitStr
		if errorRate > thresholds.ErrorRate {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      errorRate,
				Limit:       thresholds.ErrorRate,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxMeanLatency > 0 {
		desc := fmt.Sprintf("mean_latency < %v", thresholds.MaxMeanLatency)
		if result.Latency.Mean > thresholds.MaxMeanLatency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.Mean,
				Limit:       thresholds.MaxMeanLatency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxP95Latency > 0 {
		desc := fmt.Sprintf("p95_latency < %v", thresholds.MaxP95Latency)
		if result.Latency.P95 > thresholds.MaxP95Latency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.P95,
				Limit:       thresholds.MaxP95Latency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxP99Latency > 0 {
		desc := fmt.Sprintf("p99_latency < %v", thresholds.MaxP99Latency)
		if result.Latency.P99 > thresholds.MaxP99Latency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.P99,
				Limit:       thresholds.MaxP99Latency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	return outcomes, errors.Join(errs...)
}

func toSchedulerPhases(phases []Phase) []scheduler.Phase {
	schedulerPhases := make([]scheduler.Phase, len(phases))
	for i := range phases {
		schedulerPhases[i] = scheduler.Phase{
			Type:          PhaseType(strings.TrimSpace(string(phases[i].Type))),
			Duration:      phases[i].Duration,
			ArrivalRate:   phases[i].ArrivalRate,
			From:          phases[i].From,
			To:            phases[i].To,
			Steps:         phases[i].Steps,
			SpikeAt:       phases[i].SpikeAt,
			SpikeDuration: phases[i].SpikeDuration,
		}
	}

	return schedulerPhases
}
