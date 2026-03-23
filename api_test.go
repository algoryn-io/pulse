package pulse

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunReturnsErrorWhenNoPhases(t *testing.T) {
	test := Test{
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNoPhases {
		t.Fatalf("expected %v, got %v", errNoPhases, err)
	}
}

func TestRunReturnsErrorWhenScenarioIsNil(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
		},
	}

	_, err := Run(test)
	if err != errNilScenario {
		t.Fatalf("expected %v, got %v", errNilScenario, err)
	}
}

func TestRunReturnsErrorWhenPhaseDurationIsNotPositive(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 0, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNonPositivePhase {
		t.Fatalf("expected %v, got %v", errNonPositivePhase, err)
	}
}

func TestRunReturnsErrorWhenPhaseTypeIsEmpty(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: "", Duration: time.Second, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errEmptyPhaseType {
		t.Fatalf("expected %v, got %v", errEmptyPhaseType, err)
	}
}

func TestRunReturnsErrorWhenPhaseTypeIsUnsupported(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseType("custom"), Duration: time.Second, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errUnsupportedPhaseType {
		t.Fatalf("expected %v, got %v", errUnsupportedPhaseType, err)
	}
}

func TestRunReturnsErrorWhenPhaseArrivalRateIsNotPositive(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 0},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNonPositiveArrivalRate {
		t.Fatalf("expected %v, got %v", errNonPositiveArrivalRate, err)
	}
}

func TestRunReturnsErrorWhenRampEndpointsAreInvalid(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeRamp, Duration: time.Second, From: 0, To: 5},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errInvalidRampEndpoints {
		t.Fatalf("expected %v, got %v", errInvalidRampEndpoints, err)
	}
}

func TestRunExecutesRampPhase(t *testing.T) {
	calls := 0
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeRamp, Duration: 250 * time.Millisecond, From: 10, To: 25},
			},
			MaxConcurrency: 2,
		},
		Scenario: func(context.Context) (int, error) {
			calls++
			return 0, nil
		},
	}

	_, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected ramp to invoke scenario multiple times, got %d", calls)
	}
}

func TestRunExecutesScenario(t *testing.T) {
	calls := 0
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
			MaxConcurrency: 4,
		},
		Scenario: func(context.Context) (int, error) {
			calls++
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls == 0 {
		t.Fatal("expected scenario to execute at least once")
	}

	if got.Total != int64(calls) {
		t.Fatalf("expected total %d, got %d", calls, got.Total)
	}

	if got.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", got.Failed)
	}

	if got.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", got.Duration)
	}

	if got.Latency.Min <= 0 {
		t.Fatalf("expected positive min latency, got %v", got.Latency.Min)
	}

	if got.Latency.Max <= 0 {
		t.Fatalf("expected positive max latency, got %v", got.Latency.Max)
	}

	if got.Latency.Mean <= 0 {
		t.Fatalf("expected positive mean latency, got %v", got.Latency.Mean)
	}

	l := got.Latency
	if l.P50 <= 0 || l.P95 <= 0 || l.P99 <= 0 {
		t.Fatalf("expected positive latency percentiles, got %+v", l)
	}
	if l.Min > l.P50 || l.P50 > l.Max {
		t.Fatalf("P50 outside [min,max]: %+v", l)
	}
	if l.P50 > l.P95 || l.P95 > l.P99 {
		t.Fatalf("expected P50 <= P95 <= P99, got %v %v %v", l.P50, l.P95, l.P99)
	}
	if l.P99 > l.Max {
		t.Fatalf("P99 above max: %+v", l)
	}
}

func TestRunRecordsScenarioErrorsWithoutAborting(t *testing.T) {
	wantErr := errors.New("scenario failed")
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
			MaxConcurrency: 4,
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, wantErr
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected nil error from Run when only scenario fails, got %v", err)
	}

	if got.Total < 2 {
		t.Fatalf("expected run to continue, total %d", got.Total)
	}

	if got.Failed != got.Total {
		t.Fatalf("expected all executions failed, total %d failed %d", got.Total, got.Failed)
	}

	if got.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", got.Duration)
	}

	if got.Latency.Min <= 0 || got.Latency.Max <= 0 || got.Latency.Mean <= 0 {
		t.Fatalf("expected latency fields to be populated, got %+v", got.Latency)
	}
}

func TestRunPassesThresholds(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.5,
				MaxMeanLatency: 50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got.Total == 0 {
		t.Fatal("expected executions to run")
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: true, Description: "error_rate < 0.5"},
		{Pass: true, Description: "mean_latency < 50ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestRunFailsWhenThresholdsAreViolated(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.1,
				MaxMeanLatency: time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, errors.New("scenario failed")
		},
	}

	got, err := Run(test)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var tv *ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError in chain, got %v", err)
	}

	if got.Total < 1 {
		t.Fatalf("expected at least one execution, got %d", got.Total)
	}

	if got.Failed != got.Total {
		t.Fatalf("expected all executions failed, total %d failed %d", got.Total, got.Failed)
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: false, Description: "error_rate < 0.1"},
		{Pass: false, Description: "mean_latency < 1ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestThresholdViolationErrorFormatsErrorRateNicely(t *testing.T) {
	err := (&ThresholdViolationError{
		Description: "error_rate < 0.1",
		Actual:      1.0,
		Limit:       0.1,
	}).Error()

	want := "pulse: threshold violated (error_rate < 0.1): got 1.000 (100.0%), limit 0.100 (10.0%)"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}

func TestThresholdViolationErrorFormatsLatencyNicely(t *testing.T) {
	err := (&ThresholdViolationError{
		Description: "mean_latency < 200ms",
		Actual:      250 * time.Millisecond,
		Limit:       200 * time.Millisecond,
	}).Error()

	want := "pulse: threshold violated (mean_latency < 200ms): got 250ms, limit 200ms"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}

func TestRunReturnsErrorWhenThresholdMaxP95LatencyIsNegative(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			Thresholds: Thresholds{MaxP95Latency: -time.Millisecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeP95Latency {
		t.Fatalf("expected %v, got %v", errNegativeP95Latency, err)
	}
}

func TestRunReturnsErrorWhenThresholdMaxP99LatencyIsNegative(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			Thresholds: Thresholds{MaxP99Latency: -time.Millisecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeP99Latency {
		t.Fatalf("expected %v, got %v", errNegativeP99Latency, err)
	}
}

func TestRunPassesP95AndP99Thresholds(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 120 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				MaxP95Latency: 50 * time.Millisecond,
				MaxP99Latency: 50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: true, Description: "p95_latency < 50ms"},
		{Pass: true, Description: "p99_latency < 50ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestRunFailsWhenP95ThresholdViolated(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 200 * time.Millisecond, ArrivalRate: 15},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				MaxP95Latency: 2 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(10 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var tv *ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError, got %v", err)
	}
	if tv.Description != "p95_latency < 2ms" {
		t.Fatalf("description: got %q, want p95_latency < 2ms", tv.Description)
	}

	if len(got.ThresholdOutcomes) != 1 {
		t.Fatalf("expected 1 threshold outcome, got %+v", got.ThresholdOutcomes)
	}
	want := ThresholdOutcome{Pass: false, Description: "p95_latency < 2ms"}
	if got.ThresholdOutcomes[0] != want {
		t.Fatalf("want %+v, got %+v", want, got.ThresholdOutcomes[0])
	}
}

func TestRunThresholdOutcomesStableOrderWhenAllSet(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 120 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.5,
				MaxMeanLatency: 50 * time.Millisecond,
				MaxP95Latency:  50 * time.Millisecond,
				MaxP99Latency:  50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []ThresholdOutcome{
		{Pass: true, Description: "error_rate < 0.5"},
		{Pass: true, Description: "mean_latency < 50ms"},
		{Pass: true, Description: "p95_latency < 50ms"},
		{Pass: true, Description: "p99_latency < 50ms"},
	}
	if len(got.ThresholdOutcomes) != len(want) {
		t.Fatalf("expected %d outcomes, got %+v", len(want), got.ThresholdOutcomes)
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}
