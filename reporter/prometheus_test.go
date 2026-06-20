package reporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func TestPrometheusReporterOnSnapshotUpdatesMetrics(t *testing.T) {
	rep := &PrometheusReporter{}

	rep.OnSnapshot(pulse.Snapshot{
		RPS:    100.5,
		Total:  200,
		Failed: 10,
		Latency: pulse.LatencyStats{
			Mean: 15 * time.Millisecond,
			P50:  12 * time.Millisecond,
			P90:  20 * time.Millisecond,
			P95:  22 * time.Millisecond,
			P99:  30 * time.Millisecond,
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)

	body := rec.Body.String()

	assertContains(t, body, "pulse_rps 100.5")
	assertContains(t, body, "pulse_requests_total 200")
	assertContains(t, body, "pulse_failed_total 10")
	assertContains(t, body, `pulse_latency_ms{quantile="0.99"} 30`)
	assertContains(t, body, `pulse_latency_ms{quantile="mean"} 15`)
}

func TestPrometheusReporterOnResultSwitchesToFinalMetrics(t *testing.T) {
	rep := &PrometheusReporter{}

	rep.OnSnapshot(pulse.Snapshot{RPS: 50, Total: 100})
	rep.OnResult(pulse.Result{
		RPS:    75.0,
		Total:  500,
		Failed: 5,
		Latency: pulse.LatencyStats{
			P99:  45 * time.Millisecond,
			Mean: 20 * time.Millisecond,
		},
	}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)

	body := rec.Body.String()

	// After OnResult, final result takes precedence
	assertContains(t, body, "pulse_rps 75")
	assertContains(t, body, "pulse_requests_total 500")
}

func TestPrometheusReporterContentType(t *testing.T) {
	rep := &PrometheusReporter{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
}

func TestPrometheusReporterEmptyStateReturnsZeroMetrics(t *testing.T) {
	rep := &PrometheusReporter{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)

	body := rec.Body.String()
	assertContains(t, body, "pulse_rps 0")
	assertContains(t, body, "pulse_requests_total 0")
}

func TestPrometheusReporterServerShutdownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rep := NewPrometheusReporter(ctx, ":0")

	// Confirm server is running by hitting serveMetrics directly
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	cancel()
	time.Sleep(50 * time.Millisecond) // allow shutdown goroutine to run

	// Server should be shut down; trying to connect should fail
	resp, err := http.Get("http://" + rep.server.Addr + "/metrics")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected connection error after server shutdown, got response")
	}
}

func TestPrometheusReporterErrorRateCalculation(t *testing.T) {
	rep := &PrometheusReporter{}

	rep.OnSnapshot(pulse.Snapshot{Total: 100, Failed: 25})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rep.serveMetrics(rec, req)

	body := rec.Body.String()
	assertContains(t, body, "pulse_error_rate 0.25")
}

// PrometheusReporter implements pulse.Reporter at compile time.
var _ pulse.Reporter = (*PrometheusReporter)(nil)

func assertContains(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Fatalf("expected body to contain %q\nbody:\n%s", substr, body)
	}
}

// unused import guard
var _ = io.Discard
