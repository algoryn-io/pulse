package metrics

import "time"

// LatencyStats contains aggregate latency data for a run.
type LatencyStats struct {
	Min  time.Duration
	Max  time.Duration
	Mean time.Duration
}
