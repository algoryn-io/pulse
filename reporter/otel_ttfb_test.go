package reporter_test

import (
	"testing"
	"time"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/reporter"
)

func TestOTelRecordsTTFBAndBytes(t *testing.T) {
	provider, reader := newTestProvider()
	rep, err := reporter.NewOTelReporter(provider)
	if err != nil {
		t.Fatalf("NewOTelReporter: %v", err)
	}
	rep.OnSnapshot(pulse.Snapshot{
		RPS:      100,
		Total:    200,
		TTFB:     pulse.LatencyStats{P50: 3 * time.Millisecond, P99: 12 * time.Millisecond},
		BytesIn:  50000,
		BytesOut: 8000,
	})

	if got := collectGauge(t, reader, "pulse.ttfb.p99"); got != 12 {
		t.Errorf("pulse.ttfb.p99 = %v, want 12", got)
	}
	if got := collectInt64Gauge(t, reader, "pulse.bytes.in"); got != 50000 {
		t.Errorf("pulse.bytes.in = %d, want 50000", got)
	}
	if got := collectInt64Gauge(t, reader, "pulse.bytes.out"); got != 8000 {
		t.Errorf("pulse.bytes.out = %d, want 8000", got)
	}
}
