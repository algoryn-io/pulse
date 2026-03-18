package metrics

import (
	"testing"
	"time"
)

func TestAggregatorResult(t *testing.T) {
	aggregator := NewAggregator()
	aggregator.Record(10*time.Millisecond, false)
	aggregator.Record(30*time.Millisecond, true)

	result := aggregator.Result(50 * time.Millisecond)

	if result.Total != 2 {
		t.Fatalf("expected total 2, got %d", result.Total)
	}

	if result.Failed != 1 {
		t.Fatalf("expected failed 1, got %d", result.Failed)
	}

	if result.Duration != 50*time.Millisecond {
		t.Fatalf("expected duration 50ms, got %v", result.Duration)
	}

	if result.Latency.Min != 10*time.Millisecond {
		t.Fatalf("expected min 10ms, got %v", result.Latency.Min)
	}

	if result.Latency.Max != 30*time.Millisecond {
		t.Fatalf("expected max 30ms, got %v", result.Latency.Max)
	}

	if result.Latency.Mean != 20*time.Millisecond {
		t.Fatalf("expected mean 20ms, got %v", result.Latency.Mean)
	}
}
