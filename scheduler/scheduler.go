package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"algoryn.io/pulse/internal"
	"algoryn.io/pulse/model"
)

var (
	ErrUnsupportedPhaseType   = errors.New("scheduler: unsupported phase type")
	ErrNonPositiveArrivalRate = errors.New("scheduler: phase arrival rate must be positive")
	ErrInvalidRampEndpoints   = errors.New("scheduler: ramp from and to must be positive")
	ErrInvalidStepConfig      = errors.New("scheduler: step phase requires From, To and Steps > 0")
	ErrInvalidSpikeConfig     = errors.New("scheduler: spike phase requires positive From, To and SpikeDuration")
)

// Phase contains the scheduling inputs for a single phase.
type Phase struct {
	Type          model.PhaseType
	Duration      time.Duration
	ArrivalRate   int
	From          int
	To            int
	Steps         int           // number of discrete steps; only used by PhaseTypeStep
	SpikeAt       time.Duration // when spike starts (from phase start)
	SpikeDuration time.Duration // how long the spike lasts
}

// Run executes the supported scheduling strategy for a phase.
func Run(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	switch phase.Type {
	case model.PhaseTypeConstant:
		if phase.ArrivalRate <= 0 {
			return ErrNonPositiveArrivalRate
		}
		return runConstant(ctx, phase, scenario)
	case model.PhaseTypeRamp:
		if phase.From <= 0 || phase.To <= 0 {
			return ErrInvalidRampEndpoints
		}
		return runRamp(ctx, phase, scenario)
	case model.PhaseTypeStep:
		if phase.From <= 0 || phase.To <= 0 || phase.Steps <= 0 {
			return ErrInvalidStepConfig
		}
		return runStep(ctx, phase, scenario)
	case model.PhaseTypeSpike:
		if phase.From <= 0 || phase.To <= 0 || phase.SpikeDuration <= 0 {
			return ErrInvalidSpikeConfig
		}
		return runSpike(ctx, phase, scenario)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPhaseType, phase.Type)
	}
}

// newStoppedTimer creates a timer that is immediately stopped and drained,
// ready for use with Reset in a poll loop without any initial allocation waste.
func newStoppedTimer() *time.Timer {
	t := time.NewTimer(0)
	if !t.Stop() {
		<-t.C
	}
	return t
}

// pollWait sleeps for d or until ctx is done, using the provided timer to avoid
// allocating a new channel on every iteration. After pollWait returns nil the
// timer has fired and its channel is empty, so the next Reset call is safe.
func pollWait(ctx context.Context, t *time.Timer, d time.Duration) error {
	t.Reset(d)
	select {
	case <-ctx.Done():
		if !t.Stop() {
			<-t.C
		}
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runConstant(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	capacity := phase.ArrivalRate
	if capacity < 1 {
		capacity = 1
	}
	// Drained start avoids bursting the whole capacity before the engine limiter
	// can apply backpressure (wrappedScenario returns before work finishes).
	bucket := internal.NewDrainedTokenBucket(capacity, float64(phase.ArrivalRate))
	deadline := time.Now().Add(phase.Duration)
	const poll = time.Millisecond

	timer := newStoppedTimer()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}

		if bucket.Allow(now) {
			if err := scenario(ctx); err != nil {
				return err
			}
		} else if err := pollWait(ctx, timer, poll); err != nil {
			return err
		}
	}
}

func runRamp(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	start := time.Now()
	deadline := start.Add(phase.Duration)

	capacity := phase.From
	if phase.To > capacity {
		capacity = phase.To
	}
	if capacity < 1 {
		capacity = 1
	}

	initialRate := float64(phase.From)
	if initialRate < 1 {
		initialRate = 1
	}
	bucket := internal.NewDrainedTokenBucket(capacity, initialRate)
	const poll = time.Millisecond

	timer := newStoppedTimer()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}

		elapsed := now.Sub(start)
		frac := float64(elapsed) / float64(phase.Duration)
		if frac > 1 {
			frac = 1
		}
		rate := float64(phase.From) + float64(phase.To-phase.From)*frac
		if rate < 1 {
			rate = 1
		}

		bucket.SetRefillRate(rate, now)

		if bucket.Allow(now) {
			if err := scenario(ctx); err != nil {
				return err
			}
		} else if err := pollWait(ctx, timer, poll); err != nil {
			return err
		}
	}
}

func runStep(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	start := time.Now()
	deadline := start.Add(phase.Duration)

	capacity := phase.From
	if phase.To > capacity {
		capacity = phase.To
	}
	if capacity < 1 {
		capacity = 1
	}

	bucket := internal.NewDrainedTokenBucket(capacity, float64(phase.From))
	const poll = time.Millisecond
	currentStep := -1 // force SetRefillRate on first iteration

	timer := newStoppedTimer()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}

		elapsed := now.Sub(start)
		frac := float64(elapsed) / float64(phase.Duration)
		if frac > 1 {
			frac = 1
		}

		stepIndex := int(frac * float64(phase.Steps))
		if stepIndex >= phase.Steps {
			stepIndex = phase.Steps - 1
		}

		if stepIndex != currentStep {
			currentStep = stepIndex
			var rate float64
			if phase.Steps == 1 {
				rate = float64(phase.From)
			} else {
				rate = float64(phase.From) + float64(phase.To-phase.From)*
					float64(stepIndex)/float64(phase.Steps-1)
			}
			if rate < 1 {
				rate = 1
			}
			bucket.SetRefillRate(rate, now)
		}

		if bucket.Allow(now) {
			if err := scenario(ctx); err != nil {
				return err
			}
		} else if err := pollWait(ctx, timer, poll); err != nil {
			return err
		}
	}
}

func runSpike(ctx context.Context, phase Phase, scenario func(context.Context) error) error {
	start := time.Now()
	deadline := start.Add(phase.Duration)
	spikeStart := start.Add(phase.SpikeAt)
	spikeEnd := spikeStart.Add(phase.SpikeDuration)

	capacity := phase.From
	if phase.To > capacity {
		capacity = phase.To
	}
	if capacity < 1 {
		capacity = 1
	}

	bucket := internal.NewDrainedTokenBucket(capacity, float64(phase.From))
	const poll = time.Millisecond
	inSpike := false

	timer := newStoppedTimer()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}

		shouldBeInSpike := !now.Before(spikeStart) && now.Before(spikeEnd)
		if shouldBeInSpike != inSpike {
			inSpike = shouldBeInSpike
			if inSpike {
				bucket.SetRefillRate(float64(phase.To), now)
			} else {
				bucket.SetRefillRate(float64(phase.From), now)
			}
		}

		if bucket.Allow(now) {
			if err := scenario(ctx); err != nil {
				return err
			}
		} else if err := pollWait(ctx, timer, poll); err != nil {
			return err
		}
	}
}
