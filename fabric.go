package pulse

import (
	"fmt"
	"time"

	fab "algoryn.io/fabric"
	fabricevents "algoryn.io/fabric/events"
	fabricv1 "algoryn.io/fabric/gen/go/fabric/v1"
	fabricmetrics "algoryn.io/fabric/metrics"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// FabricRunEmit bundles the protobuf shapes Pulse emits for downstream Algoryn tools.
// RunEvent carries the full MetricSnapshot; RunCompleted is the fabric.v1.Event envelope
// (EVENT_TYPE_RUN_COMPLETED) for consumers such as Beacon.
type FabricRunEmit struct {
	RunEvent     *fabricv1.RunEvent
	RunCompleted *fabricv1.Event
}

// newRunID returns a simple unique identifier for a Pulse run.
// It avoids external dependencies by using the nanosecond clock.
func newRunID() string {
	return fmt.Sprintf("pulse-%d", time.Now().UnixNano())
}

func newFabricEventID() string {
	return fmt.Sprintf("pulse-event-%d", time.Now().UnixNano())
}

// ToRunEvent converts a Pulse Result into a fabric metrics.RunEvent,
// making it compatible with other Algoryn ecosystem tools.
// The startedAt parameter should be the time the run began.
// If zero, time.Now() minus result.Duration is used as a best-effort approximation.
// Snapshot.Service is empty; use ToFabricRunEmit when you need a service name on the snapshot.
func ToRunEvent(result Result, passed bool, startedAt time.Time) fabricmetrics.RunEvent {
	return toRunEventWithID(newRunID(), "", result, passed, startedAt)
}

func toRunEventWithID(runID, service string, result Result, passed bool, startedAt time.Time) fabricmetrics.RunEvent {
	if startedAt.IsZero() {
		startedAt = time.Now().Add(-result.Duration)
	}
	endedAt := startedAt.Add(result.Duration)

	thresholds := make([]fabricmetrics.ThresholdResult, len(result.ThresholdOutcomes))
	for i, t := range result.ThresholdOutcomes {
		thresholds[i] = fabricmetrics.ThresholdResult{
			Description: t.Description,
			Pass:        t.Pass,
		}
	}

	snapshot := fabricmetrics.MetricSnapshot{
		Source:      fabricmetrics.SourcePulse,
		Service:     service,
		Timestamp:   startedAt,
		Window:      result.Duration,
		Total:       result.Total,
		Failed:      result.Failed,
		RPS:         result.RPS,
		StatusCodes: result.StatusCounts,
		Errors:      result.ErrorCounts,
		Latency: fabricmetrics.LatencyStats{
			Min:  result.Latency.Min,
			Mean: result.Latency.Mean,
			P50:  result.Latency.P50,
			P90:  result.Latency.P90,
			P95:  result.Latency.P95,
			P99:  result.Latency.P99,
			Max:  result.Latency.Max,
		},
	}

	return fabricmetrics.RunEvent{
		ID:         runID,
		Source:     fabricmetrics.SourcePulse,
		StartedAt:  startedAt,
		EndedAt:    endedAt,
		Snapshot:   snapshot,
		Thresholds: thresholds,
		Passed:     passed,
	}
}

// ToRunEventProto converts a Pulse Result into fabric.v1.RunEvent (binary contract).
// Timestamps are set from startedAt / endedAt via Fabric conversion helpers (timestamppb).
func ToRunEventProto(result Result, passed bool, startedAt time.Time) *fabricv1.RunEvent {
	return fab.RunEventToProto(ToRunEvent(result, passed, startedAt))
}

// ToFabricRunEmit builds a matched pair: full RunEvent proto and a RunCompleted fabric Event
// sharing the same run id. RunCompleted uses timestamppb.Now() for the envelope timestamp.
func ToFabricRunEmit(service string, result Result, passed bool, startedAt time.Time) FabricRunEmit {
	runID := newRunID()
	legacy := toRunEventWithID(runID, service, result, passed, startedAt)
	runProto := fab.RunEventToProto(legacy)

	payload := fabricevents.RunCompletedPayload{
		RunID:    runID,
		Service:  service,
		Passed:   passed,
		Duration: result.Duration,
		Summary:  runSummaryFromResult(result),
	}

	completed := &fabricv1.Event{
		Id:        newFabricEventID(),
		Type:      fabricv1.EventType_EVENT_TYPE_RUN_COMPLETED,
		Source:    fabricmetrics.SourcePulse,
		Timestamp: timestamppb.Now(),
		Payload: &fabricv1.Event_RunCompleted{
			RunCompleted: fab.RunCompletedPayloadToProto(&payload),
		},
	}

	return FabricRunEmit{RunEvent: runProto, RunCompleted: completed}
}

func runSummaryFromResult(result Result) fabricevents.RunSummary {
	var errorRate float64
	if result.Total > 0 {
		errorRate = float64(result.Failed) / float64(result.Total)
	}
	p99ms := float64(result.Latency.P99) / float64(time.Millisecond)
	return fabricevents.RunSummary{
		Total:     result.Total,
		Failed:    result.Failed,
		RPS:       result.RPS,
		ErrorRate: errorRate,
		P99Ms:     p99ms,
	}
}
