package reporter

import (
	"net"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func captureUDP(t *testing.T) (*net.UDPConn, string) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	return conn.(*net.UDPConn), conn.LocalAddr().String()
}

func receiveUDPDatagrams(conn *net.UDPConn, timeout time.Duration) []string {
	conn.SetReadDeadline(time.Now().Add(timeout)) //nolint:errcheck
	var received []string
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		received = append(received, string(buf[:n]))
	}
	return received
}

func TestDatadogReporterOnSnapshotSendsGauges(t *testing.T) {
	ln, addr := captureUDP(t)
	defer ln.Close()

	rep, err := NewDatadogReporter(DatadogConfig{Addr: addr})
	if err != nil {
		t.Fatalf("NewDatadogReporter: %v", err)
	}

	rep.OnSnapshot(pulse.Snapshot{
		RPS:    120.5,
		Total:  240,
		Failed: 12,
		Latency: pulse.LatencyStats{
			Mean: 10 * time.Millisecond,
			P50:  8 * time.Millisecond,
			P99:  25 * time.Millisecond,
		},
	})

	datagrams := receiveUDPDatagrams(ln, 100*time.Millisecond)
	joined := strings.Join(datagrams, "\n")

	assertContains(t, joined, "pulse.rps:120.5|g")
	assertContains(t, joined, "pulse.requests.total:240|g")
	assertContains(t, joined, "pulse.requests.failed:12|g")
	assertContains(t, joined, "pulse.error_rate:0.05|g")
}

func TestDatadogReporterOnResultSendsFinalGauges(t *testing.T) {
	ln, addr := captureUDP(t)
	defer ln.Close()

	rep, err := NewDatadogReporter(DatadogConfig{Addr: addr})
	if err != nil {
		t.Fatalf("NewDatadogReporter: %v", err)
	}

	rep.OnResult(pulse.Result{
		RPS:      60.0,
		Total:    300,
		Failed:   0,
		Duration: 5 * time.Second,
	}, true)

	datagrams := receiveUDPDatagrams(ln, 100*time.Millisecond)
	joined := strings.Join(datagrams, "\n")

	assertContains(t, joined, "pulse.result.rps:60|g")
	assertContains(t, joined, "pulse.result.passed:1|g")
	assertContains(t, joined, "pulse.result.duration_ms:5000|g")
}

func TestDatadogReporterTagsAppendedToMetrics(t *testing.T) {
	ln, addr := captureUDP(t)
	defer ln.Close()

	rep, err := NewDatadogReporter(DatadogConfig{
		Addr: addr,
		Tags: []string{"env:prod", "service:api"},
	})
	if err != nil {
		t.Fatalf("NewDatadogReporter: %v", err)
	}

	rep.OnSnapshot(pulse.Snapshot{RPS: 10})

	datagrams := receiveUDPDatagrams(ln, 100*time.Millisecond)
	joined := strings.Join(datagrams, "\n")

	assertContains(t, joined, "|#env:prod,service:api")
}

func TestDatadogReporterNamespacePrefixesMetrics(t *testing.T) {
	ln, addr := captureUDP(t)
	defer ln.Close()

	rep, err := NewDatadogReporter(DatadogConfig{
		Addr:      addr,
		Namespace: "myapp",
	})
	if err != nil {
		t.Fatalf("NewDatadogReporter: %v", err)
	}

	rep.OnSnapshot(pulse.Snapshot{RPS: 5})

	datagrams := receiveUDPDatagrams(ln, 100*time.Millisecond)
	joined := strings.Join(datagrams, "\n")

	assertContains(t, joined, "myapp.pulse.rps:5|g")
}

func TestDatadogReporterDefaultAddr(t *testing.T) {
	rep, err := NewDatadogReporter(DatadogConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.addr.String() != "127.0.0.1:8125" {
		t.Fatalf("expected 127.0.0.1:8125, got %s", rep.addr)
	}
}

func TestNewDatadogReporterRejectsInvalidAddr(t *testing.T) {
	_, err := NewDatadogReporter(DatadogConfig{Addr: "not-valid-addr:::"})
	if err == nil {
		t.Fatal("expected error for invalid addr")
	}
}

// DatadogReporter implements pulse.Reporter at compile time.
var _ pulse.Reporter = (*DatadogReporter)(nil)
