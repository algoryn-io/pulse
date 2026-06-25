package pulse

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSnapshotToDTOIncludesTTFBAndBytes(t *testing.T) {
	dto := snapshotToDTO(Snapshot{
		RPS:      100,
		Duration: time.Second,
		TTFB:     LatencyStats{P50: 3 * time.Millisecond, P99: 12 * time.Millisecond},
		BytesIn:  50000,
		BytesOut: 8000,
	})
	if dto.TTFB.P99Ns != int64(12*time.Millisecond) {
		t.Errorf("TTFB.P99Ns = %d, want %d", dto.TTFB.P99Ns, int64(12*time.Millisecond))
	}
	if dto.BytesIn != 50000 || dto.BytesOut != 8000 {
		t.Errorf("bytes = %d/%d, want 50000/8000", dto.BytesIn, dto.BytesOut)
	}
	b, _ := json.Marshal(dto)
	for _, want := range []string{`"ttfb"`, `"bytes_in":50000`, `"bytes_out":8000`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("snapshot JSON missing %q: %s", want, b)
		}
	}
}

func TestResultToDTOIncludesTTFBAndBytes(t *testing.T) {
	dto := resultToDTO(Result{
		RPS:      100,
		Duration: time.Second,
		TTFB:     LatencyStats{P50: 3 * time.Millisecond, P99: 12 * time.Millisecond},
		BytesIn:  50000,
		BytesOut: 8000,
	}, true)
	if dto.TTFB.P99Ns != int64(12*time.Millisecond) {
		t.Errorf("TTFB.P99Ns = %d", dto.TTFB.P99Ns)
	}
	if dto.BytesIn != 50000 || dto.BytesOut != 8000 {
		t.Errorf("bytes = %d/%d", dto.BytesIn, dto.BytesOut)
	}
}
