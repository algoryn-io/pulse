package pulse

import (
	"context"
	"errors"
	"testing"
	"time"

	fabricv1 "algoryn.io/fabric/gen/go/fabric/v1"
)

func TestValidateConfigRejectsAbortWithoutInterval(t *testing.T) {
	cfg := Config{
		Phases: []Phase{{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1}},
		Abort:  AbortConfig{MaxErrorRate: 0.5},
		// Reporting.Interval intentionally left zero.
	}
	if err := ValidateConfig(cfg); !errors.Is(err, errAbortRequiresInterval) {
		t.Fatalf("expected errAbortRequiresInterval, got %v", err)
	}
}

func TestValidateConfigRejectsAbortInvalidErrorRate(t *testing.T) {
	cfg := Config{
		Phases:    []Phase{{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1}},
		Reporting: ReportingConfig{Interval: 100 * time.Millisecond},
		Abort:     AbortConfig{MaxErrorRate: 1.5},
	}
	if err := ValidateConfig(cfg); !errors.Is(err, errAbortInvalidErrorRate) {
		t.Fatalf("expected errAbortInvalidErrorRate, got %v", err)
	}
}

func TestRunContextAbortsAndReturnsErrAborted(t *testing.T) {
	test := Test{
		Config: Config{
			Phases:         []Phase{{Type: PhaseTypeConstant, Duration: 5 * time.Second, ArrivalRate: 200}},
			MaxConcurrency: 100,
			Reporting:      ReportingConfig{Interval: 20 * time.Millisecond},
			Abort:          AbortConfig{MaxErrorRate: 0.5},
		},
		Scenario: func(context.Context) (int, error) { return 500, errors.New("boom") },
	}

	start := time.Now()
	_, err := RunContext(context.Background(), test)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected early abort, ran for %v", elapsed)
	}
}

func TestValidateConfigRejectsOutOfRangePercentile(t *testing.T) {
	for _, p := range []float64{0, 100, -1, 100.1} {
		cfg := Config{
			Phases:      []Phase{{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1}},
			Percentiles: []float64{p},
		}
		if err := ValidateConfig(cfg); !errors.Is(err, errInvalidPercentile) {
			t.Fatalf("percentile %v: expected errInvalidPercentile, got %v", p, err)
		}
	}
}

func TestRunContextReportsExtraPercentiles(t *testing.T) {
	test := Test{
		Config: Config{
			Phases:         []Phase{{Type: PhaseTypeConstant, Duration: 60 * time.Millisecond, ArrivalRate: 200}},
			MaxConcurrency: 50,
			Percentiles:    []float64{99.9},
		},
		Scenario: func(context.Context) (int, error) { return 200, nil },
	}
	result, err := RunContext(context.Background(), test)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := result.ExtraPercentiles["p99.9"]; !ok {
		t.Fatalf("expected p99.9 in ExtraPercentiles, got %v", result.ExtraPercentiles)
	}
}

func TestRunReturnsErrorWhenNoPhases(t *testing.T) {
	test := Test{
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNoPhases {
		t.Fatalf("expected %v, got %v", errNoPhases, err)
	}
}

func TestRunReturnsErrorWhenScenarioIsNil(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
		},
	}

	_, err := Run(test)
	if err != errNilScenario {
		t.Fatalf("expected %v, got %v", errNilScenario, err)
	}
}

func TestRunReturnsErrorWhenPhaseDurationIsNotPositive(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 0, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNonPositivePhase {
		t.Fatalf("expected %v, got %v", errNonPositivePhase, err)
	}
}

func TestRunReturnsErrorWhenPhaseTypeIsEmpty(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: "", Duration: time.Second, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errEmptyPhaseType {
		t.Fatalf("expected %v, got %v", errEmptyPhaseType, err)
	}
}

func TestRunReturnsErrorWhenPhaseTypeIsUnsupported(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseType("custom"), Duration: time.Second, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errUnsupportedPhaseType {
		t.Fatalf("expected %v, got %v", errUnsupportedPhaseType, err)
	}
}

func TestRunReturnsErrorWhenPhaseArrivalRateIsNotPositive(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 0},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNonPositiveArrivalRate {
		t.Fatalf("expected %v, got %v", errNonPositiveArrivalRate, err)
	}
}

func TestRunContextPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	passed := make(chan bool, 1)

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 100},
			},
			MaxConcurrency: 1,
			OnResult: func(_ Result, resultPassed bool) {
				passed <- resultPassed
			},
		},
		Scenario: func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := RunContext(ctx, test)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scenario to start")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected %v, got %v", context.Canceled, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RunContext cancellation")
	}

	if resultPassed := <-passed; resultPassed {
		t.Fatal("expected canceled run to invoke hook with passed=false")
	}
}

func TestRunDefaultsToDropSaturationPolicy(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 100},
			},
			MaxConcurrency: 1,
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(100 * time.Millisecond)
			return 0, nil
		},
	}

	result, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Dropped == 0 {
		t.Fatalf("expected dropped arrivals under saturation, got %+v", result)
	}
	if result.Scheduled != result.Started+result.Dropped {
		t.Fatalf("scheduled arrivals do not reconcile: %+v", result)
	}
	if result.Completed != result.Total {
		t.Fatalf("completed %d vs total %d", result.Completed, result.Total)
	}
}

func TestRunRejectsUnsupportedSaturationPolicy(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			SaturationPolicy: SaturationPolicy("queue"),
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errUnsupportedSaturation {
		t.Fatalf("expected %v, got %v", errUnsupportedSaturation, err)
	}
}

func TestRunRejectsNegativeMaxConcurrency(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			MaxConcurrency: -1,
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeMaxConcurrency {
		t.Fatalf("expected %v, got %v", errNegativeMaxConcurrency, err)
	}
}

func TestRunReturnsIntervalSnapshots(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 140 * time.Millisecond, ArrivalRate: 50},
			},
			MaxConcurrency: 2,
			Reporting: ReportingConfig{
				Interval: 50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			return 200, nil
		},
	}

	result, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result.Snapshots) < 2 {
		t.Fatalf("expected multiple interval snapshots, got %+v", result.Snapshots)
	}

	var scheduled, completed int64
	for _, snapshot := range result.Snapshots {
		scheduled += snapshot.Scheduled
		completed += snapshot.Completed
	}
	if scheduled != result.Scheduled {
		t.Fatalf("snapshot scheduled total %d vs result %d", scheduled, result.Scheduled)
	}
	if completed != result.Completed {
		t.Fatalf("snapshot completed total %d vs result %d", completed, result.Completed)
	}
}

func TestRunRejectsNegativeReportingInterval(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			Reporting: ReportingConfig{Interval: -time.Second},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeReportInterval {
		t.Fatalf("expected %v, got %v", errNegativeReportInterval, err)
	}
}

func TestRunRejectsReportingIntervalBelowLimit(t *testing.T) {
	test := Test{
		Config: Config{
			Phases:    []Phase{{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1}},
			Reporting: ReportingConfig{Interval: time.Nanosecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errReportIntervalTooSmall {
		t.Fatalf("expected %v, got %v", errReportIntervalTooSmall, err)
	}
}

func TestRunRejectsTooManySnapshots(t *testing.T) {
	test := Test{
		Config: Config{
			Phases:    []Phase{{Type: PhaseTypeConstant, Duration: 2 * time.Minute, ArrivalRate: 1}},
			Reporting: ReportingConfig{Interval: 10 * time.Millisecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errTooManySnapshots {
		t.Fatalf("expected %v, got %v", errTooManySnapshots, err)
	}
}

func TestRunRejectsSpikeOutsidePhase(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{
					Type:          PhaseTypeSpike,
					Duration:      time.Second,
					From:          10,
					To:            20,
					SpikeAt:       800 * time.Millisecond,
					SpikeDuration: 300 * time.Millisecond,
				},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errInvalidSpikeConfig {
		t.Fatalf("expected %v, got %v", errInvalidSpikeConfig, err)
	}
}

func TestRunReturnsErrorWhenRampEndpointsAreInvalid(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeRamp, Duration: time.Second, From: 0, To: 5},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errInvalidRampEndpoints {
		t.Fatalf("expected %v, got %v", errInvalidRampEndpoints, err)
	}
}

func TestRunExecutesRampPhase(t *testing.T) {
	calls := 0
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeRamp, Duration: 250 * time.Millisecond, From: 10, To: 25},
			},
			MaxConcurrency: 2,
		},
		Scenario: func(context.Context) (int, error) {
			calls++
			return 0, nil
		},
	}

	_, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected ramp to invoke scenario multiple times, got %d", calls)
	}
}

func TestRunExecutesScenario(t *testing.T) {
	calls := 0
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
			MaxConcurrency: 4,
		},
		Scenario: func(context.Context) (int, error) {
			calls++
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls == 0 {
		t.Fatal("expected scenario to execute at least once")
	}

	if got.Total != int64(calls) {
		t.Fatalf("expected total %d, got %d", calls, got.Total)
	}

	if got.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", got.Failed)
	}

	if got.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", got.Duration)
	}

	if got.Latency.Min <= 0 {
		t.Fatalf("expected positive min latency, got %v", got.Latency.Min)
	}

	if got.Latency.Max <= 0 {
		t.Fatalf("expected positive max latency, got %v", got.Latency.Max)
	}

	if got.Latency.Mean <= 0 {
		t.Fatalf("expected positive mean latency, got %v", got.Latency.Mean)
	}

	l := got.Latency
	if l.P50 <= 0 || l.P90 <= 0 || l.P95 <= 0 || l.P99 <= 0 {
		t.Fatalf("expected positive latency percentiles, got %+v", l)
	}
	if l.Min > l.P50 || l.P50 > l.Max {
		t.Fatalf("P50 outside [min,max]: %+v", l)
	}
	if l.P50 > l.P90 || l.P90 > l.P95 || l.P95 > l.P99 {
		t.Fatalf("expected P50 <= P90 <= P95 <= P99, got %v %v %v %v", l.P50, l.P90, l.P95, l.P99)
	}
	if l.P99 > l.Max {
		t.Fatalf("P99 above max: %+v", l)
	}
}

func TestRunRecordsScenarioErrorsWithoutAborting(t *testing.T) {
	wantErr := errors.New("scenario failed")
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
			},
			MaxConcurrency: 4,
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, wantErr
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected nil error from Run when only scenario fails, got %v", err)
	}

	if got.Total < 2 {
		t.Fatalf("expected run to continue, total %d", got.Total)
	}

	if got.Failed != got.Total {
		t.Fatalf("expected all executions failed, total %d failed %d", got.Total, got.Failed)
	}

	if got.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", got.Duration)
	}

	if got.Latency.Min <= 0 || got.Latency.Max <= 0 || got.Latency.Mean <= 0 {
		t.Fatalf("expected latency fields to be populated, got %+v", got.Latency)
	}
}

func TestRunPassesThresholds(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.5,
				MaxMeanLatency: 50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got.Total == 0 {
		t.Fatal("expected executions to run")
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: true, Description: "error_rate < 0.5"},
		{Pass: true, Description: "mean_latency < 50ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestRunFailsWhenThresholdsAreViolated(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.1,
				MaxMeanLatency: time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, errors.New("scenario failed")
		},
	}

	got, err := Run(test)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var tv *ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError in chain, got %v", err)
	}

	if got.Total < 1 {
		t.Fatalf("expected at least one execution, got %d", got.Total)
	}

	if got.Failed != got.Total {
		t.Fatalf("expected all executions failed, total %d failed %d", got.Total, got.Failed)
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: false, Description: "error_rate < 0.1"},
		{Pass: false, Description: "mean_latency < 1ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestThresholdViolationErrorFormatsErrorRateNicely(t *testing.T) {
	err := (&ThresholdViolationError{
		Description: "error_rate < 0.1",
		Actual:      1.0,
		Limit:       0.1,
	}).Error()

	want := "pulse: threshold violated (error_rate < 0.1): got 1.000 (100.0%), limit 0.100 (10.0%)"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}

func TestRunFailsWhenDroppedRateThresholdIsViolated(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 100},
			},
			MaxConcurrency: 1,
			Thresholds: Thresholds{
				MaxDroppedRate: 0.1,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(100 * time.Millisecond)
			return 0, nil
		},
	}

	result, err := Run(test)
	if err == nil {
		t.Fatal("expected threshold error, got nil")
	}
	if result.DroppedRate <= 0.1 {
		t.Fatalf("expected dropped rate above threshold, got %+v", result)
	}
	var tv *ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError, got %v", err)
	}
	if tv.Description != "dropped_rate < 0.1" {
		t.Fatalf("unexpected description %q", tv.Description)
	}
}

func TestRunRejectsInvalidDroppedRateThreshold(t *testing.T) {
	base := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	base.Config.Thresholds.MaxDroppedRate = -0.1
	if _, err := Run(base); err != errNegativeDroppedRate {
		t.Fatalf("expected %v, got %v", errNegativeDroppedRate, err)
	}

	base.Config.Thresholds.MaxDroppedRate = 1.1
	if _, err := Run(base); err != errDroppedRateAboveOne {
		t.Fatalf("expected %v, got %v", errDroppedRateAboveOne, err)
	}
}

func TestThresholdViolationErrorFormatsLatencyNicely(t *testing.T) {
	err := (&ThresholdViolationError{
		Description: "mean_latency < 200ms",
		Actual:      250 * time.Millisecond,
		Limit:       200 * time.Millisecond,
	}).Error()

	want := "pulse: threshold violated (mean_latency < 200ms): got 250ms, limit 200ms"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}

func TestRunReturnsErrorWhenThresholdMaxP95LatencyIsNegative(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			Thresholds: Thresholds{MaxP95Latency: -time.Millisecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeP95Latency {
		t.Fatalf("expected %v, got %v", errNegativeP95Latency, err)
	}
}

func TestRunReturnsErrorWhenThresholdMaxP99LatencyIsNegative(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: time.Second, ArrivalRate: 1},
			},
			Thresholds: Thresholds{MaxP99Latency: -time.Millisecond},
		},
		Scenario: func(context.Context) (int, error) { return 0, nil },
	}

	_, err := Run(test)
	if err != errNegativeP99Latency {
		t.Fatalf("expected %v, got %v", errNegativeP99Latency, err)
	}
}

func TestRunPassesP95AndP99Thresholds(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 120 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				MaxP95Latency: 50 * time.Millisecond,
				MaxP99Latency: 50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(got.ThresholdOutcomes) != 2 {
		t.Fatalf("expected 2 threshold outcomes, got %+v", got.ThresholdOutcomes)
	}
	want := []ThresholdOutcome{
		{Pass: true, Description: "p95_latency < 50ms"},
		{Pass: true, Description: "p99_latency < 50ms"},
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestRunFailsWhenP95ThresholdViolated(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 200 * time.Millisecond, ArrivalRate: 15},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				MaxP95Latency: 2 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(10 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var tv *ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError, got %v", err)
	}
	if tv.Description != "p95_latency < 2ms" {
		t.Fatalf("description: got %q, want p95_latency < 2ms", tv.Description)
	}

	if len(got.ThresholdOutcomes) != 1 {
		t.Fatalf("expected 1 threshold outcome, got %+v", got.ThresholdOutcomes)
	}
	want := ThresholdOutcome{Pass: false, Description: "p95_latency < 2ms"}
	if got.ThresholdOutcomes[0] != want {
		t.Fatalf("want %+v, got %+v", want, got.ThresholdOutcomes[0])
	}
}

func TestRunThresholdOutcomesStableOrderWhenAllSet(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 120 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate:      0.5,
				MaxMeanLatency: 50 * time.Millisecond,
				MaxP95Latency:  50 * time.Millisecond,
				MaxP99Latency:  50 * time.Millisecond,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []ThresholdOutcome{
		{Pass: true, Description: "error_rate < 0.5"},
		{Pass: true, Description: "mean_latency < 50ms"},
		{Pass: true, Description: "p95_latency < 50ms"},
		{Pass: true, Description: "p99_latency < 50ms"},
	}
	if len(got.ThresholdOutcomes) != len(want) {
		t.Fatalf("expected %d outcomes, got %+v", len(want), got.ThresholdOutcomes)
	}
	for i := range want {
		if got.ThresholdOutcomes[i] != want[i] {
			t.Fatalf("outcome %d: want %+v, got %+v", i, want[i], got.ThresholdOutcomes[i])
		}
	}
}

func TestOnResultInvokedWithResultAndPassedTrue(t *testing.T) {
	var hookCalled bool
	var gotResult Result
	var gotPassed bool

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			OnResult: func(r Result, passed bool) {
				hookCalled = true
				gotResult = r
				gotPassed = passed
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 200, nil
		},
	}

	want, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !hookCalled {
		t.Fatal("expected OnResult to be called")
	}
	if !gotPassed {
		t.Fatalf("expected passed true with no thresholds, got %v", gotPassed)
	}
	if gotResult.Total != want.Total || gotResult.Failed != want.Failed {
		t.Fatalf("hook result mismatch: want Total=%d Failed=%d, got Total=%d Failed=%d",
			want.Total, want.Failed, gotResult.Total, gotResult.Failed)
	}
}

func TestOnResultPassedFalseWhenThresholdFails(t *testing.T) {
	// ErrorRate must be > 0 for evaluateThresholds to apply error_rate (0 disables it).
	var gotPassed bool
	var hookCalled bool

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate: 1e-9, // any failed request violates
			},
			OnResult: func(_ Result, passed bool) {
				hookCalled = true
				gotPassed = passed
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, errors.New("scenario failed")
		},
	}

	_, err := Run(test)
	if err == nil {
		t.Fatal("expected threshold error")
	}
	if !hookCalled {
		t.Fatal("expected OnResult to be called")
	}
	if gotPassed {
		t.Fatal("expected passed false when threshold fails")
	}
}

func TestOnResultNilDoesNotPanic(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		},
	}

	got, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Total == 0 {
		t.Fatal("expected at least one execution")
	}
}

func TestOnFabricEmitInvokedWithProtobufContracts(t *testing.T) {
	var hookCalled bool
	var gotRun *fabricv1.RunEvent
	var gotEvt *fabricv1.Event

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Service:        "api-test",
			OnFabricEmit: func(run *fabricv1.RunEvent, completed *fabricv1.Event) {
				hookCalled = true
				gotRun = run
				gotEvt = completed
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 200, nil
		},
	}

	_, err := Run(test)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !hookCalled {
		t.Fatal("expected OnFabricEmit to be called")
	}
	if gotRun == nil || gotRun.GetSnapshot() == nil {
		t.Fatal("expected non-nil RunEvent with snapshot")
	}
	if gotRun.GetSnapshot().GetService() != "api-test" {
		t.Fatalf("snapshot service = %q", gotRun.GetSnapshot().GetService())
	}
	if gotEvt.GetType() != fabricv1.EventType_EVENT_TYPE_RUN_COMPLETED {
		t.Fatalf("event type = %v", gotEvt.GetType())
	}
	if gotEvt.GetRunCompleted().GetService() != "api-test" {
		t.Fatalf("payload service = %q", gotEvt.GetRunCompleted().GetService())
	}
}
