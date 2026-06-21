package engine

import (
	"sync/atomic"
	"time"

	"algoryn.io/pulse/metrics"
)

// AdaptiveConfig enables real-time RPS auto-tuning for PhaseTypeConstant phases.
// On each reporting interval the engine measures observed error rate and P99
// latency and adjusts the arrival rate up or down accordingly.
// Requires engine.Options.ReportInterval > 0 to take effect.
type AdaptiveConfig struct {
	// MaxErrorRate is the error rate [0,1] above which RPS is reduced.
	// Zero disables error-rate-based tuning.
	MaxErrorRate float64
	// MaxP99 is the P99 latency threshold above which RPS is reduced.
	// Zero disables P99-based tuning.
	MaxP99 time.Duration
	// StepDown is the multiplier applied when a threshold is breached (e.g.
	// 0.9 reduces RPS by 10%). Must be in (0,1); defaults to 0.9.
	StepDown float64
	// StepUp is the multiplier applied when all thresholds are met (e.g.
	// 1.05 increases RPS by 5%). Must be > 1; defaults to 1.05.
	StepUp float64
	// MinRPS is the floor for the auto-tuned rate. Defaults to 1.
	MinRPS int
	// MaxRPS is the ceiling for the auto-tuned rate. 0 means uncapped.
	MaxRPS int
}

// IsZero reports whether the config contains no adaptive tuning triggers.
func (a AdaptiveConfig) IsZero() bool {
	return a.MaxErrorRate == 0 && a.MaxP99 == 0
}

// adaptiveController adjusts the arrival rate of a running phase based on
// live snapshot metrics. It is safe for concurrent use: onSnapshot is called
// from the interval goroutine while rate is read from the scheduler tick.
type adaptiveController struct {
	cfg      AdaptiveConfig
	milliRPS atomic.Int64 // current rate × 1000 for sub-integer precision
}

func newAdaptiveController(cfg AdaptiveConfig, baseRPS int) *adaptiveController {
	if cfg.StepDown <= 0 || cfg.StepDown >= 1 {
		cfg.StepDown = 0.9
	}
	if cfg.StepUp <= 1 {
		cfg.StepUp = 1.05
	}
	if cfg.MinRPS < 1 {
		cfg.MinRPS = 1
	}
	c := &adaptiveController{cfg: cfg}
	c.milliRPS.Store(int64(baseRPS) * 1000)
	return c
}

func (c *adaptiveController) onSnapshot(snap metrics.Snapshot) {
	current := float64(c.milliRPS.Load()) / 1000.0

	breached := false
	if c.cfg.MaxErrorRate > 0 && snap.Total > 0 {
		if float64(snap.Failed)/float64(snap.Total) > c.cfg.MaxErrorRate {
			breached = true
		}
	}
	if c.cfg.MaxP99 > 0 && snap.Latency.P99 > c.cfg.MaxP99 {
		breached = true
	}

	var next float64
	if breached {
		next = current * c.cfg.StepDown
	} else {
		next = current * c.cfg.StepUp
	}

	if next < float64(c.cfg.MinRPS) {
		next = float64(c.cfg.MinRPS)
	}
	if c.cfg.MaxRPS > 0 && next > float64(c.cfg.MaxRPS) {
		next = float64(c.cfg.MaxRPS)
	}

	c.milliRPS.Store(int64(next * 1000))
}

func (c *adaptiveController) rate() float64 {
	return float64(c.milliRPS.Load()) / 1000.0
}
