package engine

import (
	"errors"
	"testing"
	"time"
)

func TestSnapshotCollectorBuildsIntervalMetrics(t *testing.T) {
	startedAt := time.Date(2026, time.May, 31, 12, 0, 0, 0, time.UTC)
	collector := newSnapshotCollector(startedAt, 100*time.Millisecond)
	t.Cleanup(collector.close)

	collector.recordScheduled(startedAt.Add(10 * time.Millisecond))
	collector.recordStarted(startedAt.Add(11*time.Millisecond), 1)
	collector.recordCompleted(startedAt.Add(40*time.Millisecond), 29*time.Millisecond, 200, nil)

	collector.recordScheduled(startedAt.Add(120 * time.Millisecond))
	collector.recordDropped(startedAt.Add(120 * time.Millisecond))
	collector.recordScheduled(startedAt.Add(130 * time.Millisecond))
	collector.recordStarted(startedAt.Add(131*time.Millisecond), 2)
	collector.recordCompleted(startedAt.Add(180*time.Millisecond), 49*time.Millisecond, 500, errors.New("failed"))

	snapshots := collector.snapshots(250 * time.Millisecond)
	if len(snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(snapshots))
	}

	first := snapshots[0]
	if first.StartedAt != startedAt || first.Duration != 100*time.Millisecond {
		t.Fatalf("unexpected first snapshot window: %+v", first)
	}
	if first.Scheduled != 1 || first.Started != 1 || first.Dropped != 0 || first.Completed != 1 {
		t.Fatalf("unexpected first snapshot counters: %+v", first)
	}
	if first.StatusCounts[200] != 1 || first.Latency.Mean != 29*time.Millisecond {
		t.Fatalf("unexpected first snapshot metrics: %+v", first)
	}

	second := snapshots[1]
	if second.Scheduled != 2 || second.Started != 1 || second.Dropped != 1 || second.DroppedRate != 0.5 {
		t.Fatalf("unexpected second snapshot load fidelity: %+v", second)
	}
	if second.Completed != 1 || second.Failed != 1 || second.StatusCounts[500] != 1 {
		t.Fatalf("unexpected second snapshot completion metrics: %+v", second)
	}
	if second.ErrorCounts["unknown_error"] != 1 || second.MaxActive != 2 {
		t.Fatalf("unexpected second snapshot error metrics: %+v", second)
	}

	third := snapshots[2]
	if third.Duration != 50*time.Millisecond {
		t.Fatalf("expected trailing duration 50ms, got %v", third.Duration)
	}
	if third.Scheduled != 0 || third.Completed != 0 {
		t.Fatalf("expected empty trailing snapshot, got %+v", third)
	}
}

func TestSnapshotCollectorDisabledWhenIntervalIsZero(t *testing.T) {
	if collector := newSnapshotCollector(time.Now(), 0); collector != nil {
		t.Fatalf("expected nil collector, got %+v", collector)
	}
}
