package merger_test

import (
	"testing"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/distributed/merger"
	"algoryn.io/pulse/internal/stats"
)

func TestMerge_SumsBytes(t *testing.T) {
	w1 := makeWorkerResult(100, 0, time.Millisecond)
	w1.BytesIn, w1.BytesOut = 10_000, 2_000
	w2 := makeWorkerResult(100, 0, time.Millisecond)
	w2.BytesIn, w2.BytesOut = 5_000, 1_000

	r := merger.Merge([]distributed.WorkerResult{w1, w2})
	if r.BytesIn != 15_000 {
		t.Errorf("BytesIn: want 15000, got %d", r.BytesIn)
	}
	if r.BytesOut != 3_000 {
		t.Errorf("BytesOut: want 3000, got %d", r.BytesOut)
	}
}

func TestMerge_AccurateBucketTTFB(t *testing.T) {
	// Worker 1: 500 TTFB samples at 1ms.
	e1 := stats.NewEngine()
	defer e1.Close()
	for range 500 {
		e1.RecordLatency(int64(time.Millisecond))
	}
	w1 := makeWorkerResult(500, 0, 10*time.Millisecond)
	w1.TTFBBuckets = e1.ExportBuckets()
	w1.TTFB = distributed.LatencyStats{Min: time.Millisecond, Mean: time.Millisecond, Max: time.Millisecond}

	// Worker 2: 500 TTFB samples at 50ms.
	e2 := stats.NewEngine()
	defer e2.Close()
	for range 500 {
		e2.RecordLatency(int64(50 * time.Millisecond))
	}
	w2 := makeWorkerResult(500, 0, 60*time.Millisecond)
	w2.TTFBBuckets = e2.ExportBuckets()
	w2.TTFB = distributed.LatencyStats{Min: 50 * time.Millisecond, Mean: 50 * time.Millisecond, Max: 50 * time.Millisecond}

	r := merger.Merge([]distributed.WorkerResult{w1, w2})

	if r.TTFB.P50 < 500*time.Microsecond || r.TTFB.P50 > 5*time.Millisecond {
		t.Errorf("TTFB P50: want ~1ms, got %v", r.TTFB.P50)
	}
	if r.TTFB.P99 < 25*time.Millisecond {
		t.Errorf("TTFB P99: want >= 25ms, got %v", r.TTFB.P99)
	}
	if r.TTFB.Min != time.Millisecond {
		t.Errorf("TTFB Min: want 1ms, got %v", r.TTFB.Min)
	}
	if r.TTFB.Max != 50*time.Millisecond {
		t.Errorf("TTFB Max: want 50ms, got %v", r.TTFB.Max)
	}
}

func TestMerge_NoTTFBLeavesZero(t *testing.T) {
	// Workers that report no TTFB (e.g. older workers) leave merged TTFB zero.
	w := makeWorkerResult(100, 0, time.Millisecond)
	r := merger.Merge([]distributed.WorkerResult{w})
	if r.TTFB.P50 != 0 || r.TTFB.Max != 0 {
		t.Fatalf("expected zero TTFB, got %+v", r.TTFB)
	}
}
