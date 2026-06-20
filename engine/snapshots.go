package engine

import (
	"sync"
	"time"

	"algoryn.io/pulse/metrics"
)

type snapshotWindow struct {
	aggregator *metrics.Aggregator
	scheduled  int64
	started    int64
	dropped    int64
	maxActive  int64
}

type snapshotCollector struct {
	mu        sync.Mutex
	startedAt time.Time
	interval  time.Duration
	windows   map[int64]*snapshotWindow
}

func newSnapshotCollector(startedAt time.Time, interval time.Duration) *snapshotCollector {
	if interval <= 0 {
		return nil
	}
	return &snapshotCollector{
		startedAt: startedAt,
		interval:  interval,
		windows:   make(map[int64]*snapshotWindow),
	}
}

func (c *snapshotCollector) recordScheduled(at time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowLocked(at).scheduled++
}

func (c *snapshotCollector) recordStarted(at time.Time, active int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	window := c.windowLocked(at)
	window.started++
	if active > window.maxActive {
		window.maxActive = active
	}
}

func (c *snapshotCollector) recordDropped(at time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowLocked(at).dropped++
}

func (c *snapshotCollector) recordCompleted(at time.Time, latency time.Duration, statusCode int, err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	window := c.windowLocked(at)
	if window.aggregator == nil {
		window.aggregator = metrics.NewAggregator()
	}
	window.aggregator.Record(latency, statusCode, err)
}

func (c *snapshotCollector) snapshots(duration time.Duration) []metrics.Snapshot {
	if c == nil || duration <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	count := int64((duration-1)/c.interval + 1)
	result := make([]metrics.Snapshot, count)
	var active int64
	for i := int64(0); i < count; i++ {
		windowDuration := c.interval
		if remaining := duration - time.Duration(i)*c.interval; remaining < windowDuration {
			windowDuration = remaining
		}
		snapshot := metrics.Snapshot{
			StartedAt: c.startedAt.Add(time.Duration(i) * c.interval),
			Duration:  windowDuration,
		}
		if window := c.windows[i]; window != nil {
			snapshot.Scheduled = window.scheduled
			snapshot.Started = window.started
			snapshot.Dropped = window.dropped
			if snapshot.Scheduled > 0 {
				snapshot.DroppedRate = float64(snapshot.Dropped) / float64(snapshot.Scheduled)
			}
			snapshot.MaxActive = window.maxActive
			if active > snapshot.MaxActive {
				snapshot.MaxActive = active
			}
			if window.aggregator != nil {
				aggregated := window.aggregator.Result(windowDuration)
				snapshot.Total = aggregated.Total
				snapshot.Failed = aggregated.Failed
				snapshot.RPS = aggregated.RPS
				snapshot.Completed = aggregated.Total
				snapshot.Latency = aggregated.Latency
				snapshot.StatusCounts = aggregated.StatusCounts
				snapshot.ErrorCounts = aggregated.ErrorCounts
			}
			active += window.started - snapshot.Completed
		} else if active > 0 {
			snapshot.MaxActive = active
		}
		result[i] = snapshot
	}
	return result
}

// liveSnapshot returns the most recently completed interval window as a
// Snapshot. It is safe to call concurrently with record* methods. If no
// completed window exists yet (the run has been active for less than one
// interval), it returns a zero-value Snapshot.
func (c *snapshotCollector) liveSnapshot(now time.Time) metrics.Snapshot {
	if c == nil {
		return metrics.Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Current (possibly incomplete) window index.
	currentIdx := int64(now.Sub(c.startedAt) / c.interval)
	if currentIdx == 0 {
		return metrics.Snapshot{} // first window not yet complete
	}
	lastIdx := currentIdx - 1

	window := c.windows[lastIdx]
	if window == nil {
		return metrics.Snapshot{}
	}

	snap := metrics.Snapshot{
		StartedAt:   c.startedAt.Add(time.Duration(lastIdx) * c.interval),
		Duration:    c.interval,
		Scheduled:   window.scheduled,
		Started:     window.started,
		Dropped:     window.dropped,
		MaxActive:   window.maxActive,
	}
	if snap.Scheduled > 0 {
		snap.DroppedRate = float64(snap.Dropped) / float64(snap.Scheduled)
	}
	if window.aggregator != nil {
		aggregated := window.aggregator.Result(c.interval)
		snap.Total = aggregated.Total
		snap.Failed = aggregated.Failed
		snap.RPS = aggregated.RPS
		snap.Completed = aggregated.Total
		snap.Latency = aggregated.Latency
		snap.StatusCounts = aggregated.StatusCounts
		snap.ErrorCounts = aggregated.ErrorCounts
	}
	return snap
}

func (c *snapshotCollector) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, window := range c.windows {
		if window.aggregator != nil {
			window.aggregator.Close()
		}
	}
}

func (c *snapshotCollector) windowLocked(at time.Time) *snapshotWindow {
	index := int64(at.Sub(c.startedAt) / c.interval)
	if index < 0 {
		index = 0
	}
	window := c.windows[index]
	if window == nil {
		window = &snapshotWindow{}
		c.windows[index] = window
	}
	return window
}
