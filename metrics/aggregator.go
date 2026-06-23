package metrics

import (
	"math"
	"strconv"
	"sync"
	"time"

	"algoryn.io/pulse/internal/stats"
)

// NumBuckets is re-exported from internal/stats so callers in the distributed
// layer can size bucket arrays without importing internal/stats directly.
const NumBuckets = stats.NumBuckets

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
	// ExtraPercentiles holds additional latency percentiles requested via
	// configuration, keyed by label (e.g. "p99.9"). Nil when none were
	// requested. The standard P50/P90/P95/P99 always live in Latency.
	ExtraPercentiles map[string]time.Duration
	// Buckets contains the raw histogram bucket counts (stats.NumBuckets = 800 values).
	// Populated by Aggregator.Result() for use by distributed coordinators that need
	// to merge per-worker histograms before computing cross-worker percentiles.
	// Callers that only need latency percentiles can ignore this field.
	Buckets []uint64
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
	percentiles  []float64 // extra percentiles to report, in (0,100)
}

// NewAggregator creates an empty metrics aggregator and allocates the native
// stats engine used for low-memory percentile estimates.
func NewAggregator() *Aggregator {
	return NewAggregatorWithPercentiles(nil)
}

// NewAggregatorWithPercentiles is like NewAggregator but also computes the given
// extra percentiles (values in (0,100), e.g. 99.9) in Result.ExtraPercentiles.
// Out-of-range values are ignored.
func NewAggregatorWithPercentiles(percentiles []float64) *Aggregator {
	var ps []float64
	for _, p := range percentiles {
		if p > 0 && p < 100 {
			ps = append(ps, p)
		}
	}
	return &Aggregator{
		engine:       stats.NewEngine(),
		statusCounts: make(map[int]int64),
		errorCounts:  make(map[string]int64),
		percentiles:  ps,
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

	if len(a.percentiles) > 0 {
		extra := make(map[string]time.Duration, len(a.percentiles))
		for _, p := range a.percentiles {
			extra[PercentileLabel(p)] = clampDuration(nsToDuration(a.engine.GetPercentile(p)), a.minLatency, a.maxLatency)
		}
		result.ExtraPercentiles = extra
	}

	result.StatusCounts = copyInt64MapByInt(a.statusCounts)
	result.ErrorCounts = copyInt64MapByString(a.errorCounts)
	result.Buckets = a.engine.ExportBuckets()

	return result
}

// PercentileLabel formats a percentile value as a stable map key, e.g. 99 → "p99"
// and 99.9 → "p99.9".
func PercentileLabel(p float64) string {
	return "p" + strconv.FormatFloat(p, 'f', -1, 64)
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
	// time.Duration is int64 nanoseconds. Guard the overflow on the float64
	// before the int64 cast — once cast, a too-large value would have already
	// wrapped, so a post-cast comparison against math.MaxInt64 is dead.
	v = math.Round(v)
	if v >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(int64(v))
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
