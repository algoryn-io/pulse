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

	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < 2 {
		t.Fatalf("expected scenario to run multiple times, got %d", calls)
	}
}

func TestEngineRunPropagatesScenarioError(t *testing.T) {
	wantErr := errors.New("scenario failed")
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
	}, func(context.Context) error {
		return wantErr
	})

	if err := engine.Run(context.Background()); err != wantErr {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestEngineRunPropagatesUnsupportedPhaseType(t *testing.T) {
	engine := New([]scheduler.Phase{
		{Type: model.PhaseType("unsupported"), Duration: time.Second, ArrivalRate: 1},
	}, func(context.Context) error {
		return nil
	})

	err := engine.Run(context.Background())
	if !errors.Is(err, scheduler.ErrUnsupportedPhaseType) {
		t.Fatalf("expected %v, got %v", scheduler.ErrUnsupportedPhaseType, err)
	}
}
