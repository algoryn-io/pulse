package engine

import (
	"context"
	"testing"
	"time"

	"algoryn.io/pulse/metrics"
	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
)

func healthySnap() metrics.Snapshot { return metrics.Snapshot{Total: 100, Failed: 0} }

func TestStressControllerRampsUpThenFails(t *testing.T) {
	c := newStressController(StressConfig{
		StepRPS:      50,
		MaxErrorRate: 0.1,
	}, 100)

	if c.rate() != 100 {
		t.Fatalf("initial rate = %v, want 100", c.rate())
	}
	// Two healthy intervals: 100 -> 150 -> 200.
	c.onSnapshot(healthySnap())
	if c.rate() != 150 {
		t.Fatalf("after 1 healthy: rate = %v, want 150", c.rate())
	}
	c.onSnapshot(healthySnap())
	if c.rate() != 200 {
		t.Fatalf("after 2 healthy: rate = %v, want 200", c.rate())
	}
	// Now an interval (running at 200) breaches the error rate.
	c.onSnapshot(metrics.Snapshot{Total: 100, Failed: 50})
	if !c.isDone() {
		t.Fatal("expected the controller to be done after a breach")
	}
	res := c.result()
	if !res.Failed || res.Reason != "error_rate" {
		t.Fatalf("unexpected outcome: %+v", res)
	}
	if res.FailedAtRPS != 200 {
		t.Errorf("FailedAtRPS = %d, want 200", res.FailedAtRPS)
	}
	if res.MaxHealthyRPS != 150 {
		t.Errorf("MaxHealthyRPS = %d, want 150", res.MaxHealthyRPS)
	}
}

func TestStressControllerSustainedIntervals(t *testing.T) {
	c := newStressController(StressConfig{
		StepRPS:            10,
		MaxP99:             100 * time.Millisecond,
		SustainedIntervals: 2,
	}, 50)

	breach := metrics.Snapshot{Total: 100, Latency: metrics.LatencyStats{P99: 200 * time.Millisecond}}
	c.onSnapshot(breach) // first breach: not yet done
	if c.isDone() {
		t.Fatal("should not be done after a single breach with SustainedIntervals=2")
	}
	c.onSnapshot(breach) // second consecutive breach: done
	if !c.isDone() {
		t.Fatal("expected done after 2 consecutive breaches")
	}
	if c.result().Reason != "p99_latency" {
		t.Fatalf("reason = %q, want p99_latency", c.result().Reason)
	}
}

func TestStressControllerBreachStreakResets(t *testing.T) {
	c := newStressController(StressConfig{StepRPS: 10, MaxErrorRate: 0.1, SustainedIntervals: 2}, 50)
	c.onSnapshot(metrics.Snapshot{Total: 100, Failed: 50}) // breach 1
	c.onSnapshot(healthySnap())                            // recovers -> streak resets, ramps
	c.onSnapshot(metrics.Snapshot{Total: 100, Failed: 50}) // breach 1 again
	if c.isDone() {
		t.Fatal("a single breach after recovery should not finish the run")
	}
}

func TestStressControllerMinRequestsGuard(t *testing.T) {
	c := newStressController(StressConfig{StepRPS: 10, MaxErrorRate: 0.1, MinRequests: 50}, 100)
	// Tiny window below MinRequests: neither ramps nor fails.
	c.onSnapshot(metrics.Snapshot{Total: 10, Failed: 10})
	if c.isDone() || c.rate() != 100 {
		t.Fatalf("small window should be ignored: done=%v rate=%v", c.isDone(), c.rate())
	}
}

func TestStressControllerMaxRPSCap(t *testing.T) {
	c := newStressController(StressConfig{StepRPS: 100, MaxRPS: 120, MaxErrorRate: 0.5}, 100)
	c.onSnapshot(healthySnap()) // 100 -> would be 200, capped to 120
	if c.rate() != 120 {
		t.Fatalf("rate = %v, want capped 120", c.rate())
	}
}

// Integration: a consistently slow scenario breaches MaxP99 and the run stops
// gracefully (nil error) with Result.Stress populated.
func TestEngineStressStopsAndReportsCapacity(t *testing.T) {
	scenario := func(ctx context.Context) (int, error) {
		select {
		case <-time.After(40 * time.Millisecond):
			return 200, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	phases := []scheduler.Phase{{
		Type:        model.PhaseTypeConstant,
		Duration:    5 * time.Second, // generous ceiling; stress should stop sooner
		ArrivalRate: 50,
	}}
	eng := NewWithOptions(phases, scenario, Options{
		MaxConcurrency: 100,
		ReportInterval: 25 * time.Millisecond,
		Stress: StressConfig{
			StepRPS: 50,
			MaxP99:  10 * time.Millisecond, // 40ms latency always breaches
		},
	})

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("stress run should end without error, got %v", err)
	}
	if res.Stress == nil {
		t.Fatal("expected Result.Stress to be populated")
	}
	if !res.Stress.Failed || res.Stress.Reason != "p99_latency" {
		t.Fatalf("expected a p99 failure, got %+v", res.Stress)
	}
}

// Integration: a fast, healthy scenario never breaches and the ramp completes at
// MaxRPS with Failed=false.
func TestEngineStressCompletesWithoutFailure(t *testing.T) {
	scenario := func(ctx context.Context) (int, error) { return 200, nil }
	phases := []scheduler.Phase{{
		Type:        model.PhaseTypeConstant,
		Duration:    200 * time.Millisecond,
		ArrivalRate: 100,
	}}
	eng := NewWithOptions(phases, scenario, Options{
		MaxConcurrency: 100,
		ReportInterval: 25 * time.Millisecond,
		Stress: StressConfig{
			StepRPS: 50,
			MaxRPS:  300,
			MaxP99:  500 * time.Millisecond, // never breached by a no-op scenario
		},
	})

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.Stress == nil || res.Stress.Failed {
		t.Fatalf("expected a non-failing stress outcome, got %+v", res.Stress)
	}
	if res.Stress.MaxHealthyRPS < 100 {
		t.Fatalf("expected MaxHealthyRPS >= 100, got %d", res.Stress.MaxHealthyRPS)
	}
}
