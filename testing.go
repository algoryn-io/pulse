package pulse

import (
	"testing"
	"time"
)

// TB is the minimal testing interface required by Pulse helpers.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	Skip(args ...any)
}

// RunT runs a Pulse load test as a Go test.
// It calls t.Fatal if any threshold fails or if the engine returns an error.
// Metrics are reported via t.Log, visible with go test -v.
// It returns the Result for additional assertions.
func RunT(t TB, test Test) Result {
	t.Helper()

	startedAt := time.Now()

	result, err := Run(test)

	t.Logf("pulse: total=%d failed=%d rps=%.2f duration=%v",
		result.Total, result.Failed, result.RPS, result.Duration)
	t.Logf("pulse: latency p50=%v p95=%v p99=%v",
		result.Latency.P50, result.Latency.P95, result.Latency.P99)

	for _, outcome := range result.ThresholdOutcomes {
		status := "PASS"
		if !outcome.Pass {
			status = "FAIL"
		}
		t.Logf("pulse: threshold [%s] %s", status, outcome.Description)
	}

	_ = startedAt

	if err != nil {
		t.Fatalf("pulse: %v", err)
	}

	return result
}

// SkipIfShort skips the test if -short flag is set.
func SkipIfShort(t TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("pulse: skipping load test in short mode (-short)")
	}
}
