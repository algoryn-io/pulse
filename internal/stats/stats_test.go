package stats

import "testing"

func TestExportImportBuckets_RoundTrip(t *testing.T) {
	src := NewEngine()
	defer src.Close()
	for _, ns := range []int64{1_000, 5_000, 10_000, 100_000, 1_000_000} {
		for range 100 {
			src.RecordLatency(ns)
		}
	}

	buckets := src.ExportBuckets()
	if len(buckets) != NumBuckets {
		t.Fatalf("ExportBuckets: want len %d, got %d", NumBuckets, len(buckets))
	}

	dst := NewEngine()
	defer dst.Close()
	dst.ImportBuckets(buckets)

	if dst.Total() != src.Total() {
		t.Fatalf("Total after import: want %d, got %d", src.Total(), dst.Total())
	}
	for _, p := range []float64{50, 90, 95, 99} {
		want := src.GetPercentile(p)
		got := dst.GetPercentile(p)
		if want != got {
			t.Errorf("P%.0f: src=%v dst=%v", p, want, got)
		}
	}
}

func TestImportBuckets_MergesTwoEngines(t *testing.T) {
	w1 := NewEngine()
	defer w1.Close()
	w2 := NewEngine()
	defer w2.Close()

	for range 500 {
		w1.RecordLatency(1_000_000) // 1ms
	}
	for range 500 {
		w2.RecordLatency(100_000_000) // 100ms
	}

	merged := NewEngine()
	defer merged.Close()
	merged.ImportBuckets(w1.ExportBuckets())
	merged.ImportBuckets(w2.ExportBuckets())

	if merged.Total() != 1000 {
		t.Fatalf("merged total: want 1000, got %d", merged.Total())
	}
	// P50 should be around the 1ms bucket (first 500 samples).
	p50 := merged.GetPercentile(50)
	if p50 < 500_000 || p50 > 5_000_000 {
		t.Errorf("P50 outside expected range: %v ns (want ~1ms)", p50)
	}
	// P99 should be in the 100ms bucket.
	p99 := merged.GetPercentile(99)
	if p99 < 50_000_000 {
		t.Errorf("P99 too low: %v ns (want >= 50ms)", p99)
	}
}

func TestImportBuckets_WrongLengthIsNoOp(t *testing.T) {
	e := NewEngine()
	defer e.Close()
	e.RecordLatency(1_000_000)
	before := e.Total()
	e.ImportBuckets(make([]uint64, NumBuckets-1)) // wrong length
	if e.Total() != before {
		t.Fatal("ImportBuckets with wrong len should be a no-op")
	}
}

func TestExportBuckets_ClosedEngineReturnsZeros(t *testing.T) {
	e := NewEngine()
	e.Close()
	buckets := e.ExportBuckets()
	if len(buckets) != NumBuckets {
		t.Fatalf("want len %d, got %d", NumBuckets, len(buckets))
	}
	for i, v := range buckets {
		if v != 0 {
			t.Fatalf("bucket[%d] = %d on closed engine, want 0", i, v)
		}
	}
}
