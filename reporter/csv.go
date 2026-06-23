package reporter

import (
	"encoding/csv"
	"io"
	"strconv"
	"sync"
	"time"

	pulse "algoryn.io/pulse"
)

// csvColumns is the stable column order written by CSVReporter. New columns may
// be appended in future versions, but existing columns will not be renamed or
// reordered.
var csvColumns = []string{
	"kind", "rps", "error_rate",
	"total", "failed", "dropped", "dropped_rate", "max_active",
	"min_ms", "mean_ms", "p50_ms", "p90_ms", "p95_ms", "p99_ms", "max_ms",
	"duration_ms", "passed",
}

// CSVConfig configures a CSVReporter.
type CSVConfig struct {
	// Snapshots, when true, writes one row per reporting interval (kind=snapshot)
	// in addition to the final summary row. When false, only the final
	// kind=result row is written.
	Snapshots bool
}

// CSVReporter writes Pulse metrics to an io.Writer in CSV format. It emits a
// header row on construction, an optional row per snapshot, and a final summary
// row when the run completes. The caller owns the underlying writer (e.g. an
// *os.File) and is responsible for closing it.
//
// Usage:
//
//	f, _ := os.Create("run.csv")
//	defer f.Close()
//	rep := reporter.NewCSVReporter(f, reporter.CSVConfig{Snapshots: true})
//	pulse.Run(pulse.Test{
//	    Config: pulse.Config{
//	        Reporters: []pulse.Reporter{rep},
//	        Reporting: pulse.ReportingConfig{Interval: time.Second},
//	    },
//	    Scenario: myScenario,
//	})
type CSVReporter struct {
	cfg CSVConfig

	mu sync.Mutex // serializes writes (OnSnapshot runs on a background goroutine)
	w  *csv.Writer
}

// NewCSVReporter creates a CSVReporter that writes to w and immediately emits
// the header row.
func NewCSVReporter(w io.Writer, cfg CSVConfig) *CSVReporter {
	r := &CSVReporter{cfg: cfg, w: csv.NewWriter(w)}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.w.Write(csvColumns)
	r.w.Flush()
	return r
}

// OnSnapshot implements pulse.Reporter. Writes one row per interval when
// CSVConfig.Snapshots is true; otherwise it is a no-op.
func (r *CSVReporter) OnSnapshot(s pulse.Snapshot) {
	if !r.cfg.Snapshots {
		return
	}
	row := metricRow("snapshot", s.RPS, s.Total, s.Failed, s.Dropped, s.DroppedRate,
		s.MaxActive, s.Latency, s.Duration, "")
	r.writeRow(row)
}

// OnResult implements pulse.Reporter. Writes the final summary row.
func (r *CSVReporter) OnResult(result pulse.Result, passed bool) {
	row := metricRow("result", result.RPS, result.Total, result.Failed, result.Dropped,
		result.DroppedRate, result.MaxActive, result.Latency, result.Duration,
		strconv.FormatBool(passed))
	r.writeRow(row)
}

func (r *CSVReporter) writeRow(row []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.w.Write(row)
	r.w.Flush()
}

// metricRow formats a single CSV record in csvColumns order. passed is left
// empty for snapshot rows.
func metricRow(kind string, rps float64, total, failed, dropped int64, droppedRate float64,
	maxActive int64, lat pulse.LatencyStats, duration time.Duration, passed string) []string {

	var errorRate float64
	if total > 0 {
		errorRate = float64(failed) / float64(total)
	}
	return []string{
		kind,
		strconv.FormatFloat(rps, 'g', -1, 64),
		strconv.FormatFloat(errorRate, 'g', -1, 64),
		strconv.FormatInt(total, 10),
		strconv.FormatInt(failed, 10),
		strconv.FormatInt(dropped, 10),
		strconv.FormatFloat(droppedRate, 'g', -1, 64),
		strconv.FormatInt(maxActive, 10),
		strconv.FormatFloat(msf(lat.Min), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.Mean), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.P50), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.P90), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.P95), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.P99), 'g', -1, 64),
		strconv.FormatFloat(msf(lat.Max), 'g', -1, 64),
		strconv.FormatFloat(msf(duration), 'g', -1, 64),
		passed,
	}
}

// CSVReporter implements pulse.Reporter at compile time.
var _ pulse.Reporter = (*CSVReporter)(nil)
