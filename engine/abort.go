package engine

import (
	"errors"
	"time"

	"algoryn.io/pulse/metrics"
)

// ErrAborted is returned by Run when an AbortConfig threshold is breached and
// the run is stopped early. Detect it with errors.Is.
var ErrAborted = errors.New("pulse: run aborted by threshold")

// AbortConfig stops a run early (fail-fast) when live interval metrics breach a
// limit. Unlike AdaptiveConfig, which lowers the arrival rate, AbortConfig
// cancels the run and Run returns ErrAborted. Requires
// engine.Options.ReportInterval > 0 to take effect.
type AbortConfig struct {
	// MaxErrorRate is the per-interval error rate [0,1] above which the run is
	// aborted. Zero disables error-rate-based aborting.
	MaxErrorRate float64
	// MaxP99 is the per-interval P99 latency above which the run is aborted.
	// Zero disables P99-based aborting.
	MaxP99 time.Duration
	// MinRequests is the minimum number of completed requests an interval must
	// contain before its metrics are eligible to trigger an abort. It guards
	// against aborting on a tiny, noisy first window. Defaults to 0 (no minimum).
	MinRequests int64
}

// IsZero reports whether the config contains no abort triggers.
func (a AbortConfig) IsZero() bool {
	return a.MaxErrorRate == 0 && a.MaxP99 == 0
}

// breached reports whether snap violates any configured abort threshold.
func (a AbortConfig) breached(snap metrics.Snapshot) bool {
	if snap.Total < a.MinRequests {
		return false
	}
	if a.MaxErrorRate > 0 && snap.Total > 0 {
		if float64(snap.Failed)/float64(snap.Total) > a.MaxErrorRate {
			return true
		}
	}
	if a.MaxP99 > 0 && snap.Latency.P99 > a.MaxP99 {
		return true
	}
	return false
}
