package metrics

import (
	"testing"
	"time"
)

func TestRecordFullPopulatesTTFBAndBytes(t *testing.T) {
	a := NewAggregator()
	defer a.Close()

	for i := 0; i < 100; i++ {
		a.RecordFull(20*time.Millisecond, 8*time.Millisecond, 1000, 200, 200, nil)
	}
	res := a.Result(time.Second)

	if res.BytesIn != 100*1000 {
		t.Fatalf("BytesIn = %d, want %d", res.BytesIn, 100*1000)
	}
	if res.BytesOut != 100*200 {
		t.Fatalf("BytesOut = %d, want %d", res.BytesOut, 100*200)
	}
	if res.TTFB.P50 <= 0 {
		t.Fatalf("expected a positive TTFB P50, got %v", res.TTFB.P50)
	}
	// TTFB should be at or below total latency, and near the recorded 8ms.
	if res.TTFB.Max > res.Latency.Max {
		t.Fatalf("TTFB max %v exceeds latency max %v", res.TTFB.Max, res.Latency.Max)
	}
	if len(res.TTFBBuckets) != NumBuckets {
		t.Fatalf("TTFBBuckets len = %d, want %d", len(res.TTFBBuckets), NumBuckets)
	}
}

func TestRecordFullSkipsZeroTTFB(t *testing.T) {
	a := NewAggregator()
	defer a.Close()

	// No TTFB reported (e.g. non-HTTP scenario): TTFB stats stay zero, but byte
	// counters and latency still record.
	a.RecordFull(15*time.Millisecond, 0, 0, 0, 200, nil)
	res := a.Result(time.Second)

	if res.TTFB.P50 != 0 || res.TTFB.Max != 0 || res.TTFB.Mean != 0 {
		t.Fatalf("expected empty TTFB stats, got %+v", res.TTFB)
	}
	if res.TTFBBuckets != nil {
		t.Fatalf("expected nil TTFBBuckets when no TTFB recorded, got %d buckets", len(res.TTFBBuckets))
	}
	if res.Latency.P50 <= 0 {
		t.Fatal("latency should still be recorded")
	}
}

func TestRecordDelegatesToRecordFull(t *testing.T) {
	a := NewAggregator()
	defer a.Close()
	a.Record(10*time.Millisecond, 200, nil)
	res := a.Result(time.Second)
	if res.Total != 1 {
		t.Fatalf("Total = %d, want 1", res.Total)
	}
	if res.BytesIn != 0 || res.TTFB.P50 != 0 {
		t.Fatal("Record should not populate TTFB or bytes")
	}
}
