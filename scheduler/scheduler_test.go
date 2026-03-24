package scheduler

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jmgo38/Pulse/model"
)

// constantPhaseCallBounds returns [min,max] inclusive for the number of scenario
// invocations expected from a constant phase with drained token bucket pacing,
// arrivalRate r over duration d. tol is a fraction (e.g. 0.25 for ±25%).
//
// Expected calls ≈ r * d (seconds), with slack for first-token delay (~1/r),
// 1ms polling, and OS timer jitter.
func constantPhaseCallBounds(r int, d time.Duration, tol float64) (min, max int) {
	exp := float64(r) * d.Seconds()
	low := exp * (1 - tol)
	high := exp * (1 + tol)
	min = int(math.Floor(low))
	max = int(math.Ceil(high))
	if min < 1 {
		min = 1
	}
	if max < min {
		max = min
	}
	return min, max
}

// rampPhaseCallBounds uses average rate (From+To)/2 over duration d; linear ramp
// has the same integrated rate. tol widens for stepwise SetRefillRate + polling.
func rampPhaseCallBounds(from, to int, d time.Duration, tol float64) (min, max int) {
	exp := 0.5 * float64(from+to) * d.Seconds()
	low := exp * (1 - tol)
	high := exp * (1 + tol)
	min = int(math.Floor(low))
	max = int(math.Ceil(high))
	if min < 1 {
		min = 1
	}
	if max < min {
		max = min
	}
	return min, max
}

// stepPhaseCallBounds uses the same average-rate integral as rampPhaseCallBounds;
// stepwise SetRefillRate adds extra jitter vs a smooth ramp.
func stepPhaseCallBounds(from, to int, d time.Duration, tol float64) (min, max int) {
	return rampPhaseCallBounds(from, to, d, tol)
}

func spikePhaseCallBounds(from, to int, duration, spikeAt, spikeDuration time.Duration, tol float64) (min, max int) {
	baseTime := duration - spikeDuration
	if baseTime < 0 {
		baseTime = 0
	}
	if spikeAt > duration {
		baseTime = duration
		spikeDuration = 0
	}
	exp := float64(from)*baseTime.Seconds() + float64(to)*spikeDuration.Seconds()
	low := exp * (1 - tol)
	high := exp * (1 + tol)
	min = int(math.Floor(low))
	max = int(math.Ceil(high))
	if min < 1 {
		min = 1
	}
	if max < min {
		max = min
	}
	return min, max
}

func TestRunConstantExecutesScenarioMultipleTimes(t *testing.T) {
	const (
		arrivalRate = 25
		duration    = 200 * time.Millisecond
	)
	minCalls, maxCalls := constantPhaseCallBounds(arrivalRate, duration, 0.25)
	if minCalls < 2 {
		minCalls = 2
	}

	calls := 0

	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    duration,
		ArrivalRate: arrivalRate,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < minCalls || calls > maxCalls {
		t.Fatalf("expected calls in [%d,%d] (≈%d over %v @ %d/s), got %d",
			minCalls, maxCalls, int(float64(arrivalRate)*duration.Seconds()), duration, arrivalRate, calls)
	}
}

func TestRunConstantRoughlyRespectsDurationAndRate(t *testing.T) {
	const (
		arrivalRate = 20
		duration    = 280 * time.Millisecond
	)
	minCalls, maxCalls := constantPhaseCallBounds(arrivalRate, duration, 0.25)

	calls := 0

	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    duration,
		ArrivalRate: arrivalRate,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < minCalls || calls > maxCalls {
		t.Fatalf("expected calls in [%d,%d] (≈%.1f over %v @ %d/s), got %d",
			minCalls, maxCalls, float64(arrivalRate)*duration.Seconds(), duration, arrivalRate, calls)
	}
}

func TestRunConstantPropagatesScenarioError(t *testing.T) {
	wantErr := errors.New("scenario failed")
	calls := 0

	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    200 * time.Millisecond,
		ArrivalRate: 50,
	}, func(context.Context) error {
		calls++
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	if calls != 1 {
		t.Fatalf("expected scheduler to stop after first error, got %d calls", calls)
	}
}

func TestRunReturnsErrorForUnsupportedPhaseType(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:        model.PhaseType("unsupported"),
		Duration:    time.Second,
		ArrivalRate: 1,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrUnsupportedPhaseType) {
		t.Fatalf("expected %v, got %v", ErrUnsupportedPhaseType, err)
	}
}

func TestRunReturnsErrorForNonPositiveArrivalRate(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    time.Second,
		ArrivalRate: 0,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrNonPositiveArrivalRate) {
		t.Fatalf("expected %v, got %v", ErrNonPositiveArrivalRate, err)
	}
}

func TestRunRampCompletesWithoutPanic(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeRamp,
		Duration: 120 * time.Millisecond,
		From:     2,
		To:       10,
	}, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRunRampInvokesScenario(t *testing.T) {
	const (
		from     = 10
		to       = 30
		duration = 280 * time.Millisecond
	)
	minCalls, maxCalls := rampPhaseCallBounds(from, to, duration, 0.30)
	if minCalls < 2 {
		minCalls = 2
	}

	calls := 0
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeRamp,
		Duration: duration,
		From:     from,
		To:       to,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < minCalls || calls > maxCalls {
		exp := 0.5 * float64(from+to) * duration.Seconds()
		t.Fatalf("expected calls in [%d,%d] (≈%.1f invocations over %v), got %d",
			minCalls, maxCalls, exp, duration, calls)
	}
}

func TestRunRampReturnsErrorForInvalidEndpoints(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeRamp,
		Duration: time.Second,
		From:     0,
		To:       10,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidRampEndpoints) {
		t.Fatalf("expected %v, got %v", ErrInvalidRampEndpoints, err)
	}
}

func TestRunStepCompletesWithoutPanic(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeStep,
		Duration: 120 * time.Millisecond,
		From:     2,
		To:       10,
		Steps:    3,
	}, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRunStepInvokesScenario(t *testing.T) {
	const (
		from     = 10
		to       = 40
		steps    = 3
		duration = 280 * time.Millisecond
	)
	minCalls, maxCalls := stepPhaseCallBounds(from, to, duration, 0.35)
	if minCalls < 2 {
		minCalls = 2
	}

	calls := 0
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeStep,
		Duration: duration,
		From:     from,
		To:       to,
		Steps:    steps,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < minCalls || calls > maxCalls {
		exp := 0.5 * float64(from+to) * duration.Seconds()
		t.Fatalf("expected calls in [%d,%d] (≈%.1f invocations over %v), got %d",
			minCalls, maxCalls, exp, duration, calls)
	}
}

func TestRunStepReturnsErrorForInvalidConfig(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeStep,
		Duration: time.Second,
		From:     10,
		To:       20,
		Steps:    0,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidStepConfig) {
		t.Fatalf("expected %v for Steps=0, got %v", ErrInvalidStepConfig, err)
	}

	err = Run(context.Background(), Phase{
		Type:     model.PhaseTypeStep,
		Duration: time.Second,
		From:     0,
		To:       20,
		Steps:    3,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidStepConfig) {
		t.Fatalf("expected %v for From=0, got %v", ErrInvalidStepConfig, err)
	}
}

func TestRunSpikeCompletesWithoutPanic(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:          model.PhaseTypeSpike,
		Duration:      150 * time.Millisecond,
		From:          5,
		To:            50,
		SpikeAt:       50 * time.Millisecond,
		SpikeDuration: 50 * time.Millisecond,
	}, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRunSpikeInvokesScenario(t *testing.T) {
	const (
		from          = 10
		to            = 50
		duration      = 300 * time.Millisecond
		spikeAt       = 100 * time.Millisecond
		spikeDuration = 100 * time.Millisecond
	)
	minCalls, maxCalls := spikePhaseCallBounds(from, to, duration, spikeAt, spikeDuration, 0.50)

	calls := 0
	err := Run(context.Background(), Phase{
		Type:          model.PhaseTypeSpike,
		Duration:      duration,
		From:          from,
		To:            to,
		SpikeAt:       spikeAt,
		SpikeDuration: spikeDuration,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < minCalls || calls > maxCalls {
		exp := float64(from)*0.2 + float64(to)*0.1
		t.Fatalf("expected calls in [%d,%d] (≈%.1f invocations over %v), got %d",
			minCalls, maxCalls, exp, duration, calls)
	}
}

func TestRunSpikeReturnsErrorForInvalidConfig(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:          model.PhaseTypeSpike,
		Duration:      time.Second,
		From:          10,
		To:            20,
		SpikeDuration: 0,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidSpikeConfig) {
		t.Fatalf("expected %v for SpikeDuration=0, got %v", ErrInvalidSpikeConfig, err)
	}

	err = Run(context.Background(), Phase{
		Type:          model.PhaseTypeSpike,
		Duration:      time.Second,
		From:          0,
		To:            20,
		SpikeDuration: 100 * time.Millisecond,
	}, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidSpikeConfig) {
		t.Fatalf("expected %v for From=0, got %v", ErrInvalidSpikeConfig, err)
	}
}

func TestRunSpikeSpikeAtZeroStartsImmediately(t *testing.T) {
	err := Run(context.Background(), Phase{
		Type:          model.PhaseTypeSpike,
		Duration:      150 * time.Millisecond,
		From:          5,
		To:            50,
		SpikeAt:       0,
		SpikeDuration: 80 * time.Millisecond,
	}, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
