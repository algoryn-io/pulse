package engine

import (
	"context"
	"testing"
	"time"

	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
)

// BenchmarkEngineRun measures end-to-end engine overhead across the full
// pipeline: scheduler → concurrency limiter → goroutine spawn → aggregator.
// Each iteration runs a single 10 ms constant phase with an unlimited
// concurrency cap and a no-op scenario. "invocations/iter" shows total
// scenario calls recorded by the aggregator per engine run.
//
// Run with:
//
//	go test -bench=BenchmarkEngineRun -benchtime=5s ./engine/
func BenchmarkEngineRun(b *testing.B) {
	phases := []scheduler.Phase{
		{
			Type:        model.PhaseTypeConstant,
			Duration:    10 * time.Millisecond,
			ArrivalRate: 500,
		},
	}
	scenario := func(context.Context) (int, error) { return 200, nil }

	var totalInvocations int64
	b.ResetTimer()
	for range b.N {
		// maxConcurrency=1000: effectively uncapped for a no-op scenario
		// at 500 rps over 10 ms (≈5 concurrent goroutines at peak).
		e := New(phases, scenario, 1000)
		result, err := e.Run(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		totalInvocations += result.Total
	}

	if b.N > 0 {
		b.ReportMetric(float64(totalInvocations)/float64(b.N), "invocations/iter")
	}
}

// BenchmarkEngineRunWithConcurrencyLimit measures engine overhead when a
// MaxConcurrency cap is active, exercising the semaphore acquire/release hot
// path in addition to the scheduler and aggregator.
func BenchmarkEngineRunWithConcurrencyLimit(b *testing.B) {
	phases := []scheduler.Phase{
		{
			Type:        model.PhaseTypeConstant,
			Duration:    10 * time.Millisecond,
			ArrivalRate: 500,
		},
	}
	scenario := func(context.Context) (int, error) { return 200, nil }

	var totalInvocations int64
	b.ResetTimer()
	for range b.N {
		e := New(phases, scenario, 50)
		result, err := e.Run(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		totalInvocations += result.Total
	}

	if b.N > 0 {
		b.ReportMetric(float64(totalInvocations)/float64(b.N), "invocations/iter")
	}
}

// BenchmarkEngineRunDropPolicy measures engine overhead under the drop
// saturation policy with a tight concurrency cap, exercising the
// TryAcquire fast path and dropped-request accounting.
func BenchmarkEngineRunDropPolicy(b *testing.B) {
	phases := []scheduler.Phase{
		{
			Type:        model.PhaseTypeConstant,
			Duration:    10 * time.Millisecond,
			ArrivalRate: 500,
		},
	}
	// Scenario blocks briefly so the concurrency cap is regularly hit,
	// exercising the drop path without making the benchmark too slow.
	scenario := func(context.Context) (int, error) {
		time.Sleep(time.Millisecond)
		return 200, nil
	}

	b.ResetTimer()
	for range b.N {
		e := NewWithSaturationPolicy(phases, scenario, 5, SaturationPolicyDrop)
		if _, err := e.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineRunMultiPhase measures engine overhead across phase
// boundaries: the engine must set up and tear down the scheduler for each
// phase and merge metrics across all of them.
func BenchmarkEngineRunMultiPhase(b *testing.B) {
	phases := []scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 5 * time.Millisecond, ArrivalRate: 200},
		{Type: model.PhaseTypeConstant, Duration: 5 * time.Millisecond, ArrivalRate: 500},
	}
	scenario := func(context.Context) (int, error) { return 200, nil }

	var totalInvocations int64
	b.ResetTimer()
	for range b.N {
		e := New(phases, scenario, 1000)
		result, err := e.Run(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		totalInvocations += result.Total
	}

	if b.N > 0 {
		b.ReportMetric(float64(totalInvocations)/float64(b.N), "invocations/iter")
	}
}
