package merger_test

import (
	"testing"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/distributed/merger"
	"algoryn.io/pulse/internal/stats"
)

func TestMerge_Empty(t *testing.T) {
	r := merger.Merge(nil)
	if r.Total != 0 {
		t.Fatalf("empty merge: want Total=0, got %d", r.Total)
	}
}

func TestMerge_SingleWorker(t *testing.T) {
	w := makeWorkerResult(100, 5, 10*time.Millisecond)
	r := merger.Merge([]distributed.WorkerResult{w})
	if r.Total != 100 {
		t.Fatalf("Total: want 100, got %d", r.Total)
	}
	if r.Failed != 5 {
		t.Fatalf("Failed: want 5, got %d", r.Failed)
	}
}

func TestMerge_SumsCounters(t *testing.T) {
	w1 := makeWorkerResult(300, 10, 10*time.Millisecond)
	w2 := makeWorkerResult(200, 5, 10*time.Millisecond)
	r := merger.Merge([]distributed.WorkerResult{w1, w2})
	if r.Total != 500 {
		t.Fatalf("Total: want 500, got %d", r.Total)
	}
	if r.Failed != 15 {
		t.Fatalf("Failed: want 15, got %d", r.Failed)
	}
}

func TestMerge_MergesStatusCounts(t *testing.T) {
	w1 := makeWorkerResult(100, 0, 5*time.Millisecond)
	w1.StatusCounts = map[string]int64{"200": 80, "500": 20}
	w2 := makeWorkerResult(100, 0, 5*time.Millisecond)
	w2.StatusCounts = map[string]int64{"200": 90, "404": 10}

	r := merger.Merge([]distributed.WorkerResult{w1, w2})
	if r.StatusCounts[200] != 170 {
		t.Errorf("status 200: want 170, got %d", r.StatusCounts[200])
	}
	if r.StatusCounts[500] != 20 {
		t.Errorf("status 500: want 20, got %d", r.StatusCounts[500])
	}
	if r.StatusCounts[404] != 10 {
		t.Errorf("status 404: want 10, got %d", r.StatusCounts[404])
	}
}

func TestMerge_AccurateBucketLatency(t *testing.T) {
	// Worker 1: 500 requests at 1ms
	e1 := stats.NewEngine()
	defer e1.Close()
	for range 500 {
		e1.RecordLatency(int64(time.Millisecond))
	}
	w1 := makeWorkerResult(500, 0, time.Millisecond)
	w1.Buckets = e1.ExportBuckets()
	w1.Latency.P99 = time.Duration(e1.GetPercentile(99))

	// Worker 2: 500 requests at 100ms
	e2 := stats.NewEngine()
	defer e2.Close()
	for range 500 {
		e2.RecordLatency(int64(100 * time.Millisecond))
	}
	w2 := makeWorkerResult(500, 0, 100*time.Millisecond)
	w2.Buckets = e2.ExportBuckets()
	w2.Latency.P99 = time.Duration(e2.GetPercentile(99))

	r := merger.Merge([]distributed.WorkerResult{w1, w2})

	// P50 of merged distribution should be around 1ms (first 500 samples).
	if r.Latency.P50 < 500*time.Microsecond || r.Latency.P50 > 5*time.Millisecond {
		t.Errorf("P50: want ~1ms, got %v", r.Latency.P50)
	}
	// P99 should be around 100ms (tail of distribution).
	if r.Latency.P99 < 50*time.Millisecond {
		t.Errorf("P99: want >= 50ms, got %v", r.Latency.P99)
	}
}

func makeWorkerResult(total, failed int64, latency time.Duration) distributed.WorkerResult {
	buckets := make([]uint64, stats.NumBuckets)
	return distributed.WorkerResult{
		Total:        total,
		Failed:       failed,
		Duration:     latency * time.Duration(total),
		Scheduled:    total,
		Started:      total,
		Completed:    total,
		StatusCounts: map[string]int64{"200": total - failed, "500": failed},
		ErrorCounts:  map[string]int64{},
		Latency: distributed.LatencyStats{
			Min:  latency,
			Mean: latency,
			P50:  latency,
			P90:  latency,
			P95:  latency,
			P99:  latency,
			Max:  latency,
		},
		Buckets: buckets,
	}
}
