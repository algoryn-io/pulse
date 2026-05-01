package pulse

import (
	"testing"
	"time"

	fabricmetrics "algoryn.io/fabric/metrics"
	fabricv1 "algoryn.io/fabric/gen/go/fabric/v1"
)

func TestToRunEvent_BasicFields(t *testing.T) {
	startedAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	duration := 30 * time.Second

	result := Result{
		Total:    100,
		Failed:   5,
		Duration: duration,
		RPS:      20.0,
	}

	event := ToRunEvent(result, true, startedAt)

	if event.Source != fabricmetrics.SourcePulse {
		t.Errorf("Source = %q; want %q", event.Source, fabricmetrics.SourcePulse)
	}
	if event.Snapshot.Total != 100 {
		t.Errorf("Snapshot.Total = %d; want 100", event.Snapshot.Total)
	}
	if event.Snapshot.Failed != 5 {
		t.Errorf("Snapshot.Failed = %d; want 5", event.Snapshot.Failed)
	}
	if !event.Passed {
		t.Errorf("Passed = false; want true")
	}
	if !event.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v; want %v", event.StartedAt, startedAt)
	}
	wantEndedAt := startedAt.Add(duration)
	if !event.EndedAt.Equal(wantEndedAt) {
		t.Errorf("EndedAt = %v; want %v", event.EndedAt, wantEndedAt)
	}
}

func TestToRunEvent_ZeroStartedAt(t *testing.T) {
	result := Result{
		Total:    10,
		Failed:   1,
		Duration: 5 * time.Second,
		RPS:      2.0,
	}

	// Should not panic when startedAt is the zero value.
	event := ToRunEvent(result, false, time.Time{})

	if event.StartedAt.IsZero() {
		t.Errorf("StartedAt is zero; expected a best-effort approximation")
	}
}

func TestToRunEvent_ThresholdOutcomes(t *testing.T) {
	startedAt := time.Now()
	result := Result{
		Total:    50,
		Failed:   0,
		Duration: 10 * time.Second,
		RPS:      5.0,
		ThresholdOutcomes: []ThresholdOutcome{
			{Description: "error_rate < 0.05", Pass: true},
			{Description: "p99_latency < 200ms", Pass: false},
		},
	}

	event := ToRunEvent(result, false, startedAt)

	if len(event.Thresholds) != 2 {
		t.Fatalf("len(Thresholds) = %d; want 2", len(event.Thresholds))
	}
	if event.Thresholds[0].Description != "error_rate < 0.05" {
		t.Errorf("Thresholds[0].Description = %q; want %q", event.Thresholds[0].Description, "error_rate < 0.05")
	}
	if !event.Thresholds[0].Pass {
		t.Errorf("Thresholds[0].Pass = false; want true")
	}
	if event.Thresholds[1].Description != "p99_latency < 200ms" {
		t.Errorf("Thresholds[1].Description = %q; want %q", event.Thresholds[1].Description, "p99_latency < 200ms")
	}
	if event.Thresholds[1].Pass {
		t.Errorf("Thresholds[1].Pass = true; want false")
	}
}

func TestToRunEventProto_MetricSnapshot(t *testing.T) {
	startedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	result := Result{
		Total:        80,
		Failed:       2,
		Duration:     8 * time.Second,
		RPS:          10,
		StatusCounts: map[int]int64{200: 78, 500: 2},
		Latency: LatencyStats{
			Min:  1 * time.Millisecond,
			Mean: 5 * time.Millisecond,
			P50:  4 * time.Millisecond,
			P90:  10 * time.Millisecond,
			P95:  12 * time.Millisecond,
			P99:  20 * time.Millisecond,
			Max:  25 * time.Millisecond,
		},
	}

	pb := ToRunEventProto(result, true, startedAt)
	if pb.GetSource() != fabricmetrics.SourcePulse {
		t.Fatalf("source = %q", pb.GetSource())
	}
	snap := pb.GetSnapshot()
	if snap.GetTotal() != 80 || snap.GetFailed() != 2 {
		t.Fatalf("snapshot totals: %+v", snap)
	}
	if snap.GetStatusCodes()[200] != 78 || snap.GetStatusCodes()[500] != 2 {
		t.Fatalf("status codes: %v", snap.GetStatusCodes())
	}
	if !pb.GetPassed() {
		t.Fatal("expected passed")
	}
	if pb.GetStartedAt() == nil || pb.GetEndedAt() == nil {
		t.Fatal("expected timestamps")
	}
}

func TestToFabricRunEmit_RunIDMatchesAndServiceOnSnapshot(t *testing.T) {
	startedAt := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	result := Result{
		Total:    40,
		Failed:   4,
		Duration: 4 * time.Second,
		RPS:      10,
		Latency: LatencyStats{
			P99: 30 * time.Millisecond,
		},
	}

	emit := ToFabricRunEmit("checkout-api", result, false, startedAt)
	run := emit.RunEvent
	rc := emit.RunCompleted

	if run.GetId() == "" {
		t.Fatal("empty run id")
	}
	p := rc.GetRunCompleted()
	if p == nil {
		t.Fatal("missing run_completed payload")
	}
	if p.GetRunId() != run.GetId() {
		t.Fatalf("run id mismatch: event %q vs run %q", p.GetRunId(), run.GetId())
	}
	if p.GetService() != "checkout-api" {
		t.Fatalf("service = %q", p.GetService())
	}
	if run.GetSnapshot().GetService() != "checkout-api" {
		t.Fatalf("snapshot service = %q", run.GetSnapshot().GetService())
	}
	if rc.GetType() != fabricv1.EventType_EVENT_TYPE_RUN_COMPLETED {
		t.Fatalf("type = %v", rc.GetType())
	}
	if rc.GetTimestamp() == nil {
		t.Fatal("envelope timestamp")
	}
	if p.GetSummary() == nil {
		t.Fatal("summary")
	}
	wantErrRate := 0.1
	if p.GetSummary().GetErrorRate() != wantErrRate {
		t.Fatalf("error rate = %v want %v", p.GetSummary().GetErrorRate(), wantErrRate)
	}
}
