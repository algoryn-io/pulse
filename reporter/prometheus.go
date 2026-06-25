// Package reporter provides plug-in metric exporters for Pulse test runs.
// Implement the pulse.Reporter interface to add custom exporters.
package reporter

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	pulse "algoryn.io/pulse"
)

// PrometheusReporter exposes Pulse metrics in the Prometheus text exposition
// format at /metrics. It starts an HTTP server on the configured address and
// updates metrics on each snapshot interval and after the run completes.
//
// Usage:
//
//	rep := reporter.NewPrometheusReporter(ctx, ":2112")
//	pulse.Run(pulse.Test{
//	    Config: pulse.Config{
//	        Reporters:  []pulse.Reporter{rep},
//	        Reporting:  pulse.ReportingConfig{Interval: time.Second},
//	        ...
//	    },
//	    Scenario: myScenario,
//	})
type PrometheusReporter struct {
	mu     sync.RWMutex
	snap   pulse.Snapshot
	result pulse.Result
	done   bool
	server *http.Server
}

// NewPrometheusReporter creates a PrometheusReporter that serves /metrics on
// addr. The server shuts down when ctx is cancelled.
func NewPrometheusReporter(ctx context.Context, addr string) *PrometheusReporter {
	r := &PrometheusReporter{}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", r.serveMetrics)
	r.server = &http.Server{Addr: addr, Handler: mux}
	go r.server.ListenAndServe() //nolint:errcheck
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.server.Shutdown(shutCtx) //nolint:errcheck
	}()
	return r
}

// OnSnapshot implements pulse.Reporter. Updates the latest live metrics.
func (r *PrometheusReporter) OnSnapshot(s pulse.Snapshot) {
	r.mu.Lock()
	r.snap = s
	r.mu.Unlock()
}

// OnResult implements pulse.Reporter. Updates metrics with the final result.
func (r *PrometheusReporter) OnResult(result pulse.Result, _ bool) {
	r.mu.Lock()
	r.result = result
	r.done = true
	r.mu.Unlock()
}

func (r *PrometheusReporter) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	r.mu.RLock()
	snap := r.snap
	result := r.result
	done := r.done
	r.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	if done {
		writePrometheusMetrics(w, result.RPS, result.Latency, result.TTFB, result.Total, result.Failed, result.BytesIn, result.BytesOut)
	} else {
		writePrometheusMetrics(w, snap.RPS, snap.Latency, snap.TTFB, snap.Total, snap.Failed, snap.BytesIn, snap.BytesOut)
	}
}

func writePrometheusMetrics(w http.ResponseWriter, rps float64, lat, ttfb pulse.LatencyStats, total, failed, bytesIn, bytesOut int64) {
	var errorRate float64
	if total > 0 {
		errorRate = float64(failed) / float64(total)
	}

	fmt.Fprintf(w, "# HELP pulse_rps Current requests per second\n")
	fmt.Fprintf(w, "# TYPE pulse_rps gauge\n")
	fmt.Fprintf(w, "pulse_rps %g\n", rps)

	fmt.Fprintf(w, "# HELP pulse_error_rate Fraction of failed requests [0,1]\n")
	fmt.Fprintf(w, "# TYPE pulse_error_rate gauge\n")
	fmt.Fprintf(w, "pulse_error_rate %g\n", errorRate)

	fmt.Fprintf(w, "# HELP pulse_latency_ms Request latency in milliseconds\n")
	fmt.Fprintf(w, "# TYPE pulse_latency_ms gauge\n")
	fmt.Fprintf(w, "pulse_latency_ms{quantile=\"mean\"} %g\n", msf(lat.Mean))
	fmt.Fprintf(w, "pulse_latency_ms{quantile=\"0.50\"} %g\n", msf(lat.P50))
	fmt.Fprintf(w, "pulse_latency_ms{quantile=\"0.90\"} %g\n", msf(lat.P90))
	fmt.Fprintf(w, "pulse_latency_ms{quantile=\"0.95\"} %g\n", msf(lat.P95))
	fmt.Fprintf(w, "pulse_latency_ms{quantile=\"0.99\"} %g\n", msf(lat.P99))

	// TTFB is reported only when the transport measured it (HTTP scenarios).
	if ttfb.P99 > 0 || ttfb.Mean > 0 {
		fmt.Fprintf(w, "# HELP pulse_ttfb_ms Time-to-first-byte in milliseconds\n")
		fmt.Fprintf(w, "# TYPE pulse_ttfb_ms gauge\n")
		fmt.Fprintf(w, "pulse_ttfb_ms{quantile=\"mean\"} %g\n", msf(ttfb.Mean))
		fmt.Fprintf(w, "pulse_ttfb_ms{quantile=\"0.50\"} %g\n", msf(ttfb.P50))
		fmt.Fprintf(w, "pulse_ttfb_ms{quantile=\"0.90\"} %g\n", msf(ttfb.P90))
		fmt.Fprintf(w, "pulse_ttfb_ms{quantile=\"0.95\"} %g\n", msf(ttfb.P95))
		fmt.Fprintf(w, "pulse_ttfb_ms{quantile=\"0.99\"} %g\n", msf(ttfb.P99))
	}

	fmt.Fprintf(w, "# HELP pulse_requests_total Total requests completed\n")
	fmt.Fprintf(w, "# TYPE pulse_requests_total counter\n")
	fmt.Fprintf(w, "pulse_requests_total %d\n", total)

	fmt.Fprintf(w, "# HELP pulse_failed_total Failed requests\n")
	fmt.Fprintf(w, "# TYPE pulse_failed_total counter\n")
	fmt.Fprintf(w, "pulse_failed_total %d\n", failed)

	fmt.Fprintf(w, "# HELP pulse_bytes_in_total Response bytes read\n")
	fmt.Fprintf(w, "# TYPE pulse_bytes_in_total counter\n")
	fmt.Fprintf(w, "pulse_bytes_in_total %d\n", bytesIn)

	fmt.Fprintf(w, "# HELP pulse_bytes_out_total Request bytes sent\n")
	fmt.Fprintf(w, "# TYPE pulse_bytes_out_total counter\n")
	fmt.Fprintf(w, "pulse_bytes_out_total %d\n", bytesOut)
}

func msf(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
