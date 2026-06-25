// Package distributed contains the wire types and HTTP transport used for
// distributed Pulse runs. Workers expose POST /run and GET /ping over HTTP/JSON.
// The coordinator fans out RunRequests to all workers and merges WorkerResults.
package distributed

import "time"

// Phase is the wire representation of a load-test phase sent to a worker.
// Durations are encoded as int64 nanoseconds by the standard time.Duration JSON marshaling.
type Phase struct {
	Type          string        `json:"type"`
	Duration      time.Duration `json:"duration"`
	ArrivalRate   int           `json:"arrivalRate"`
	From          int           `json:"from"`
	To            int           `json:"to"`
	Steps         int           `json:"steps"`
	SpikeAt       time.Duration `json:"spikeAt"`
	SpikeDuration time.Duration `json:"spikeDuration"`
}

// HTTPScenario describes the HTTP request a worker should generate when it has
// no pre-registered scenario (CLI worker mode). Workers that were started with
// a pre-registered scenario via ListenAsWorker ignore this field.
type HTTPScenario struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// RunRequest is the JSON body the coordinator POSTs to /run on each worker.
// Workers execute the phases at the specified arrival rate and return a WorkerResult.
type RunRequest struct {
	Phases           []Phase      `json:"phases"`
	MaxConcurrency   int          `json:"maxConcurrency"`
	SaturationPolicy string       `json:"saturationPolicy"`
	// HTTPScenario is set by the coordinator when workers should build a built-in
	// HTTP scenario from scratch (CLI distributed mode). When nil, workers use
	// their pre-registered scenario (library distributed mode).
	HTTPScenario *HTTPScenario `json:"httpScenario,omitempty"`
}

// LatencyStats is a wire-safe latency summary using nanosecond int64 durations.
type LatencyStats struct {
	Min  time.Duration `json:"min"`
	Mean time.Duration `json:"mean"`
	P50  time.Duration `json:"p50"`
	P90  time.Duration `json:"p90"`
	P95  time.Duration `json:"p95"`
	P99  time.Duration `json:"p99"`
	Max  time.Duration `json:"max"`
}

// WorkerResult is the JSON body returned by a worker when a run completes.
// StatusCounts uses string keys because JSON only allows string map keys.
type WorkerResult struct {
	Total        int64            `json:"total"`
	Failed       int64            `json:"failed"`
	Duration     time.Duration    `json:"duration"`
	Scheduled    int64            `json:"scheduled"`
	Started      int64            `json:"started"`
	Dropped      int64            `json:"dropped"`
	DroppedRate  float64          `json:"droppedRate"`
	Completed    int64            `json:"completed"`
	MaxActive    int64            `json:"maxActive"`
	Latency      LatencyStats     `json:"latency"`
	// TTFB holds the worker's time-to-first-byte summary.
	TTFB LatencyStats `json:"ttfb"`
	// BytesIn and BytesOut are the worker's total response and request bytes.
	BytesIn  int64 `json:"bytesIn"`
	BytesOut int64 `json:"bytesOut"`
	// StatusCounts keys are decimal HTTP status codes (e.g. "200", "404").
	StatusCounts map[string]int64 `json:"statusCounts"`
	ErrorCounts  map[string]int64 `json:"errorCounts"`
	// Buckets contains the 800 histogram bucket counts exported from the worker's
	// stats.Engine. The coordinator sums these across all workers and imports them
	// into a fresh engine to compute accurate merged latency percentiles.
	Buckets []uint64 `json:"buckets"`
	// TTFBBuckets mirrors Buckets for the time-to-first-byte histogram so the
	// coordinator can compute accurate merged TTFB percentiles.
	TTFBBuckets []uint64 `json:"ttfbBuckets"`
}

// PingResponse is the JSON body returned by GET /ping.
type PingResponse struct {
	Status string `json:"status"`
}
