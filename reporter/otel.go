package reporter

import (
	"context"
	"time"

	pulse "algoryn.io/pulse"
	"go.opentelemetry.io/otel/metric"
)

// OTelReporter exports Pulse metrics to an OpenTelemetry MeterProvider.
// It records gauges on each reporting interval and after the run completes.
// The caller is responsible for configuring and shutting down the provider.
//
// Usage:
//
//	provider := setupOTelProvider() // your OTLP exporter + SDK setup
//	rep, err := reporter.NewOTelReporter(provider)
//	if err != nil { ... }
//	pulse.Run(pulse.Test{
//	    Config: pulse.Config{
//	        Reporters: []pulse.Reporter{rep},
//	        Reporting: pulse.ReportingConfig{Interval: time.Second},
//	        ...
//	    },
//	    Scenario: myScenario,
//	})
type OTelReporter struct {
	rps       metric.Float64Gauge
	errorRate metric.Float64Gauge
	p50       metric.Float64Gauge
	p90       metric.Float64Gauge
	p95       metric.Float64Gauge
	p99       metric.Float64Gauge
	total     metric.Int64Gauge
	failed    metric.Int64Gauge
}

// NewOTelReporter creates an OTelReporter using the given MeterProvider.
// Returns an error if any instrument cannot be created.
func NewOTelReporter(provider metric.MeterProvider) (*OTelReporter, error) {
	meter := provider.Meter("algoryn.io/pulse")

	rps, err := meter.Float64Gauge("pulse.rps",
		metric.WithDescription("Current requests per second"),
		metric.WithUnit("{req}/s"))
	if err != nil {
		return nil, err
	}
	errorRate, err := meter.Float64Gauge("pulse.error_rate",
		metric.WithDescription("Fraction of failed requests [0,1]"),
		metric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	p50, err := meter.Float64Gauge("pulse.latency.p50",
		metric.WithDescription("P50 request latency"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	p90, err := meter.Float64Gauge("pulse.latency.p90",
		metric.WithDescription("P90 request latency"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	p95, err := meter.Float64Gauge("pulse.latency.p95",
		metric.WithDescription("P95 request latency"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	p99, err := meter.Float64Gauge("pulse.latency.p99",
		metric.WithDescription("P99 request latency"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	total, err := meter.Int64Gauge("pulse.requests.total",
		metric.WithDescription("Total requests completed"))
	if err != nil {
		return nil, err
	}
	failed, err := meter.Int64Gauge("pulse.requests.failed",
		metric.WithDescription("Total failed requests"))
	if err != nil {
		return nil, err
	}

	return &OTelReporter{
		rps: rps, errorRate: errorRate,
		p50: p50, p90: p90, p95: p95, p99: p99,
		total: total, failed: failed,
	}, nil
}

// OnSnapshot implements pulse.Reporter. Records live metrics at each interval.
func (r *OTelReporter) OnSnapshot(s pulse.Snapshot) {
	ctx := context.Background()
	r.record(ctx, s.RPS, s.Latency, s.Total, s.Failed)
}

// OnResult implements pulse.Reporter. Records final metrics after the run.
func (r *OTelReporter) OnResult(result pulse.Result, _ bool) {
	ctx := context.Background()
	r.record(ctx, result.RPS, result.Latency, result.Total, result.Failed)
}

func (r *OTelReporter) record(ctx context.Context, rps float64, lat pulse.LatencyStats, total, failed int64) {
	var errRate float64
	if total > 0 {
		errRate = float64(failed) / float64(total)
	}
	r.rps.Record(ctx, rps)
	r.errorRate.Record(ctx, errRate)
	r.p50.Record(ctx, ms(lat.P50))
	r.p90.Record(ctx, ms(lat.P90))
	r.p95.Record(ctx, ms(lat.P95))
	r.p99.Record(ctx, ms(lat.P99))
	r.total.Record(ctx, total)
	r.failed.Record(ctx, failed)
}

func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
