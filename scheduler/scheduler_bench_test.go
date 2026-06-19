package scheduler

import (
	"context"
	"testing"
	"time"

	"algoryn.io/pulse/model"
)

// BenchmarkSchedulerConstant measures the per-phase overhead of the constant
// scheduler: token-bucket refill, poll-loop timer management, and scenario
// dispatch. Each iteration runs a complete 10 ms phase at 500 rps with a
// no-op scenario. ns/op is dominated by the configured phase duration;
// "invocations/iter" shows how many scenario calls were fired per phase run.
//
// Run with:
//
//	go test -bench=BenchmarkSchedulerConstant -benchtime=5s ./scheduler/
func BenchmarkSchedulerConstant(b *testing.B) {
	const (
		phaseDuration = 10 * time.Millisecond
		arrivalRate   = 500
	)

	phase := Phase{
		Type:        model.PhaseTypeConstant,
		Duration:    phaseDuration,
		ArrivalRate: arrivalRate,
	}

	var total int // safe: scheduler dispatches scenario sequentially
	b.ResetTimer()
	for range b.N {
		_ = Run(context.Background(), phase, func(context.Context) error {
			total++
			return nil
		})
	}

	if b.N > 0 {
		b.ReportMetric(float64(total)/float64(b.N), "invocations/iter")
	}
}

// BenchmarkSchedulerRamp measures overhead for a ramp phase, where the
// token-bucket refill rate is updated on every poll-loop iteration as the
// rate interpolates linearly between From and To.
func BenchmarkSchedulerRamp(b *testing.B) {
	const phaseDuration = 10 * time.Millisecond

	phase := Phase{
		Type:     model.PhaseTypeRamp,
		Duration: phaseDuration,
		From:     100,
		To:       500,
	}

	var total int
	b.ResetTimer()
	for range b.N {
		_ = Run(context.Background(), phase, func(context.Context) error {
			total++
			return nil
		})
	}

	if b.N > 0 {
		b.ReportMetric(float64(total)/float64(b.N), "invocations/iter")
	}
}

// BenchmarkSchedulerStep measures overhead for a step phase, which switches
// the refill rate at discrete step boundaries rather than every iteration.
func BenchmarkSchedulerStep(b *testing.B) {
	const phaseDuration = 10 * time.Millisecond

	phase := Phase{
		Type:     model.PhaseTypeStep,
		Duration: phaseDuration,
		From:     100,
		To:       500,
		Steps:    5,
	}

	var total int
	b.ResetTimer()
	for range b.N {
		_ = Run(context.Background(), phase, func(context.Context) error {
			total++
			return nil
		})
	}

	if b.N > 0 {
		b.ReportMetric(float64(total)/float64(b.N), "invocations/iter")
	}
}
