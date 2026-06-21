package reporter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func TestInfluxDBReporterOnSnapshotWritesLineProtocol(t *testing.T) {
	var body string
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rep := NewInfluxDBReporter(InfluxDBConfig{
		URL:    srv.URL,
		Token:  "test-token",
		Org:    "myorg",
		Bucket: "mybucket",
	})

	rep.OnSnapshot(pulse.Snapshot{
		RPS:       50.5,
		Total:     100,
		Failed:    5,
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Latency: pulse.LatencyStats{
			P50:  10 * time.Millisecond,
			P99:  40 * time.Millisecond,
			Mean: 15 * time.Millisecond,
		},
	})

	// Wait for async goroutine
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(body, "pulse,type=snapshot") {
		t.Fatalf("expected line protocol measurement, got %q", body)
	}
	if !strings.Contains(body, "rps=50.5") {
		t.Fatalf("expected rps field, got %q", body)
	}
	if !strings.Contains(body, "total=100i") {
		t.Fatalf("expected total field, got %q", body)
	}
	if !strings.Contains(body, "failed=5i") {
		t.Fatalf("expected failed field, got %q", body)
	}
	if authHeader != "Token test-token" {
		t.Fatalf("expected Authorization header, got %q", authHeader)
	}
}

func TestInfluxDBReporterOnResultWritesLineProtocol(t *testing.T) {
	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rep := NewInfluxDBReporter(InfluxDBConfig{
		URL:    srv.URL,
		Token:  "tok",
		Org:    "org",
		Bucket: "bkt",
	})

	rep.OnResult(pulse.Result{
		RPS:    80.0,
		Total:  400,
		Failed: 20,
	}, true)

	if !strings.Contains(body, "pulse,type=result") {
		t.Fatalf("expected result measurement, got %q", body)
	}
	if !strings.Contains(body, "passed=1i") {
		t.Fatalf("expected passed=1i, got %q", body)
	}
}

func TestInfluxDBReporterDefaultMeasurement(t *testing.T) {
	rep := NewInfluxDBReporter(InfluxDBConfig{})
	if rep.cfg.Measurement != "pulse" {
		t.Fatalf("expected default measurement 'pulse', got %q", rep.cfg.Measurement)
	}
}

func TestInfluxDBReporterCustomMeasurement(t *testing.T) {
	rep := NewInfluxDBReporter(InfluxDBConfig{Measurement: "loadtest"})
	if rep.cfg.Measurement != "loadtest" {
		t.Fatalf("expected 'loadtest', got %q", rep.cfg.Measurement)
	}
}

func TestInfluxDBReporterWriteURLFormat(t *testing.T) {
	rep := NewInfluxDBReporter(InfluxDBConfig{
		URL:    "http://influx:8086",
		Org:    "myorg",
		Bucket: "mybucket",
	})
	want := "http://influx:8086/api/v2/write?org=myorg&bucket=mybucket&precision=ns"
	if rep.writeURL != want {
		t.Fatalf("expected write URL %q, got %q", want, rep.writeURL)
	}
}

// InfluxDBReporter implements pulse.Reporter at compile time.
var _ pulse.Reporter = (*InfluxDBReporter)(nil)
