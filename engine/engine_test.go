package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmgo38/Pulse/model"
	"github.com/jmgo38/Pulse/scheduler"
)

func TestEngineRunExecutesScenarioAcrossPhases(t *testing.T) {
	calls := 0
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
	}, func(context.Context) error {
		calls++
		return nil
	})

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < 2 {
		t.Fatalf("expected scenario to run multiple times, got %d", calls)
	}

	if result.Total != int64(calls) {
		t.Fatalf("expected total %d, got %d", calls, result.Total)
	}

	if result.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", result.Failed)
	}

	if result.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", result.Duration)
	}
}

func TestEngineRunPropagatesScenarioError(t *testing.T) {
	wantErr := errors.New("scenario failed")
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
	}, func(context.Context) error {
		return wantErr
	})

	result, err := engine.Run(context.Background())
	if err != wantErr {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	if result.Total != 1 {
		t.Fatalf("expected total 1, got %d", result.Total)
	}

	if result.Failed != 1 {
		t.Fatalf("expected failed 1, got %d", result.Failed)
	}
}

func TestEngineRunPropagatesUnsupportedPhaseType(t *testing.T) {
	engine := New([]scheduler.Phase{
		{Type: model.PhaseType("unsupported"), Duration: time.Second, ArrivalRate: 1},
	}, func(context.Context) error {
		return nil
	})

	_, err := engine.Run(context.Background())
	if !errors.Is(err, scheduler.ErrUnsupportedPhaseType) {
		t.Fatalf("expected %v, got %v", scheduler.ErrUnsupportedPhaseType, err)
	}
}
