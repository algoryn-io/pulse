package engine

import (
	"math"
	"sync"
	"time"

	"algoryn.io/pulse/metrics"
)

// StressConfig enables ramp-to-failure (capacity discovery): the engine starts
// at the phase's arrival rate and raises it by StepRPS every healthy reporting
// interval until the target's error rate or P99 latency breaches a failure
// threshold, then stops and reports the sustained capacity. Requires
// engine.Options.ReportInterval > 0 and is mutually exclusive with AdaptiveConfig.
type StressConfig struct {
	// StepRPS is the additive arrival-rate increase applied after each healthy
	// interval. Defaults to 10 when not set.
	StepRPS int
	// MaxRPS is a safety ceiling for the ramp. 0 means uncapped (only a failure
	// or the phase duration stops the run).
	MaxRPS int
	// MaxErrorRate is the per-interval error rate [0,1] that counts as failure.
	// Zero disables error-rate-based failure detection.
	MaxErrorRate float64
	// MaxP99 is the per-interval P99 latency that counts as failure. Zero disables
	// latency-based failure detection.
	MaxP99 time.Duration
	// SustainedIntervals is the number of consecutive breached intervals required
	// to confirm failure, guarding against a single noisy spike. Defaults to 1.
	SustainedIntervals int
	// MinRequests is the minimum completed requests an interval must contain
	// before it is evaluated (or counted as a healthy step). Guards against
	// ramping or failing on a tiny, noisy window. Defaults to 0.
	MinRequests int64
}

// IsZero reports whether stress mode has no failure trigger configured.
func (s StressConfig) IsZero() bool {
	return s.MaxErrorRate == 0 && s.MaxP99 == 0
}

// stressController drives a one-directional arrival-rate ramp and detects the
// point at which the target fails. It is safe for concurrent use: onSnapshot is
// called from the interval goroutine while rate is read from the scheduler tick.
type stressController struct {
	cfg StressConfig

	mu           sync.Mutex
	current      float64 // current target RPS
	lastHealthy  int     // highest target RPS that completed an interval healthily
	breachStreak int
	done         bool
	outcome      metrics.StressResult
}

func newStressController(cfg StressConfig, baseRPS int) *stressController {
	if cfg.StepRPS < 1 {
		cfg.StepRPS = 10
	}
	if cfg.SustainedIntervals < 1 {
		cfg.SustainedIntervals = 1
	}
	start := float64(baseRPS)
	if start < 1 {
		start = 1
	}
	if cfg.MaxRPS > 0 && start > float64(cfg.MaxRPS) {
		start = float64(cfg.MaxRPS)
	}
	return &stressController{cfg: cfg, current: start}
}

// onSnapshot evaluates one completed interval: it ramps the rate up on a healthy
// interval, or accumulates breaches and marks the controller done once failure
// is sustained.
func (c *stressController) onSnapshot(snap metrics.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	// Ignore windows too small to judge, so the ramp does not advance or fail on
	// a partial/noisy interval.
	if snap.Total < c.cfg.MinRequests {
		return
	}

	if reason := c.breachReason(snap); reason != "" {
		c.breachStreak++
		if c.breachStreak >= c.cfg.SustainedIntervals {
			c.done = true
			c.outcome = metrics.StressResult{
				MaxHealthyRPS: c.lastHealthy,
				FailedAtRPS:   int(math.Round(c.current)),
				Reason:        reason,
				Failed:        true,
			}
		}
		return
	}

	// Healthy interval: record capacity and step up (capped at MaxRPS).
	c.breachStreak = 0
	c.lastHealthy = int(math.Round(c.current))
	next := c.current + float64(c.cfg.StepRPS)
	if c.cfg.MaxRPS > 0 && next > float64(c.cfg.MaxRPS) {
		next = float64(c.cfg.MaxRPS)
	}
	c.current = next
}

func (c *stressController) breachReason(snap metrics.Snapshot) string {
	if c.cfg.MaxErrorRate > 0 && snap.Total > 0 {
		if float64(snap.Failed)/float64(snap.Total) > c.cfg.MaxErrorRate {
			return "error_rate"
		}
	}
	if c.cfg.MaxP99 > 0 && snap.Latency.P99 > c.cfg.MaxP99 {
		return "p99_latency"
	}
	return ""
}

// rate is the scheduler RateFunc: the current target arrival rate.
func (c *stressController) rate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// isDone reports whether sustained failure has been detected.
func (c *stressController) isDone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

// result returns the stress outcome. When the run ended without a detected
// failure (hit MaxRPS or the phase duration), Failed is false and MaxHealthyRPS
// is the highest rate sustained.
func (c *stressController) result() metrics.StressResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.outcome
	}
	return metrics.StressResult{MaxHealthyRPS: c.lastHealthy, Failed: false}
}
