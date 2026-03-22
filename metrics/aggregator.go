package metrics

import (
	"math"
	"slices"
	"sync"
	"time"
)

// Result contains the aggregated execution metrics for a run.
type Result struct {
	Total        int64
	Failed       int64
	Duration     time.Duration
	RPS          float64
	Latency      LatencyStats
	StatusCounts map[int]int64
	ErrorCounts  map[string]int64
}

// Aggregator collects execution metrics for the MVP.
type Aggregator struct {
	mu           sync.Mutex
	total        int64
	failed       int64
	meanNanos    float64
	minLatency   time.Duration
	maxLatency   time.Duration
	latencies    []time.Duration // retained for future percentile computation
	statusCounts map[int]int64
	errorCounts  map[string]int64
}

// NewAggregator creates an empty metrics aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{
		latencies:    make([]time.Duration, 0),
		statusCounts: make(map[int]int64),
		errorCounts:  make(map[string]int64),
	}
}

// Record stores metrics for a single execution.
func (a *Aggregator) Record(latency time.Duration, statusCode int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

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

	a.latencies = append(a.latencies, latency)
}

// Result returns the aggregated metrics snapshot.
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

	if a.total == 0 {
		return result
	}

	sorted := make([]time.Duration, len(a.latencies))
	copy(sorted, a.latencies)
	slices.Sort(sorted)

	result.Latency = LatencyStats{
		Min:  a.minLatency,
		Max:  a.maxLatency,
		Mean: time.Duration(math.Round(a.meanNanos)),
		P50:  percentileFromSorted(sorted, 50),
		P95:  percentileFromSorted(sorted, 95),
		P99:  percentileFromSorted(sorted, 99),
	}

	result.StatusCounts = copyInt64MapByInt(a.statusCounts)
	result.ErrorCounts = copyInt64MapByString(a.errorCounts)

	return result
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
