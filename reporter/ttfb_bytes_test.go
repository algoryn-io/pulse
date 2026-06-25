package reporter

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func sampleSnapshot() pulse.Snapshot {
	return pulse.Snapshot{
		RPS: 100, Total: 200, Failed: 4, Duration: time.Second,
		Latency:  pulse.LatencyStats{P50: 8 * time.Millisecond, P99: 30 * time.Millisecond, Mean: 10 * time.Millisecond},
		TTFB:     pulse.LatencyStats{P50: 3 * time.Millisecond, P99: 12 * time.Millisecond, Mean: 4 * time.Millisecond},
		BytesIn:  50000,
		BytesOut: 8000,
	}
}

func TestPrometheusExposesTTFBAndBytes(t *testing.T) {
	rep := &PrometheusReporter{}
	rep.OnSnapshot(sampleSnapshot())
	rec := httptest.NewRecorder()
	rep.serveMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := rec.Body.String()
	for _, want := range []string{
		`pulse_ttfb_ms{quantile="0.99"} 12`,
		"pulse_bytes_in_total 50000",
		"pulse_bytes_out_total 8000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prometheus output missing %q\n%s", want, out)
		}
	}
}

func TestInfluxDBLineIncludesTTFBAndBytes(t *testing.T) {
	rep := NewInfluxDBReporter(InfluxDBConfig{URL: "http://x", Bucket: "b", Org: "o"})
	line := rep.snapshotLine(sampleSnapshot())
	for _, want := range []string{"ttfb_p50_ms=3", "ttfb_p99_ms=12", "bytes_in=50000i", "bytes_out=8000i"} {
		if !strings.Contains(line, want) {
			t.Errorf("influx line missing %q: %s", want, line)
		}
	}
}

func TestDatadogSendsTTFBAndBytes(t *testing.T) {
	ln, addr := captureUDP(t)
	defer ln.Close()
	rep, err := NewDatadogReporter(DatadogConfig{Addr: addr})
	if err != nil {
		t.Fatalf("NewDatadogReporter: %v", err)
	}
	rep.OnSnapshot(sampleSnapshot())
	joined := strings.Join(receiveUDPDatagrams(ln, 100*time.Millisecond), "\n")
	for _, want := range []string{"pulse.ttfb.p99_ms:12|g", "pulse.bytes.in:50000|g", "pulse.bytes.out:8000|g"} {
		assertContains(t, joined, want)
	}
}

func TestCSVRowIncludesTTFBAndBytes(t *testing.T) {
	var buf bytes.Buffer
	rep := NewCSVReporter(&buf, CSVConfig{Snapshots: true})
	rep.OnSnapshot(sampleSnapshot())
	recs := parseCSV(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(recs))
	}
	header, row := recs[0], recs[1]
	idx := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		t.Fatalf("column %q not found", name)
		return -1
	}
	if row[idx("ttfb_p50_ms")] != "3" || row[idx("ttfb_p99_ms")] != "12" {
		t.Errorf("ttfb columns = %q/%q, want 3/12", row[idx("ttfb_p50_ms")], row[idx("ttfb_p99_ms")])
	}
	if row[idx("bytes_in")] != "50000" || row[idx("bytes_out")] != "8000" {
		t.Errorf("bytes columns = %q/%q, want 50000/8000", row[idx("bytes_in")], row[idx("bytes_out")])
	}
}
