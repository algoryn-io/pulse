package metrics

import (
	"errors"
	"testing"
	"time"
)

// BenchmarkAggregatorRecord measures the cost of a single Record call under
// exclusive mutex ownership. This is the hot path for every completed request
// in a load test run: histogram update, running mean, min/max tracking, and
// status/error map writes.
//
// Run with:
//
//	go test -bench=BenchmarkAggregatorRecord -benchtime=5s ./metrics/
func BenchmarkAggregatorRecord(b *testing.B) {
	a := NewAggregator()
	defer a.Close()
	latency := 10 * time.Millisecond
	b.ResetTimer()
	for range b.N {
		a.Record(latency, 200, nil)
	}
}

// BenchmarkAggregatorRecordWithError measures Record when the call includes
// error accounting: the error string is normalised and written to the error
// map in addition to the standard latency/count bookkeeping.
func BenchmarkAggregatorRecordWithError(b *testing.B) {
	a := NewAggregator()
	defer a.Close()
	latency := 10 * time.Millisecond
	err := errors.New("connection refused")
	b.ResetTimer()
	for range b.N {
		a.Record(latency, 0, err)
	}
}

// BenchmarkAggregatorRecord_Parallel measures Record throughput when many
// goroutines call it concurrently — the realistic condition during a high-RPS
// load test. It reveals mutex contention and is the baseline for any future
// sharding or lock-free optimisation.
func BenchmarkAggregatorRecord_Parallel(b *testing.B) {
	a := NewAggregator()
	defer a.Close()
	latency := 10 * time.Millisecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			a.Record(latency, 200, nil)
		}
	})
}

// BenchmarkAggregatorResult measures the cost of computing a Result snapshot
// from accumulated metrics. This is called once per reporting interval (and
// once at the end of the run), so it is not on the critical path — but it
// does hold the mutex while reading the histogram.
func BenchmarkAggregatorResult(b *testing.B) {
	a := NewAggregator()
	defer a.Close()
	latency := 10 * time.Millisecond

	// Pre-populate the aggregator so Result exercises the full histogram path.
	for range 1000 {
		a.Record(latency, 200, nil)
	}

	b.ResetTimer()
	for range b.N {
		_ = a.Result(time.Second)
	}
}
