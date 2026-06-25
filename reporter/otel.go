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
	ttfbP50   metric.Float64Gauge
	ttfbP99   metric.Float64Gauge
	bytesIn   metric.Int64Gauge
	bytesOut  metric.Int64Gauge
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
	ttfbP50, err := meter.Float64Gauge("pulse.ttfb.p50",
		metric.WithDescription("P50 time-to-first-byte"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	ttfbP99, err := meter.Float64Gauge("pulse.ttfb.p99",
		metric.WithDescription("P99 time-to-first-byte"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	bytesIn, err := meter.Int64Gauge("pulse.bytes.in",
		metric.WithDescription("Response bytes read"),
		metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	bytesOut, err := meter.Int64Gauge("pulse.bytes.out",
		metric.WithDescription("Request bytes sent"),
		metric.WithUnit("By"))
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
		ttfbP50: ttfbP50, ttfbP99: ttfbP99,
		bytesIn: bytesIn, bytesOut: bytesOut,
		total: total, failed: failed,
	}, nil
}

// OnSnapshot implements pulse.Reporter. Records live metrics at each interval.
func (r *OTelReporter) OnSnapshot(s pulse.Snapshot) {
	ctx := context.Background()
	r.record(ctx, s.RPS, s.Latency, s.TTFB, s.Total, s.Failed, s.BytesIn, s.BytesOut)
}

// OnResult implements pulse.Reporter. Records final metrics after the run.
func (r *OTelReporter) OnResult(result pulse.Result, _ bool) {
	ctx := context.Background()
	r.record(ctx, result.RPS, result.Latency, result.TTFB, result.Total, result.Failed, result.BytesIn, result.BytesOut)
}

func (r *OTelReporter) record(ctx context.Context, rps float64, lat, ttfb pulse.LatencyStats, total, failed, bytesIn, bytesOut int64) {
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
	r.ttfbP50.Record(ctx, ms(ttfb.P50))
	r.ttfbP99.Record(ctx, ms(ttfb.P99))
	r.bytesIn.Record(ctx, bytesIn)
	r.bytesOut.Record(ctx, bytesOut)
	r.total.Record(ctx, total)
	r.failed.Record(ctx, failed)
}

func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
