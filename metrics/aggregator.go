package metrics

import "time"

// Result contains the aggregated execution metrics for a run.
type Result struct {
	Total    int64
	Failed   int64
	Duration time.Duration
	Latency  LatencyStats
}

// Aggregator collects execution metrics for the MVP.
type Aggregator struct {
	total      int64
	failed     int64
	totalNanos int64
	minLatency time.Duration
	maxLatency time.Duration
}

// NewAggregator creates an empty metrics aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// Record stores metrics for a single execution.
func (a *Aggregator) Record(latency time.Duration, failed bool) {
	a.total++
	if failed {
		a.failed++
	}

	a.totalNanos += latency.Nanoseconds()
	if a.total == 1 || latency < a.minLatency {
		a.minLatency = latency
	}
	if latency > a.maxLatency {
		a.maxLatency = latency
	}
}

// Result returns the aggregated metrics snapshot.
func (a *Aggregator) Result(duration time.Duration) Result {
	result := Result{
		Total:    a.total,
		Failed:   a.failed,
		Duration: duration,
	}

	if a.total == 0 {
		return result
	}

	result.Latency = LatencyStats{
		Min:  a.minLatency,
		Max:  a.maxLatency,
		Mean: time.Duration(a.totalNanos / a.total),
	}

	return result
}
