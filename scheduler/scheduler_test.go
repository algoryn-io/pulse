package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmgo38/Pulse/model"
)

func TestRunConstantExecutesScenarioMultipleTimes(t *testing.T) {
	calls := 0

	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    80 * time.Millisecond,
		ArrivalRate: 50,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < 2 {
		t.Fatalf("expected scenario to execute multiple times, got %d", calls)
	}
}

func TestRunConstantRoughlyRespectsDurationAndRate(t *testing.T) {
	calls := 0

	err := Run(context.Background(), Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    250 * time.Millisecond,
		ArrivalRate: 20,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Drained token bucket paces at ~arrival rate; bounds stay loose for wall-clock variance.
	if calls < 4 || calls > 40 {
		t.Fatalf("expected calls roughly in [4,40], got %d", calls)
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
		Duration: 50 * time.Millisecond,
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
	calls := 0
	err := Run(context.Background(), Phase{
		Type:     model.PhaseTypeRamp,
		Duration: 250 * time.Millisecond,
		From:     10,
		To:       30,
	}, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected multiple scenario invocations, got %d", calls)
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
