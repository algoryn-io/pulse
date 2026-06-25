package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
)

func TestWriteTextIncludesStressCapacity(t *testing.T) {
	res := pulse.Result{
		Total:    100,
		Duration: 2 * time.Second,
		Stress:   &pulse.StressResult{MaxHealthyRPS: 450, FailedAtRPS: 500, Reason: "p99_latency", Failed: true},
	}
	var buf bytes.Buffer
	writeText(&buf, res, false)
	out := buf.String()
	if !strings.Contains(out, "failed at 500 RPS") || !strings.Contains(out, "max healthy 450 RPS") {
		t.Fatalf("stress capacity line missing: %q", out)
	}
	if !strings.Contains(out, "p99_latency") {
		t.Fatalf("stress reason missing: %q", out)
	}
}

func TestWriteTextStressNoFailure(t *testing.T) {
	res := pulse.Result{
		Total:    100,
		Duration: 2 * time.Second,
		Stress:   &pulse.StressResult{MaxHealthyRPS: 800, Failed: false},
	}
	var buf bytes.Buffer
	writeText(&buf, res, false)
	if !strings.Contains(buf.String(), "no failure within bounds — sustained 800 RPS") {
		t.Fatalf("expected no-failure capacity line, got %q", buf.String())
	}
}

func TestToJSONResultIncludesStress(t *testing.T) {
	res := pulse.Result{
		Total:    10,
		Duration: time.Second,
		Stress:   &pulse.StressResult{MaxHealthyRPS: 300, FailedAtRPS: 350, Reason: "error_rate", Failed: true},
	}
	out := toJSONResult(res, true)
	if out.Stress == nil {
		t.Fatal("expected stress block in JSON result")
	}
	if out.Stress.FailedAtRPS != 350 || out.Stress.Reason != "error_rate" || !out.Stress.Failed {
		t.Fatalf("unexpected stress JSON: %+v", out.Stress)
	}
}

func TestToJSONResultNoStressIsOmitted(t *testing.T) {
	out := toJSONResult(pulse.Result{Total: 1, Duration: time.Second}, true)
	if out.Stress != nil {
		t.Fatalf("expected nil stress block, got %+v", out.Stress)
	}
}
