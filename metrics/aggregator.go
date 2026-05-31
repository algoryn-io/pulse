package metrics

import (
	"math"
	"sync"
	"time"

	"algoryn.io/pulse/internal/stats"
)

// Result contains the aggregated execution metrics for a run.
type Result struct {
	Total        int64
	Failed       int64
	Duration     time.Duration
	RPS          float64
	Scheduled    int64
	Started      int64
	Dropped      int64
	DroppedRate  float64
	Completed    int64
	MaxActive    int64
	Latency      LatencyStats
	StatusCounts map[int]int64
	ErrorCounts  map[string]int64
	Snapshots    []Snapshot
}

// Snapshot contains metrics observed during one reporting interval.
// Scheduled, started, and dropped values are attributed when the arrival is
// handled. Completed requests and latency values are attributed when execution
// finishes.
type Snapshot struct {
	StartedAt    time.Time
	Duration     time.Duration
	Total        int64
	Failed       int64
	RPS          float64
	Scheduled    int64
	Started      int64
	Dropped      int64
	DroppedRate  float64
	Completed    int64
	MaxActive    int64
	Latency      LatencyStats
	StatusCounts map[int]int64
	ErrorCounts  map[string]int64
}

// Aggregator collects execution metrics for the MVP.
type Aggregator struct {
	mu           sync.Mutex
	closed       bool
	engine       *stats.Engine
	total        int64
	failed       int64
	meanNanos    float64
	minLatency   time.Duration
	maxLatency   time.Duration
	statusCounts map[int]int64
	errorCounts  map[string]int64
}

// NewAggregator creates an empty metrics aggregator and allocates the native
// stats engine used for low-memory percentile estimates.
func NewAggregator() *Aggregator {
	return &Aggregator{
		engine:       stats.NewEngine(),
		statusCounts: make(map[int]int64),
		errorCounts:  make(map[string]int64),
	}
}

// Record stores metrics for a single execution.
func (a *Aggregator) Record(latency time.Duration, statusCode int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.engine == nil {
		return
	}

	a.total++
	if statusCode != 0 {
		a.statusCounts[statusCode]++
	}
	if err != nil {
		a.failed++
		a.errorCounts[normalizeError(err)]++
	} else if statusCode >= 400 {
		a.failed++
	}

	a.meanNanos += (float64(latency.Nanoseconds()) - a.meanNanos) / float64(a.total)
	if a.total == 1 || latency < a.minLatency {
		a.minLatency = latency
	}
	if latency > a.maxLatency {
		a.maxLatency = latency
	}

	a.engine.RecordLatency(latency.Nanoseconds())
}

// Result returns the aggregated metrics snapshot. Multiple calls with no
// intervening Close() return consistent percentile estimates (same engine state).
// Call Close() when the test run is finished to release the native engine.
func (a *Aggregator) Result(duration time.Duration) Result {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := Result{
		Total:    a.total,
		Failed:   a.failed,
		Duration: duration,
	}
	if duration > 0 {
		result.RPS = float64(a.total) / duration.Seconds()
	}

	if a.total == 0 || a.engine == nil {
		return result
	}

	result.Latency = LatencyStats{
		Min:  a.minLatency,
		Max:  a.maxLatency,
		Mean: time.Duration(math.Round(a.meanNanos)),
		P50:  clampDuration(nsToDuration(a.engine.GetPercentile(50)), a.minLatency, a.maxLatency),
		P90:  clampDuration(nsToDuration(a.engine.GetPercentile(90)), a.minLatency, a.maxLatency),
		P95:  clampDuration(nsToDuration(a.engine.GetPercentile(95)), a.minLatency, a.maxLatency),
		P99:  clampDuration(nsToDuration(a.engine.GetPercentile(99)), a.minLatency, a.maxLatency),
	}

	result.StatusCounts = copyInt64MapByInt(a.statusCounts)
	result.ErrorCounts = copyInt64MapByString(a.errorCounts)

	return result
}

// Close releases the C++ stats engine. Safe to call more than once. Do not
// use this aggregator for recording after Close.
func (a *Aggregator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	if a.engine != nil {
		a.engine.Close()
		a.engine = nil
	}
	a.closed = true
}

func nsToDuration(v float64) time.Duration {
	if v <= 0 {
		return 0
	}
	// time.Duration is int64 nanoseconds.
	r := int64(math.Round(v))
	if r < 0 {
		return 0
	}
	if r > math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(r)
}

// clampDuration keeps a percentile within observed min–max. Logarithmic-bucket
// interpolation can land slightly outside the true sample extrema.
func clampDuration(d, minD, maxD time.Duration) time.Duration {
	if d < minD {
		return minD
	}
	if d > maxD {
		return maxD
	}
	return d
}

func copyInt64MapByInt(src map[int]int64) map[int]int64 {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[int]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func copyInt64MapByString(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
