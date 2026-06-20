// Package merger combines multiple WorkerResult values from distributed workers
// into a single metrics.Result. Counters are summed, maps are merged, and histogram
// buckets are accumulated before computing percentiles so that cross-worker latency
// statistics are accurate (unlike averaging per-worker P-values, which is lossy).
package merger

import (
	"math"
	"strconv"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/internal/stats"
	"algoryn.io/pulse/metrics"
)

// Merge combines n WorkerResult values into a single metrics.Result.
// An empty slice returns a zero Result.
func Merge(results []distributed.WorkerResult) metrics.Result {
	if len(results) == 0 {
		return metrics.Result{}
	}

	var (
		total         int64
		failed        int64
		scheduled     int64
		started       int64
		dropped       int64
		completed     int64
		maxActive     int64
		minLatency    time.Duration
		maxLatency    time.Duration
		duration      time.Duration
		weightedMean  float64 // numerator: sum(mean_ns * total)
		statusCounts  = make(map[int]int64)
		errorCounts   = make(map[string]int64)
		mergedBuckets = make([]uint64, stats.NumBuckets)
	)

	for _, r := range results {
		total += r.Total
		failed += r.Failed
		scheduled += r.Scheduled
		started += r.Started
		dropped += r.Dropped
		completed += r.Completed

		if r.MaxActive > maxActive {
			maxActive = r.MaxActive
		}
		// Duration is the wall-clock time of the longest worker (phases run in
		// parallel across workers so total run time ≈ max worker time).
		if r.Duration > duration {
			duration = r.Duration
		}

		// Merge status-code map (JSON keys are decimal status strings).
		for k, v := range r.StatusCounts {
			code, err := strconv.Atoi(k)
			if err != nil {
				continue
			}
			statusCounts[code] += v
		}
		for k, v := range r.ErrorCounts {
			errorCounts[k] += v
		}

		// Sum histogram buckets for accurate merged-percentile computation.
		if len(r.Buckets) == stats.NumBuckets {
			for i, v := range r.Buckets {
				mergedBuckets[i] += v
			}
		}

		// Track global latency extremes across workers.
		if r.Total > 0 {
			if minLatency == 0 || r.Latency.Min < minLatency {
				minLatency = r.Latency.Min
			}
			if r.Latency.Max > maxLatency {
				maxLatency = r.Latency.Max
			}
			// Accumulate weighted mean (weight = per-worker request count).
			weightedMean += float64(r.Latency.Mean.Nanoseconds()) * float64(r.Total)
		}
	}

	// Compute weighted mean across all workers.
	var meanNanos float64
	if total > 0 {
		meanNanos = weightedMean / float64(total)
	}

	// Load merged buckets into a fresh engine to compute accurate percentiles.
	eng := stats.NewEngine()
	defer eng.Close()
	eng.ImportBuckets(mergedBuckets)

	var droppedRate float64
	if scheduled > 0 {
		droppedRate = float64(dropped) / float64(scheduled)
	}
	var rps float64
	if duration > 0 {
		rps = float64(total) / duration.Seconds()
	}

	clamp := func(d time.Duration) time.Duration {
		if d < minLatency {
			return minLatency
		}
		if d > maxLatency {
			return maxLatency
		}
		return d
	}
	nsDuration := func(v float64) time.Duration {
		if v <= 0 {
			return 0
		}
		r := int64(math.Round(v))
		if r < 0 {
			return 0
		}
		return time.Duration(r)
	}

	var latency metrics.LatencyStats
	if total > 0 {
		latency = metrics.LatencyStats{
			Min:  minLatency,
			Max:  maxLatency,
			Mean: time.Duration(math.Round(meanNanos)),
			P50:  clamp(nsDuration(eng.GetPercentile(50))),
			P90:  clamp(nsDuration(eng.GetPercentile(90))),
			P95:  clamp(nsDuration(eng.GetPercentile(95))),
			P99:  clamp(nsDuration(eng.GetPercentile(99))),
		}
	}

	return metrics.Result{
		Total:        total,
		Failed:       failed,
		Duration:     duration,
		RPS:          rps,
		Scheduled:    scheduled,
		Started:      started,
		Dropped:      dropped,
		DroppedRate:  droppedRate,
		Completed:    completed,
		MaxActive:    maxActive,
		Latency:      latency,
		StatusCounts: statusCounts,
		ErrorCounts:  errorCounts,
		Buckets:      mergedBuckets,
	}
}
