package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
)

func TestEngineAbortsOnErrorRate(t *testing.T) {
	// A long phase whose scenario always fails; with abort on error rate the run
	// must stop well before the full duration.
	eng := NewWithOptions(
		[]scheduler.Phase{
			{Type: model.PhaseTypeConstant, Duration: 5 * time.Second, ArrivalRate: 200},
		},
		func(_ context.Context) (int, error) { return 500, errors.New("boom") },
		Options{
			MaxConcurrency: 100,
			Saturation:     SaturationPolicyDrop,
			ReportInterval: 20 * time.Millisecond,
			Abort:          AbortConfig{MaxErrorRate: 0.5},
		},
	)

	start := time.Now()
	_, err := eng.Run(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected an early abort, but ran for %v", elapsed)
	}
}

func TestEngineDoesNotAbortWhenHealthy(t *testing.T) {
	eng := NewWithOptions(
		[]scheduler.Phase{
			{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
		},
		func(_ context.Context) (int, error) { return 200, nil },
		Options{
			MaxConcurrency: 50,
			Saturation:     SaturationPolicyDrop,
			ReportInterval: 20 * time.Millisecond,
			Abort:          AbortConfig{MaxErrorRate: 0.5, MaxP99: time.Second},
		},
	)

	_, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("healthy run should not error, got %v", err)
	}
}

func TestEngineAbortMinRequestsGuard(t *testing.T) {
	// Even with a 100% error rate, a very high MinRequests means no single
	// interval reaches the threshold count, so the run completes normally.
	eng := NewWithOptions(
		[]scheduler.Phase{
			{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
		},
		func(_ context.Context) (int, error) { return 500, errors.New("boom") },
		Options{
			MaxConcurrency: 50,
			Saturation:     SaturationPolicyDrop,
			ReportInterval: 20 * time.Millisecond,
			Abort:          AbortConfig{MaxErrorRate: 0.5, MinRequests: 1_000_000},
		},
	)

	_, err := eng.Run(context.Background())
	if errors.Is(err, ErrAborted) {
		t.Fatal("MinRequests guard should have prevented the abort")
	}
}

func TestAbortConfigIsZero(t *testing.T) {
	if !(AbortConfig{}).IsZero() {
		t.Error("empty AbortConfig should be zero")
	}
	if (AbortConfig{MaxErrorRate: 0.1}).IsZero() {
		t.Error("AbortConfig with MaxErrorRate should not be zero")
	}
	if (AbortConfig{MaxP99: time.Second}).IsZero() {
		t.Error("AbortConfig with MaxP99 should not be zero")
	}
}
