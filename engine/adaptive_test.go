package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"algoryn.io/pulse/metrics"
	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
)

func TestAdaptiveControllerDefaultsStepValues(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxErrorRate: 0.1}, 100)
	if ctrl.cfg.StepDown != 0.9 {
		t.Fatalf("expected StepDown 0.9, got %f", ctrl.cfg.StepDown)
	}
	if ctrl.cfg.StepUp != 1.05 {
		t.Fatalf("expected StepUp 1.05, got %f", ctrl.cfg.StepUp)
	}
	if ctrl.cfg.MinRPS != 1 {
		t.Fatalf("expected MinRPS 1, got %d", ctrl.cfg.MinRPS)
	}
}

func TestAdaptiveControllerReducesRateOnErrorRateBreach(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxErrorRate: 0.1, StepDown: 0.5, StepUp: 1.1}, 100)

	snap := metrics.Snapshot{Total: 10, Failed: 5}
	ctrl.onSnapshot(snap)

	rate := ctrl.rate()
	if rate != 50 {
		t.Fatalf("expected rate 50 (100 * 0.5), got %f", rate)
	}
}

func TestAdaptiveControllerReducesRateOnP99Breach(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxP99: 100 * time.Millisecond, StepDown: 0.8, StepUp: 1.1}, 100)

	snap := metrics.Snapshot{
		Total:   10,
		Latency: metrics.LatencyStats{P99: 200 * time.Millisecond},
	}
	ctrl.onSnapshot(snap)

	rate := ctrl.rate()
	if rate != 80 {
		t.Fatalf("expected rate 80 (100 * 0.8), got %f", rate)
	}
}

func TestAdaptiveControllerIncreasesRateWhenHealthy(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxErrorRate: 0.1, StepDown: 0.5, StepUp: 1.1}, 100)

	snap := metrics.Snapshot{Total: 10, Failed: 0}
	ctrl.onSnapshot(snap)

	rate := ctrl.rate()
	if rate <= 100 {
		t.Fatalf("expected rate > 100, got %f", rate)
	}
}

func TestAdaptiveControllerRespectsMinRPS(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxErrorRate: 0.01, StepDown: 0.01, MinRPS: 5}, 10)

	for range 20 {
		ctrl.onSnapshot(metrics.Snapshot{Total: 10, Failed: 10})
	}

	if ctrl.rate() < 5 {
		t.Fatalf("expected rate >= 5 (MinRPS), got %f", ctrl.rate())
	}
}

func TestAdaptiveControllerRespectsMaxRPS(t *testing.T) {
	ctrl := newAdaptiveController(AdaptiveConfig{MaxErrorRate: 0.5, StepUp: 2.0, MaxRPS: 120}, 100)

	for range 10 {
		ctrl.onSnapshot(metrics.Snapshot{Total: 10, Failed: 0})
	}

	if ctrl.rate() > 120 {
		t.Fatalf("expected rate <= 120 (MaxRPS), got %f", ctrl.rate())
	}
}

func TestEngineAdaptiveReducesRPSUnderHighErrorRate(t *testing.T) {
	e := NewWithOptions(
		[]scheduler.Phase{
			{Type: model.PhaseTypeConstant, Duration: 400 * time.Millisecond, ArrivalRate: 200},
		},
		func(context.Context) (int, error) {
			return 500, errors.New("synthetic error")
		},
		Options{
			MaxConcurrency: 20,
			ReportInterval: 20 * time.Millisecond,
			Adaptive: AdaptiveConfig{
				MaxErrorRate: 0.05,
				StepDown:     0.7,
				StepUp:       1.1,
				MinRPS:       1,
			},
		},
	)

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected engine error: %v", err)
	}

	// With 100% error rate and 5% threshold, the controller should have
	// stepped down multiple times from 200 RPS. Effective RPS must be well below
	// the base rate after several reductions.
	if result.RPS >= 180 {
		t.Fatalf("expected RPS to be reduced by adaptive shaping, got %f", result.RPS)
	}
}

func TestAdaptiveConfigIsZero(t *testing.T) {
	if !(AdaptiveConfig{}).IsZero() {
		t.Fatal("expected zero AdaptiveConfig to be zero")
	}
	if (AdaptiveConfig{MaxErrorRate: 0.1}).IsZero() {
		t.Fatal("expected non-zero AdaptiveConfig to not be zero")
	}
	if (AdaptiveConfig{MaxP99: time.Second}).IsZero() {
		t.Fatal("expected non-zero AdaptiveConfig to not be zero")
	}
}
