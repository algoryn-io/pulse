package pulse

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunReturnsErrorWhenNoPhases(t *testing.T) {
	test := Test{
		Scenario: func(context.Context) error { return nil },
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
		Scenario: func(context.Context) error { return nil },
	}

	_, err := Run(test)
	if err != errNonPositivePhase {
		t.Fatalf("expected %v, got %v", errNonPositivePhase, err)
	}
}

func TestRunReturnsErrorWhenPhaseArrivalRateIsNotPositive(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 0},
			},
		},
		Scenario: func(context.Context) error { return nil },
	}

	_, err := Run(test)
	if err != errNonPositiveArrivalRate {
		t.Fatalf("expected %v, got %v", errNonPositiveArrivalRate, err)
	}
}

func TestRunExecutesScenario(t *testing.T) {
	calls := 0
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
		},
		Scenario: func(context.Context) error {
			calls++
			return nil
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
}

func TestRunPropagatesScenarioError(t *testing.T) {
	wantErr := errors.New("scenario failed")
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
		},
		Scenario: func(context.Context) error {
			return wantErr
		},
	}

	got, err := Run(test)
	if err != wantErr {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	if got.Total != 1 {
		t.Fatalf("expected total 1, got %d", got.Total)
	}

	if got.Failed != 1 {
		t.Fatalf("expected failed 1, got %d", got.Failed)
	}

	if got.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", got.Duration)
	}

	if got.Latency.Min <= 0 || got.Latency.Max <= 0 || got.Latency.Mean <= 0 {
		t.Fatalf("expected latency fields to be populated, got %+v", got.Latency)
	}
}
