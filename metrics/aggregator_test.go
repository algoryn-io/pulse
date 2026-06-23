package metrics

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"algoryn.io/pulse/transport"
)

// histTol is a tolerance for histogram-derived percentiles (log buckets + interpolation).
const histTol = 60 * time.Microsecond

func assertDurationNear(t *testing.T, name string, want, got time.Duration) {
	t.Helper()
	d := want - got
	if d < 0 {
		d = -d
	}
	if d > histTol {
		t.Fatalf("%s: want near %v, got %v (tol %v)", name, want, got, histTol)
	}
}

// newTestAggregator returns an Aggregator and registers Close to avoid leaking the native engine.
func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	a := NewAggregator()
	t.Cleanup(func() { a.Close() })
	return a
}

func TestAggregatorResult(t *testing.T) {
	aggregator := newTestAggregator(t)
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

	assertDurationNear(t, "P50", 10*time.Millisecond, result.Latency.P50)
	assertDurationNear(t, "P90", 30*time.Millisecond, result.Latency.P90)
	assertDurationNear(t, "P95", 30*time.Millisecond, result.Latency.P95)
	assertDurationNear(t, "P99", 30*time.Millisecond, result.Latency.P99)

	if len(result.ErrorCounts) != 1 || result.ErrorCounts["unknown_error"] != 1 {
		t.Fatalf("expected errorCounts unknown_error=1, got %+v", result.ErrorCounts)
	}
	if result.StatusCounts != nil {
		t.Fatalf("expected no non-zero status codes in this test, got %+v", result.StatusCounts)
	}
}

func TestAggregatorConcurrentRecord(t *testing.T) {
	aggregator := newTestAggregator(t)

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

	assertDurationNear(t, "P50", 20*time.Millisecond, result.Latency.P50)
	assertDurationNear(t, "P90", 40*time.Millisecond, result.Latency.P90)
	assertDurationNear(t, "P95", 40*time.Millisecond, result.Latency.P95)
	assertDurationNear(t, "P99", 40*time.Millisecond, result.Latency.P99)

	if result.ErrorCounts["unknown_error"] != 2 {
		t.Fatalf("expected errorCounts unknown_error=2, got %+v", result.ErrorCounts)
	}
}

func TestAggregatorResultIdempotent(t *testing.T) {
	a := newTestAggregator(t)
	a.Record(30*time.Millisecond, 0, nil)
	a.Record(10*time.Millisecond, 0, nil)

	r1 := a.Result(time.Second)
	r2 := a.Result(time.Second)
	if r1.Latency.P50 != r2.Latency.P50 {
		t.Fatalf("expected idempotent p50, got %v and %v", r1.Latency.P50, r2.Latency.P50)
	}
}

func TestAggregatorStatusCountsOnSuccess(t *testing.T) {
	a := newTestAggregator(t)
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

func TestAggregatorStatusCountsRecordedAlongsideError(t *testing.T) {
	a := newTestAggregator(t)
	a.Record(time.Millisecond, 500, &transport.HTTPStatusError{StatusCode: 500})

	r := a.Result(time.Second)
	if r.StatusCounts[500] != 1 {
		t.Fatalf("expected status 500 counted with error, got %+v", r.StatusCounts)
	}
	if r.ErrorCounts["http_status_error"] != 1 {
		t.Fatalf("unexpected errorCounts: %+v", r.ErrorCounts)
	}
	if r.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", r.Failed)
	}
}

func TestAggregatorErrorWithoutStatusCodeHasNoStatusCount(t *testing.T) {
	a := newTestAggregator(t)
	a.Record(time.Millisecond, 0, errors.New("network down"))

	r := a.Result(time.Second)
	if r.StatusCounts != nil {
		t.Fatalf("expected no status counts when code is 0, got %+v", r.StatusCounts)
	}
	if r.ErrorCounts["unknown_error"] != 1 || r.Failed != 1 {
		t.Fatalf("expected error path, failed=%d err=%+v", r.Failed, r.ErrorCounts)
	}
}

func TestAggregatorRPS(t *testing.T) {
	a := newTestAggregator(t)
	for range 10 {
		a.Record(time.Millisecond, 0, nil)
	}
	r := a.Result(2 * time.Second)
	want := 5.0
	if math.Abs(r.RPS-want) > 1e-9 {
		t.Fatalf("RPS: want %v, got %v", want, r.RPS)
	}

	r0 := a.Result(0)
	if r0.RPS != 0 {
		t.Fatalf("RPS with zero duration: want 0, got %v", r0.RPS)
	}
}

func TestAggregatorFailedWhenStatusAtLeast400WithoutErr(t *testing.T) {
	a := newTestAggregator(t)
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
	a := newTestAggregator(t)
	a.Record(time.Millisecond, 200, nil)
	r := a.Result(time.Second)
	r.StatusCounts[200] = 99

	r2 := a.Result(time.Second)
	if r2.StatusCounts[200] != 1 {
		t.Fatalf("internal statusCounts mutated via snapshot, got %+v", r2.StatusCounts)
	}
}

func TestAggregatorResultReturnsCopiedErrorCountsMap(t *testing.T) {
	a := newTestAggregator(t)
	a.Record(time.Millisecond, 0, errors.New("boom"))

	r := a.Result(time.Second)
	r.ErrorCounts["unknown_error"] = 99

	r2 := a.Result(time.Second)
	if r2.ErrorCounts["unknown_error"] != 1 {
		t.Fatalf("internal errorCounts mutated via snapshot, got %+v", r2.ErrorCounts)
	}
}

func TestAggregatorCloseIsIdempotent(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 0, nil)
	_ = a.Result(time.Second)
	a.Close()
	a.Close()
}

func TestAggregatorExtraPercentiles(t *testing.T) {
	a := NewAggregatorWithPercentiles([]float64{99.9, 150 /* out of range, ignored */})
	for i := 0; i < 1000; i++ {
		a.Record(time.Duration(i)*time.Millisecond, 200, nil)
	}
	r := a.Result(time.Second)

	if r.ExtraPercentiles == nil {
		t.Fatal("expected ExtraPercentiles to be populated")
	}
	if _, ok := r.ExtraPercentiles["p99.9"]; !ok {
		t.Fatalf("expected p99.9 key, got %v", r.ExtraPercentiles)
	}
	if _, ok := r.ExtraPercentiles["p150"]; ok {
		t.Fatal("out-of-range percentile should be ignored")
	}
	// p99.9 should be >= p99 and within [min,max].
	if r.ExtraPercentiles["p99.9"] < r.Latency.P99 {
		t.Errorf("p99.9 (%v) should be >= p99 (%v)", r.ExtraPercentiles["p99.9"], r.Latency.P99)
	}
	if r.ExtraPercentiles["p99.9"] > r.Latency.Max {
		t.Errorf("p99.9 (%v) should be <= max (%v)", r.ExtraPercentiles["p99.9"], r.Latency.Max)
	}
}

func TestAggregatorNoExtraPercentilesByDefault(t *testing.T) {
	a := NewAggregator()
	a.Record(time.Millisecond, 200, nil)
	if r := a.Result(time.Second); r.ExtraPercentiles != nil {
		t.Fatalf("expected nil ExtraPercentiles, got %v", r.ExtraPercentiles)
	}
}

func TestPercentileLabel(t *testing.T) {
	cases := map[float64]string{99: "p99", 99.9: "p99.9", 99.99: "p99.99", 50: "p50"}
	for p, want := range cases {
		if got := PercentileLabel(p); got != want {
			t.Errorf("PercentileLabel(%v) = %q, want %q", p, got, want)
		}
	}
}
