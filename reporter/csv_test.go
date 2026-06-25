package reporter

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func parseCSV(t *testing.T, s string) [][]string {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	return recs
}

func TestCSVReporterHeaderWrittenOnConstruction(t *testing.T) {
	var buf bytes.Buffer
	NewCSVReporter(&buf, CSVConfig{})
	recs := parseCSV(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("expected only a header row, got %d rows", len(recs))
	}
	if recs[0][0] != "kind" || recs[0][len(recs[0])-1] != "bytes_out" {
		t.Fatalf("unexpected header: %v", recs[0])
	}
	if len(recs[0]) != len(csvColumns) {
		t.Fatalf("header has %d columns, want %d", len(recs[0]), len(csvColumns))
	}
}

func TestCSVReporterSnapshotsDisabledByDefault(t *testing.T) {
	var buf bytes.Buffer
	rep := NewCSVReporter(&buf, CSVConfig{})
	rep.OnSnapshot(pulse.Snapshot{RPS: 10})

	recs := parseCSV(t, buf.String())
	if len(recs) != 1 { // header only
		t.Fatalf("snapshots disabled: expected 1 row (header), got %d", len(recs))
	}
}

func TestCSVReporterWritesSnapshotAndResultRows(t *testing.T) {
	var buf bytes.Buffer
	rep := NewCSVReporter(&buf, CSVConfig{Snapshots: true})

	rep.OnSnapshot(pulse.Snapshot{
		RPS:      50,
		Total:    100,
		Failed:   10,
		Duration: time.Second,
		Latency:  pulse.LatencyStats{P50: 10 * time.Millisecond, P99: 40 * time.Millisecond},
	})
	rep.OnResult(pulse.Result{
		RPS:      48,
		Total:    400,
		Failed:   20,
		Duration: 8 * time.Second,
		Latency:  pulse.LatencyStats{P99: 60 * time.Millisecond},
	}, true)

	recs := parseCSV(t, buf.String())
	if len(recs) != 3 { // header + snapshot + result
		t.Fatalf("expected 3 rows, got %d: %v", len(recs), recs)
	}

	col := func(name string) int {
		for i, c := range csvColumns {
			if c == name {
				return i
			}
		}
		t.Fatalf("unknown column %q", name)
		return -1
	}

	snap, res := recs[1], recs[2]
	if snap[col("kind")] != "snapshot" {
		t.Errorf("row 1 kind = %q, want snapshot", snap[col("kind")])
	}
	if snap[col("passed")] != "" {
		t.Errorf("snapshot passed should be empty, got %q", snap[col("passed")])
	}
	// error_rate = failed/total = 10/100 = 0.1
	if snap[col("error_rate")] != "0.1" {
		t.Errorf("snapshot error_rate = %q, want 0.1", snap[col("error_rate")])
	}
	if snap[col("p99_ms")] != "40" {
		t.Errorf("snapshot p99_ms = %q, want 40", snap[col("p99_ms")])
	}

	if res[col("kind")] != "result" {
		t.Errorf("row 2 kind = %q, want result", res[col("kind")])
	}
	if res[col("passed")] != "true" {
		t.Errorf("result passed = %q, want true", res[col("passed")])
	}
	if res[col("total")] != "400" {
		t.Errorf("result total = %q, want 400", res[col("total")])
	}
}
