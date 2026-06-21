package reporter_test

import (
	"context"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/reporter"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newTestProvider returns a MeterProvider backed by an in-memory reader so
// tests can inspect recorded measurements without a real OTLP backend.
func newTestProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return provider, reader
}

func collectGauge(t *testing.T, reader *sdkmetric.ManualReader, name string) float64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			data, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("metric %q is not a Float64Gauge", name)
			}
			if len(data.DataPoints) == 0 {
				t.Fatalf("no data points for %q", name)
			}
			return data.DataPoints[len(data.DataPoints)-1].Value
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

func collectInt64Gauge(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			data, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("metric %q is not an Int64Gauge", name)
			}
			if len(data.DataPoints) == 0 {
				t.Fatalf("no data points for %q", name)
			}
			return data.DataPoints[len(data.DataPoints)-1].Value
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

// compile-time interface check
var _ pulse.Reporter = (*reporter.OTelReporter)(nil)

func TestNewOTelReporterCreatesInstruments(t *testing.T) {
	provider, _ := newTestProvider()
	_, err := reporter.NewOTelReporter(provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOTelReporterOnSnapshotRecordsRPS(t *testing.T) {
	provider, reader := newTestProvider()
	rep, _ := reporter.NewOTelReporter(provider)

	rep.OnSnapshot(pulse.Snapshot{RPS: 42.5})

	got := collectGauge(t, reader, "pulse.rps")
	if got != 42.5 {
		t.Fatalf("expected pulse.rps=42.5, got %g", got)
	}
}

func TestOTelReporterOnSnapshotRecordsLatency(t *testing.T) {
	provider, reader := newTestProvider()
	rep, _ := reporter.NewOTelReporter(provider)

	rep.OnSnapshot(pulse.Snapshot{
		Latency: pulse.LatencyStats{
			P50: 10 * time.Millisecond,
			P90: 20 * time.Millisecond,
			P95: 30 * time.Millisecond,
			P99: 50 * time.Millisecond,
		},
	})

	if got := collectGauge(t, reader, "pulse.latency.p50"); got != 10 {
		t.Fatalf("expected p50=10ms, got %g", got)
	}
	if got := collectGauge(t, reader, "pulse.latency.p99"); got != 50 {
		t.Fatalf("expected p99=50ms, got %g", got)
	}
}

func TestOTelReporterOnSnapshotRecordsErrorRate(t *testing.T) {
	provider, reader := newTestProvider()
	rep, _ := reporter.NewOTelReporter(provider)

	rep.OnSnapshot(pulse.Snapshot{Total: 100, Failed: 5})

	got := collectGauge(t, reader, "pulse.error_rate")
	if got != 0.05 {
		t.Fatalf("expected error_rate=0.05, got %g", got)
	}
}

func TestOTelReporterOnResultRecordsTotals(t *testing.T) {
	provider, reader := newTestProvider()
	rep, _ := reporter.NewOTelReporter(provider)

	rep.OnResult(pulse.Result{Total: 1000, Failed: 20, RPS: 33.3}, true)

	if got := collectInt64Gauge(t, reader, "pulse.requests.total"); got != 1000 {
		t.Fatalf("expected total=1000, got %d", got)
	}
	if got := collectInt64Gauge(t, reader, "pulse.requests.failed"); got != 20 {
		t.Fatalf("expected failed=20, got %d", got)
	}
}

func TestOTelReporterZeroTotalProducesZeroErrorRate(t *testing.T) {
	provider, reader := newTestProvider()
	rep, _ := reporter.NewOTelReporter(provider)

	rep.OnSnapshot(pulse.Snapshot{Total: 0, Failed: 0})

	got := collectGauge(t, reader, "pulse.error_rate")
	if got != 0 {
		t.Fatalf("expected error_rate=0 when total=0, got %g", got)
	}
}
