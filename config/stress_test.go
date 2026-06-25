package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeStressYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stress.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadMapsStressConfig(t *testing.T) {
	path := writeStressYAML(t, ""+
		"phases:\n"+
		"  - type: constant\n"+
		"    duration: 1m\n"+
		"    arrivalRate: 50\n"+
		"target:\n"+
		"  method: GET\n"+
		"  url: http://127.0.0.1:8080\n"+
		"reporting:\n"+
		"  interval: 1s\n"+
		"stress:\n"+
		"  stepRPS: 25\n"+
		"  maxRPS: 1000\n"+
		"  maxErrorRate: 0.05\n"+
		"  maxP99: 250ms\n"+
		"  sustainedIntervals: 2\n"+
		"  minRequests: 20\n")

	test, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := test.Config.Stress
	if s.StepRPS != 25 || s.MaxRPS != 1000 {
		t.Errorf("step/max = %d/%d, want 25/1000", s.StepRPS, s.MaxRPS)
	}
	if s.MaxErrorRate != 0.05 || s.MaxP99 != 250*time.Millisecond {
		t.Errorf("thresholds = %v/%v", s.MaxErrorRate, s.MaxP99)
	}
	if s.SustainedIntervals != 2 || s.MinRequests != 20 {
		t.Errorf("sustained/minReq = %d/%d", s.SustainedIntervals, s.MinRequests)
	}
}

func TestLoadStressRequiresInterval(t *testing.T) {
	path := writeStressYAML(t, ""+
		"phases:\n"+
		"  - type: constant\n"+
		"    duration: 1m\n"+
		"    arrivalRate: 50\n"+
		"target:\n"+
		"  method: GET\n"+
		"  url: http://127.0.0.1:8080\n"+
		"stress:\n"+
		"  stepRPS: 25\n"+
		"  maxP99: 250ms\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "Stress requires Reporting.Interval") {
		t.Fatalf("expected a stress-interval error, got %v", err)
	}
}

func TestLoadStressDistributedRejected(t *testing.T) {
	path := writeStressYAML(t, ""+
		"phases:\n"+
		"  - type: constant\n"+
		"    duration: 1m\n"+
		"    arrivalRate: 50\n"+
		"target:\n"+
		"  method: GET\n"+
		"  url: http://127.0.0.1:8080\n"+
		"reporting:\n"+
		"  interval: 1s\n"+
		"workers: [\"127.0.0.1:9300\"]\n"+
		"stress:\n"+
		"  stepRPS: 25\n"+
		"  maxP99: 250ms\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "not supported in distributed mode") {
		t.Fatalf("expected a stress-distributed error, got %v", err)
	}
}
