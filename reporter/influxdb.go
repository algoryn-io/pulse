package reporter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	pulse "algoryn.io/pulse"
)

// InfluxDBConfig holds connection parameters for InfluxDB v2.
type InfluxDBConfig struct {
	// URL is the InfluxDB base URL, e.g. "http://localhost:8086".
	URL string
	// Token is the InfluxDB API token (Authorization: Token <token>).
	Token string
	// Org is the organisation name or ID.
	Org string
	// Bucket is the target bucket.
	Bucket string
	// Measurement is the line-protocol measurement name. Defaults to "pulse".
	Measurement string
}

// InfluxDBReporter writes Pulse metrics to InfluxDB v2 using the line protocol.
// OnSnapshot fires an async write on each interval; OnResult fires a final write.
//
// Usage:
//
//	rep := reporter.NewInfluxDBReporter(reporter.InfluxDBConfig{
//	    URL:    "http://localhost:8086",
//	    Token:  "my-token",
//	    Org:    "my-org",
//	    Bucket: "pulse",
//	})
//	pulse.Run(pulse.Test{
//	    Config: pulse.Config{
//	        Reporters: []pulse.Reporter{rep},
//	        Reporting: pulse.ReportingConfig{Interval: time.Second},
//	        ...
//	    },
//	    Scenario: myScenario,
//	})
type InfluxDBReporter struct {
	cfg    InfluxDBConfig
	client *http.Client
	writeURL string
}

// NewInfluxDBReporter creates an InfluxDBReporter with default HTTP client settings.
func NewInfluxDBReporter(cfg InfluxDBConfig) *InfluxDBReporter {
	if cfg.Measurement == "" {
		cfg.Measurement = "pulse"
	}
	return &InfluxDBReporter{
		cfg:      cfg,
		client:   &http.Client{Timeout: 5 * time.Second},
		writeURL: fmt.Sprintf("%s/api/v2/write?org=%s&bucket=%s&precision=ns", cfg.URL, cfg.Org, cfg.Bucket),
	}
}

// OnSnapshot implements pulse.Reporter. Fires an async line-protocol write.
func (r *InfluxDBReporter) OnSnapshot(s pulse.Snapshot) {
	line := r.snapshotLine(s)
	go r.post(line) //nolint:errcheck
}

// OnResult implements pulse.Reporter. Writes the final result synchronously.
func (r *InfluxDBReporter) OnResult(result pulse.Result, passed bool) {
	line := r.resultLine(result, passed)
	r.post(line) //nolint:errcheck
}

func (r *InfluxDBReporter) snapshotLine(s pulse.Snapshot) string {
	var errorRate float64
	if s.Total > 0 {
		errorRate = float64(s.Failed) / float64(s.Total)
	}
	ts := s.StartedAt.UnixNano()
	if ts == 0 {
		ts = time.Now().UnixNano()
	}
	return fmt.Sprintf(
		"%s,type=snapshot rps=%g,error_rate=%g,p50_ms=%g,p99_ms=%g,mean_ms=%g,ttfb_p50_ms=%g,ttfb_p99_ms=%g,bytes_in=%di,bytes_out=%di,total=%di,failed=%di %d",
		r.cfg.Measurement,
		s.RPS, errorRate,
		msf(s.Latency.P50), msf(s.Latency.P99), msf(s.Latency.Mean),
		msf(s.TTFB.P50), msf(s.TTFB.P99),
		s.BytesIn, s.BytesOut,
		s.Total, s.Failed,
		ts,
	)
}

func (r *InfluxDBReporter) resultLine(result pulse.Result, passed bool) string {
	var errorRate float64
	if result.Total > 0 {
		errorRate = float64(result.Failed) / float64(result.Total)
	}
	passedInt := 0
	if passed {
		passedInt = 1
	}
	return fmt.Sprintf(
		"%s,type=result rps=%g,error_rate=%g,p50_ms=%g,p99_ms=%g,mean_ms=%g,ttfb_p50_ms=%g,ttfb_p99_ms=%g,bytes_in=%di,bytes_out=%di,total=%di,failed=%di,passed=%di %d",
		r.cfg.Measurement,
		result.RPS, errorRate,
		msf(result.Latency.P50), msf(result.Latency.P99), msf(result.Latency.Mean),
		msf(result.TTFB.P50), msf(result.TTFB.P99),
		result.BytesIn, result.BytesOut,
		result.Total, result.Failed, passedInt,
		time.Now().UnixNano(),
	)
}

func (r *InfluxDBReporter) post(line string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.writeURL, bytes.NewBufferString(line))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+r.cfg.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
