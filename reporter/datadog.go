package reporter

import (
	"fmt"
	"net"
	"strings"
	"time"

	pulse "algoryn.io/pulse"
)

// DatadogConfig holds connection parameters for the DogStatsD agent.
type DatadogConfig struct {
	// Addr is the DogStatsD UDP address. Defaults to "localhost:8125".
	Addr string
	// Tags is an optional list of global tags added to every metric,
	// in "key:value" format (e.g. []string{"env:prod", "service:api"}).
	Tags []string
	// Namespace is an optional prefix prepended to every metric name,
	// separated by a dot (e.g. "myapp" → "myapp.pulse.rps").
	Namespace string
}

// DatadogReporter sends Pulse metrics to a DogStatsD agent via UDP.
// OnSnapshot fires on each interval; OnResult fires after the run completes.
//
// Usage:
//
//	rep := reporter.NewDatadogReporter(reporter.DatadogConfig{
//	    Addr:      "localhost:8125",
//	    Tags:      []string{"env:staging", "service:checkout"},
//	    Namespace: "loadtest",
//	})
//	pulse.Run(pulse.Test{
//	    Config: pulse.Config{
//	        Reporters: []pulse.Reporter{rep},
//	        Reporting: pulse.ReportingConfig{Interval: time.Second},
//	        ...
//	    },
//	    Scenario: myScenario,
//	})
type DatadogReporter struct {
	cfg      DatadogConfig
	addr     *net.UDPAddr
	tagSuffix string // pre-formatted |#tag1,tag2
}

// NewDatadogReporter creates a DatadogReporter. Defaults to "localhost:8125"
// when cfg.Addr is empty.
func NewDatadogReporter(cfg DatadogConfig) (*DatadogReporter, error) {
	if cfg.Addr == "" {
		cfg.Addr = "localhost:8125"
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("reporter: invalid Datadog addr %q: %w", cfg.Addr, err)
	}
	tagSuffix := ""
	if len(cfg.Tags) > 0 {
		tagSuffix = "|#" + strings.Join(cfg.Tags, ",")
	}
	return &DatadogReporter{cfg: cfg, addr: addr, tagSuffix: tagSuffix}, nil
}

// OnSnapshot implements pulse.Reporter. Sends gauge metrics for the interval.
func (r *DatadogReporter) OnSnapshot(s pulse.Snapshot) {
	var errorRate float64
	if s.Total > 0 {
		errorRate = float64(s.Failed) / float64(s.Total)
	}
	r.send(
		r.gauge("rps", s.RPS),
		r.gauge("error_rate", errorRate),
		r.gauge("latency.mean_ms", msf(s.Latency.Mean)),
		r.gauge("latency.p50_ms", msf(s.Latency.P50)),
		r.gauge("latency.p99_ms", msf(s.Latency.P99)),
		r.count("requests.total", s.Total),
		r.count("requests.failed", s.Failed),
	)
}

// OnResult implements pulse.Reporter. Sends final gauges after the run.
func (r *DatadogReporter) OnResult(result pulse.Result, passed bool) {
	var errorRate float64
	if result.Total > 0 {
		errorRate = float64(result.Failed) / float64(result.Total)
	}
	passedVal := 0.0
	if passed {
		passedVal = 1.0
	}
	r.send(
		r.gauge("result.rps", result.RPS),
		r.gauge("result.error_rate", errorRate),
		r.gauge("result.latency.mean_ms", msf(result.Latency.Mean)),
		r.gauge("result.latency.p99_ms", msf(result.Latency.P99)),
		r.gauge("result.duration_ms", float64(result.Duration/time.Millisecond)),
		r.gauge("result.passed", passedVal),
		r.count("result.requests.total", result.Total),
		r.count("result.requests.failed", result.Failed),
	)
}

func (r *DatadogReporter) metricName(name string) string {
	if r.cfg.Namespace != "" {
		return r.cfg.Namespace + ".pulse." + name
	}
	return "pulse." + name
}

func (r *DatadogReporter) gauge(name string, value float64) string {
	return fmt.Sprintf("%s:%g|g%s", r.metricName(name), value, r.tagSuffix)
}

func (r *DatadogReporter) count(name string, value int64) string {
	return fmt.Sprintf("%s:%d|g%s", r.metricName(name), value, r.tagSuffix)
}

// send writes each datagram to the DogStatsD agent. Each metric is sent as a
// separate UDP datagram (max 8192 bytes, well within limits for these payloads).
func (r *DatadogReporter) send(datagrams ...string) {
	conn, err := net.DialUDP("udp", nil, r.addr)
	if err != nil {
		return
	}
	defer conn.Close()
	for _, dg := range datagrams {
		conn.Write([]byte(dg)) //nolint:errcheck
	}
}
