package metrics

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAggregatorResult(t *testing.T) {
	aggregator := NewAggregator()
	aggregator.Record(10*time.Millisecond, 0, nil)
	aggregator.Record(30*time.Millisecond, 0, errors.New("failed"))

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

	if result.Latency.P50 != 10*time.Millisecond {
		t.Fatalf("expected p50 10ms, got %v", result.Latency.P50)
	}
	if result.Latency.P95 != 30*time.Millisecond {
		t.Fatalf("expected p95 30ms, got %v", result.Latency.P95)
	}
	if result.Latency.P99 != 30*time.Millisecond {
		t.Fatalf("expected p99 30ms, got %v", result.Latency.P99)
	}

	if len(result.ErrorCounts) != 1 || result.ErrorCounts["failed"] != 1 {
		t.Fatalf("expected errorCounts failed=1, got %+v", result.ErrorCounts)
	}
	if result.StatusCounts != nil {
		t.Fatalf("expected no status counts when err path, got %+v", result.StatusCounts)
	}
}

func TestAggregatorConcurrentRecord(t *testing.T) {
	aggregator := NewAggregator()

	latencies := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
	}
	errs := []error{nil, errors.New("boom"), nil, errors.New("boom")}

	var wg sync.WaitGroup
	for i := range latencies {
		wg.Add(1)
		go func(latency time.Duration, err error) {
			defer wg.Done()
			aggregator.Record(latency, 0, err)
		}(latencies[i], errs[i])
	}

	wg.Wait()

	result := aggregator.Result(100 * time.Millisecond)

	if result.Total != int64(len(latencies)) {
		t.Fatalf("expected total %d, got %d", len(latencies), result.Total)
	}

	if result.Failed != 2 {
		t.Fatalf("expected failed 2, got %d", result.Failed)
	}

	if result.Duration != 100*time.Millisecond {
		t.Fatalf("expected duration 100ms, got %v", result.Duration)
	}

	if result.Latency.Min != 10*time.Millisecond {
		t.Fatalf("expected min 10ms, got %v", result.Latency.Min)
	}

	if result.Latency.Max != 40*time.Millisecond {
		t.Fatalf("expected max 40ms, got %v", result.Latency.Max)
	}

	if result.Latency.Mean != 25*time.Millisecond {
		t.Fatalf("expected mean 25ms, got %v", result.Latency.Mean)
	}

	if result.Latency.P50 != 20*time.Millisecond {
		t.Fatalf("expected p50 20ms, got %v", result.Latency.P50)
	}
	if result.Latency.P95 != 40*time.Millisecond {
		t.Fatalf("expected p95 40ms, got %v", result.Latency.P95)
	}
	if result.Latency.P99 != 40*time.Millisecond {
		t.Fatalf("expected p99 40ms, got %v", result.Latency.P99)
	}

	if result.ErrorCounts["boom"] != 2 {
		t.Fatalf("expected errorCounts boom=2, got %+v", result.ErrorCounts)
	}
}

func TestAggregatorRetainsAllLatencies(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 0, nil)
	a.Record(2*time.Millisecond, 0, nil)
	a.Record(3*time.Millisecond, 0, errors.New("x"))

	if len(a.latencies) != 3 {
		t.Fatalf("expected 3 retained latencies, got %d", len(a.latencies))
	}
	if a.latencies[0] != time.Millisecond || a.latencies[1] != 2*time.Millisecond || a.latencies[2] != 3*time.Millisecond {
		t.Fatalf("unexpected retained order or values: %v", a.latencies)
	}
}

func TestPercentileFromSorted(t *testing.T) {
	s := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		100 * time.Millisecond,
	}
	if got := percentileFromSorted(s, 50); got != 3*time.Millisecond {
		t.Fatalf("p50: want 3ms, got %v", got)
	}
	if got := percentileFromSorted(s, 95); got != 100*time.Millisecond {
		t.Fatalf("p95: want 100ms, got %v", got)
	}
	if got := percentileFromSorted(s, 99); got != 100*time.Millisecond {
		t.Fatalf("p99: want 100ms, got %v", got)
	}
}

func TestAggregatorResultDoesNotMutateRetainedLatencies(t *testing.T) {
	a := NewAggregator()
	a.Record(30*time.Millisecond, 0, nil)
	a.Record(10*time.Millisecond, 0, nil)
	snapshot := append([]time.Duration(nil), a.latencies...)

	_ = a.Result(time.Second)

	if len(a.latencies) != len(snapshot) {
		t.Fatalf("latencies length: want %d, got %d", len(snapshot), len(a.latencies))
	}
	for i := range snapshot {
		if a.latencies[i] != snapshot[i] {
			t.Fatalf("latencies mutated at %d: want %v, got %v", i, snapshot, a.latencies)
		}
	}
}

func TestAggregatorStatusCountsOnSuccess(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 200, nil)
	a.Record(time.Millisecond, 200, nil)
	a.Record(time.Millisecond, 201, nil)

	r := a.Result(time.Second)
	if r.StatusCounts[200] != 2 || r.StatusCounts[201] != 1 {
		t.Fatalf("unexpected status counts: %+v", r.StatusCounts)
	}
	if r.Failed != 0 {
		t.Fatalf("expected 0 failed, got %d", r.Failed)
	}
}

func TestAggregatorErrorPathDoesNotIncrementStatusCounts(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 500, errors.New("transport: unexpected status code: 500"))

	r := a.Result(time.Second)
	if r.StatusCounts != nil && len(r.StatusCounts) > 0 {
		t.Fatalf("expected no status counts on error path, got %+v", r.StatusCounts)
	}
	if r.ErrorCounts["transport: unexpected status code: 500"] != 1 {
		t.Fatalf("unexpected errorCounts: %+v", r.ErrorCounts)
	}
	if r.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", r.Failed)
	}
}

func TestAggregatorFailedWhenStatusAtLeast400WithoutErr(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 404, nil)

	r := a.Result(time.Second)
	if r.Failed != 1 {
		t.Fatalf("expected 1 failed for 404, got %d", r.Failed)
	}
	if r.StatusCounts[404] != 1 {
		t.Fatalf("expected status count 404, got %+v", r.StatusCounts)
	}
}

func TestAggregatorResultReturnsCopiedMaps(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 200, nil)
	r := a.Result(time.Second)
	r.StatusCounts[200] = 99

	r2 := a.Result(time.Second)
	if r2.StatusCounts[200] != 1 {
		t.Fatalf("internal statusCounts mutated via snapshot, got %+v", r2.StatusCounts)
	}
}