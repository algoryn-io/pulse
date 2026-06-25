package pulse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	fabricv1 "algoryn.io/fabric/gen/go/fabric/v1"

	"algoryn.io/pulse/dashboard"
	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/distributed/coordinator"
	"algoryn.io/pulse/distributed/worker"
	"algoryn.io/pulse/engine"
	"algoryn.io/pulse/metrics"
	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
	"algoryn.io/pulse/transport"
)

var (
	errNoPhases               = errors.New("pulse: at least one phase is required")
	errNilScenario            = errors.New("pulse: scenario must not be nil")
	errNonPositivePhase       = errors.New("pulse: phase duration must be positive")
	errNonPositiveArrivalRate = errors.New("pulse: phase arrival rate must be positive")
	errInvalidRampEndpoints   = errors.New("pulse: ramp phase from and to must be positive")
	errInvalidStepConfig      = errors.New("pulse: step phase requires positive From, To and Steps")
	errInvalidSpikeConfig     = errors.New("pulse: spike phase requires positive From, To and SpikeDuration")
	errEmptyPhaseType         = errors.New("pulse: phase type is required")
	errUnsupportedPhaseType   = errors.New("pulse: unsupported phase type")
	errNegativeErrorRate      = errors.New("pulse: threshold error rate must not be negative")
	errErrorRateAboveOne      = errors.New("pulse: threshold error rate must not be greater than 1")
	errNegativeMeanLatency    = errors.New("pulse: threshold mean latency must not be negative")
	errNegativeP95Latency     = errors.New("pulse: threshold p95 latency must not be negative")
	errNegativeP99Latency     = errors.New("pulse: threshold p99 latency must not be negative")
	errUnsupportedSaturation  = errors.New("pulse: unsupported saturation policy")
	errNegativeDroppedRate    = errors.New("pulse: threshold dropped rate must not be negative")
	errDroppedRateAboveOne    = errors.New("pulse: threshold dropped rate must not be greater than 1")
	errNegativeMaxConcurrency    = errors.New("pulse: max concurrency must not be negative")
	errNegativeReportInterval    = errors.New("pulse: reporting interval must not be negative")
	errReportIntervalTooSmall    = errors.New("pulse: reporting interval must be at least 10ms when enabled")
	errTooManySnapshots          = errors.New("pulse: reporting interval would generate too many snapshots")
	errMaxConcurrencyTooHigh     = errors.New("pulse: max concurrency must not exceed 1000000")
	errAdaptiveRequiresInterval  = errors.New("pulse: Adaptive requires Reporting.Interval > 0")
	errAdaptiveInvalidErrorRate  = errors.New("pulse: Adaptive.MaxErrorRate must be in [0,1]")
	errAdaptiveInvalidP99        = errors.New("pulse: Adaptive.MaxP99 must not be negative")
	errAbortRequiresInterval     = errors.New("pulse: Abort requires Reporting.Interval > 0")
	errAbortInvalidErrorRate     = errors.New("pulse: Abort.MaxErrorRate must be in [0,1]")
	errAbortInvalidP99           = errors.New("pulse: Abort.MaxP99 must not be negative")
	errStressRequiresInterval       = errors.New("pulse: Stress requires Reporting.Interval > 0")
	errStressInvalidErrorRate       = errors.New("pulse: Stress.MaxErrorRate must be in [0,1]")
	errStressInvalidP99             = errors.New("pulse: Stress.MaxP99 must not be negative")
	errStressNegativeRate           = errors.New("pulse: Stress.StepRPS and Stress.MaxRPS must not be negative")
	errStressAdaptiveExclusive      = errors.New("pulse: Stress and Adaptive are mutually exclusive")
	errStressDistributedUnsupported = errors.New("pulse: Stress is not supported in distributed mode (Workers)")
	errInvalidPercentile         = errors.New("pulse: Percentiles values must be in (0,100)")
)

const (
	minReportingInterval = 10 * time.Millisecond
	maxSnapshots         = 10_000
	maxConcurrency       = 1_000_000
)

// AdaptiveConfig enables real-time RPS auto-tuning for PhaseTypeConstant phases.
// On each reporting interval the engine adjusts the arrival rate up or down
// based on observed error rate and P99 latency thresholds.
// Requires Reporting.Interval > 0.
type AdaptiveConfig = engine.AdaptiveConfig

// AbortConfig stops a run early (fail-fast) when a reporting interval breaches a
// configured error-rate or P99-latency limit. When triggered, RunContext
// returns a result for the partial run wrapped with ErrAborted.
// Requires Reporting.Interval > 0.
type AbortConfig = engine.AbortConfig

// ErrAborted is returned (joined into the run error) when an AbortConfig limit
// is breached and the run is stopped early. Detect it with errors.Is.
var ErrAborted = engine.ErrAborted

// ErrUser marks an error as originating from scenario/user code rather than the
// target, so the run counts it under the "user_error" category instead of
// "unknown_error". Detect it with errors.Is; wrap with UserError.
var ErrUser = metrics.ErrUser

// UserError wraps err so a run counts it under the "user_error" category. It
// returns nil when err is nil. The result unwraps to both ErrUser and err, so
// errors.Is matches either. Use it inside a scenario for failures that are the
// test's responsibility (bad fixtures, business-rule violations, client-side
// validation) rather than transport or server errors:
//
//	scenario := func(ctx context.Context) (int, error) {
//	    order, err := buildOrder(feeder.Next())
//	    if err != nil {
//	        return 0, pulse.UserError(err) // counted as user_error, not unknown_error
//	    }
//	    return client.Post(ctx, url, order)
//	}
func UserError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrUser, err)
}

// StressConfig enables ramp-to-failure capacity discovery: the arrival rate
// climbs from the first phase's rate by StepRPS every healthy interval until the
// target's error rate or P99 latency breaches a failure threshold. The run then
// stops normally (no error) with Result.Stress reporting the sustained capacity.
// Requires Reporting.Interval > 0 and is mutually exclusive with Adaptive.
type StressConfig = engine.StressConfig

// StressResult reports the outcome of a ramp-to-failure run (see StressConfig).
// It is nil on Result for non-stress runs.
type StressResult = metrics.StressResult

// SaturationPolicy controls what happens when all execution slots are in use.
type SaturationPolicy = engine.SaturationPolicy

const (
	// SaturationPolicyDrop preserves the configured arrival rate by discarding
	// arrivals that cannot start immediately.
	SaturationPolicyDrop = engine.SaturationPolicyDrop
	// SaturationPolicyBlock waits for capacity, applying backpressure to the
	// scheduler. This preserves the behavior of earlier Pulse versions.
	SaturationPolicyBlock = engine.SaturationPolicyBlock
)

// Scenario is the user-defined workload executed by Pulse.
// The int is an HTTP or application status code; use 0 when not applicable.
type Scenario func(ctx context.Context) (statusCode int, err error)

// PhaseType describes how a phase should be executed.
type PhaseType = model.PhaseType

const (
	// PhaseTypeConstant represents a constant arrival-rate phase.
	PhaseTypeConstant = model.PhaseTypeConstant
	// PhaseTypeRamp represents a linear ramp between two arrival rates.
	PhaseTypeRamp = model.PhaseTypeRamp
	// PhaseTypeStep represents discrete steps between two arrival rates.
	PhaseTypeStep = model.PhaseTypeStep
	// PhaseTypeSpike represents a temporary spike from a base rate to a peak rate.
	PhaseTypeSpike = model.PhaseTypeSpike
)

// Phase defines the minimal execution shape for the MVP.
type Phase struct {
	Type        PhaseType
	Duration    time.Duration
	ArrivalRate int
	// From and To are the arrival rates (per second) at the start and end of a ramp or step phase.
	From int
	To   int
	// Steps is the number of discrete rate levels for PhaseTypeStep.
	Steps int
	// SpikeAt is when the spike starts; 0 means immediately.
	SpikeAt time.Duration
	// SpikeDuration is how long the spike lasts.
	SpikeDuration time.Duration
}

// IsConstant reports whether p is a constant arrival-rate phase.
func (p Phase) IsConstant() bool {
	return p.Type == PhaseTypeConstant
}

// IsRamp reports whether p is a linear ramp phase.
func (p Phase) IsRamp() bool {
	return p.Type == PhaseTypeRamp
}

// IsStep reports whether p is a stepped ramp phase.
func (p Phase) IsStep() bool {
	return p.Type == PhaseTypeStep
}

// IsSpike reports whether p is a spike phase.
func (p Phase) IsSpike() bool {
	return p.Type == PhaseTypeSpike
}

// Thresholds define basic pass/fail conditions for a run.
type Thresholds struct {
	ErrorRate      float64
	MaxMeanLatency time.Duration
	MaxP95Latency  time.Duration
	MaxP99Latency  time.Duration
	MaxDroppedRate float64
}

// ReportingConfig controls optional interval snapshots.
type ReportingConfig struct {
	// Interval enables temporal snapshots when greater than zero.
	Interval time.Duration
}

// Config holds execution configuration for a test.
// Config holds execution configuration for a test.
type Config struct {
	Phases         []Phase
	MaxConcurrency int
	// Seed pins the random source used by built-in middlewares (WithErrorRate,
	// WithJitter, WithLatency, WithStatusCode) so that injected-fault patterns
	// are reproducible across runs. Two runs with the same Seed, the same
	// Config, and the same scenario execution order produce identical fault
	// patterns. OS scheduling variation means exact replay is best-effort.
	// Seed is applied only when SetSeed has not already been called; set it
	// to nil to leave the random source unseeded (the default).
	Seed *int64
	// SaturationPolicy defaults to SaturationPolicyDrop.
	SaturationPolicy SaturationPolicy
	Thresholds       Thresholds
	Reporting        ReportingConfig
	// Service is optional metadata for Fabric MetricSnapshot.Service and RunCompleted payloads.
	Service string
	// Workers is an optional list of distributed worker addresses ("host:port").
	// When non-empty, RunContext fans out the test to all workers via HTTP and
	// merges their results. Workers must be started with ListenAsWorker.
	// For single-node runs (the default), leave Workers nil or empty.
	Workers []string
	// WorkerWeights optionally assigns a relative capacity to each worker, in the
	// same order as Workers. Arrival rate and concurrency are split
	// proportionally (e.g. {2,1} sends a 2:1 share to the first worker). When
	// empty or mismatched in length, workers are weighted equally.
	WorkerWeights []int
	// DistributedHTTPScenario, when non-nil, is forwarded to workers in the
	// RunRequest so CLI workers can build the HTTP scenario from config.
	// Populated by config.Load() from the YAML target section.
	// Library users with pre-registered scenarios should leave this nil.
	DistributedHTTPScenario *HTTPScenarioConfig
	// DashboardAddr, when non-empty, starts an HTTP dashboard server at the
	// given address (e.g. ":9090") that streams live metrics via SSE.
	// Open http://localhost:9090 in a browser while the run is active.
	DashboardAddr string
	OnResult      ResultHook     // optional; nil means no-op
	OnFabricEmit  FabricEmitHook // optional; protobuf RunEvent + RunCompleted Event
	// OnSnapshot is called at the end of each reporting interval with the
	// metrics observed during that window. It is invoked from a background
	// goroutine and must not block. Only active when Reporting.Interval > 0.
	OnSnapshot func(snapshot Snapshot)
	// Adaptive, when non-zero, enables real-time RPS auto-tuning for
	// PhaseTypeConstant phases based on observed error rate and P99 latency.
	// Requires Reporting.Interval > 0.
	Adaptive AdaptiveConfig
	// Abort, when non-zero, stops the run early (fail-fast) when a reporting
	// interval breaches a configured error-rate or P99-latency limit. The run
	// error is wrapped with ErrAborted. Requires Reporting.Interval > 0.
	Abort AbortConfig
	// Stress, when non-zero, enables ramp-to-failure capacity discovery. The run
	// stops when a failure threshold is breached and Result.Stress is populated.
	// Requires Reporting.Interval > 0, is mutually exclusive with Adaptive, and is
	// not supported in distributed mode (Workers).
	Stress StressConfig
	// Percentiles lists additional latency percentiles (values in (0,100), e.g.
	// 99.9) to compute for the final result, in addition to the always-reported
	// P50/P90/P95/P99. Reported in Result.ExtraPercentiles keyed by label
	// ("p99.9"). Out-of-range values are rejected by validation.
	Percentiles []float64
	// Reporters is an optional list of metric exporters called on each snapshot
	// interval and once after the run completes. Requires Reporting.Interval > 0
	// for OnSnapshot to fire; OnResult is always called.
	Reporters []Reporter
}

// Test is the root public input for a Pulse run.
type Test struct {
	Config   Config
	Scenario Scenario
}

// LatencyStats contains aggregate latency data.
type LatencyStats struct {
	Min  time.Duration
	Mean time.Duration
	P50  time.Duration
	P90  time.Duration
	P95  time.Duration
	P99  time.Duration
	Max  time.Duration
}

// ThresholdOutcome records whether a configured threshold passed for a run.
type ThresholdOutcome struct {
	Pass        bool
	Description string
}

// Result contains the aggregated outcome of a test run.
type Result struct {
	Total             int64
	Failed            int64
	Duration          time.Duration
	RPS               float64
	Scheduled         int64
	Started           int64
	Dropped           int64
	DroppedRate       float64
	Completed         int64
	MaxActive         int64
	Latency           LatencyStats
	// TTFB holds time-to-first-byte statistics for HTTP scenarios. Zero-valued
	// when the transport did not report a first-byte time.
	TTFB LatencyStats
	// BytesIn and BytesOut are the total response and request bytes observed
	// across the run. Throughput is BytesIn / Duration (and BytesOut / Duration).
	BytesIn           int64
	BytesOut          int64
	// Stress reports the ramp-to-failure outcome when Config.Stress is enabled;
	// nil otherwise.
	Stress            *StressResult
	StatusCounts      map[int]int64
	ErrorCounts       map[string]int64
	ThresholdOutcomes []ThresholdOutcome `json:"-"`
	Snapshots         []Snapshot
	// ExtraPercentiles holds additional latency percentiles requested via
	// Config.Percentiles, keyed by label (e.g. "p99.9"). Nil when none were
	// requested.
	ExtraPercentiles map[string]time.Duration
}

// Snapshot contains metrics observed during one reporting interval.
type Snapshot struct {
	StartedAt    time.Time
	Duration     time.Duration
	Total        int64
	Failed       int64
	RPS          float64
	Scheduled    int64
	Started      int64
	Dropped      int64
	DroppedRate  float64
	Completed    int64
	MaxActive    int64
	Latency      LatencyStats
	TTFB         LatencyStats
	BytesIn      int64
	BytesOut     int64
	StatusCounts map[int]int64
	ErrorCounts  map[string]int64
}

// Reporter receives metrics during and after a test run. Register reporters
// via Config.Reporters to export live and final metrics to external systems
// (Prometheus, InfluxDB, Datadog, …) without modifying core pulse logic.
type Reporter interface {
	// OnSnapshot is called at the end of each reporting interval with the
	// metrics observed during that window. Called from a background goroutine;
	// must not block.
	OnSnapshot(snapshot Snapshot)
	// OnResult is called once after the run completes with the full aggregated
	// result and whether all configured thresholds passed.
	OnResult(result Result, passed bool)
}

// ResultHook is an optional callback invoked after a test run completes.
// result contains the full aggregated metrics.
// passed is true when execution completed and all configured thresholds were met.
type ResultHook func(result Result, passed bool)

// FabricEmitHook is invoked after threshold evaluation with protobuf contracts for the Fabric stack.
// run carries fabric.v1.MetricSnapshot; completed is EVENT_TYPE_RUN_COMPLETED for tools like Beacon.
type FabricEmitHook func(run *fabricv1.RunEvent, completed *fabricv1.Event)

// HTTPScenarioConfig holds the HTTP target parameters for distributed workers.
// When set alongside Config.Workers, the coordinator forwards these to each worker
// so they can build the HTTP scenario without a local config file.
// This is populated automatically by config.Load(); library users with custom
// scenarios (pre-registered in workers via ListenAsWorker) should leave it nil.
type HTTPScenarioConfig struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
	// Checks, when set, are response assertions forwarded to CLI workers so a
	// distributed run evaluates the same checks as a local run.
	Checks *transport.Checks
}

// Run validates the test definition and executes it through the engine.
// Use RunContext when the caller needs cancellation or a global timeout.
func Run(test Test) (Result, error) {
	return RunContext(context.Background(), test)
}

// ListenAsWorker starts a distributed worker server on addr that accepts
// RunRequests from a coordinator and executes scenario at the directed rate.
// It blocks until ctx is cancelled. Use in library mode alongside RunContext
// with Config.Workers set on the coordinator side.
func ListenAsWorker(ctx context.Context, addr string, scenario Scenario) error {
	return worker.New(scenario).ListenAndServe(ctx, addr)
}

// RunContext validates the test definition and executes it through the engine.
// The context controls scheduling and in-flight scenario executions.
// When test.Config.Workers is non-empty, the run is distributed across those
// workers; otherwise it executes locally.
func RunContext(ctx context.Context, test Test) (Result, error) {
	if err := validateTest(test); err != nil {
		return Result{}, err
	}
	// Apply the seed only when the caller has not already called SetSeed
	// directly, preventing double-seeding when running multiple tests in the
	// same process.
	if test.Config.Seed != nil && !hasSeed() {
		SetSeed(*test.Config.Seed)
	}

	if len(test.Config.Workers) > 0 {
		return runDistributed(ctx, test)
	}

	// Wire the dashboard server when DashboardAddr is set. The server runs in a
	// background goroutine and shuts down with the context. Its onSnap/onDone
	// callbacks are composed with any user-provided OnSnapshot/OnResult hooks.
	userOnSnapshot := test.Config.OnSnapshot
	var dashOnDone func(Result, bool)
	if test.Config.DashboardAddr != "" {
		dashOnSnap, dashDone := startDashboard(ctx, test.Config.DashboardAddr)
		dashOnDone = dashDone
		prevOnSnapshot := userOnSnapshot
		userOnSnapshot = func(s Snapshot) {
			dashOnSnap(s)
			if prevOnSnapshot != nil {
				prevOnSnapshot(s)
			}
		}
		host := test.Config.DashboardAddr
		if host[0] == ':' {
			host = "localhost" + host
		}
		fmt.Fprintf(os.Stderr, "Dashboard: http://%s\n", host)
	}

	// Compose reporters into the snapshot callback chain.
	for _, rep := range test.Config.Reporters {
		rep := rep
		prev := userOnSnapshot
		userOnSnapshot = func(s Snapshot) {
			rep.OnSnapshot(s)
			if prev != nil {
				prev(s)
			}
		}
	}

	var onLiveSnapshot func(metrics.Snapshot)
	if userOnSnapshot != nil {
		onLiveSnapshot = func(s metrics.Snapshot) {
			userOnSnapshot(toSnapshot(s))
		}
	}

	execution := engine.NewWithOptions(
		toSchedulerPhases(test.Config.Phases),
		test.Scenario,
		engine.Options{
			MaxConcurrency: test.Config.MaxConcurrency,
			Saturation:     normalizedSaturationPolicy(test.Config.SaturationPolicy),
			ReportInterval: test.Config.Reporting.Interval,
			OnLiveSnapshot: onLiveSnapshot,
			Adaptive:       test.Config.Adaptive,
			Abort:          test.Config.Abort,
			Stress:         test.Config.Stress,
			Percentiles:    test.Config.Percentiles,
		},
	)

	startedAt := time.Now()
	metricsResult, err := execution.Run(ctx)
	result := metricsResultToResult(metricsResult)

	outcomes, threshErr := evaluateThresholds(test.Config.Thresholds, result)
	result.ThresholdOutcomes = outcomes

	passed := err == nil && threshErr == nil

	if dashOnDone != nil {
		dashOnDone(result, passed)
	}

	for _, rep := range test.Config.Reporters {
		rep.OnResult(result, passed)
	}

	if test.Config.OnFabricEmit != nil {
		emit := ToFabricRunEmit(test.Config.Service, result, passed, startedAt)
		test.Config.OnFabricEmit(emit.RunEvent, emit.RunCompleted)
	}

	if test.Config.OnResult != nil {
		test.Config.OnResult(result, passed)
	}

	if err != nil {
		return result, errors.Join(err, threshErr)
	}

	return result, threshErr
}

// ValidateConfig validates the Config fields of a Test: phases, saturation
// policy, concurrency limits, reporting interval, and thresholds. It is called
// by validateTest and is also available to external packages (such as
// config.Load) that build a pulse.Config from another representation and want
// early error reporting before constructing a full Test.
func ValidateConfig(cfg Config) error {
	if len(cfg.Phases) == 0 {
		return errNoPhases
	}

	if policy := cfg.SaturationPolicy; policy != "" &&
		policy != SaturationPolicyDrop && policy != SaturationPolicyBlock {
		return errUnsupportedSaturation
	}

	if cfg.MaxConcurrency < 0 {
		return errNegativeMaxConcurrency
	}
	if cfg.MaxConcurrency > maxConcurrency {
		return errMaxConcurrencyTooHigh
	}

	if cfg.Reporting.Interval < 0 {
		return errNegativeReportInterval
	}
	if cfg.Reporting.Interval > 0 && cfg.Reporting.Interval < minReportingInterval {
		return errReportIntervalTooSmall
	}

	var totalDuration time.Duration
	for _, phase := range cfg.Phases {
		if phase.Duration <= 0 {
			return errNonPositivePhase
		}
		if totalDuration > time.Duration(1<<63-1)-phase.Duration {
			return errTooManySnapshots
		}
		totalDuration += phase.Duration
		if cfg.Reporting.Interval > 0 &&
			(totalDuration-1)/cfg.Reporting.Interval+1 > maxSnapshots {
			return errTooManySnapshots
		}

		pt := PhaseType(strings.TrimSpace(string(phase.Type)))
		if pt == "" {
			return errEmptyPhaseType
		}

		p := Phase{Type: pt}
		switch {
		case p.IsRamp():
			if phase.From <= 0 || phase.To <= 0 {
				return errInvalidRampEndpoints
			}
		case p.IsConstant():
			if phase.ArrivalRate <= 0 {
				return errNonPositiveArrivalRate
			}
		case p.IsStep():
			if phase.From <= 0 || phase.To <= 0 || phase.Steps <= 0 {
				return errInvalidStepConfig
			}
		case p.IsSpike():
			if phase.From <= 0 || phase.To <= 0 || phase.SpikeAt < 0 ||
				phase.SpikeDuration <= 0 || phase.SpikeAt+phase.SpikeDuration > phase.Duration {
				return errInvalidSpikeConfig
			}
		default:
			return errUnsupportedPhaseType
		}
	}

	if cfg.Thresholds.ErrorRate < 0 {
		return errNegativeErrorRate
	}
	if cfg.Thresholds.ErrorRate > 1 {
		return errErrorRateAboveOne
	}
	if cfg.Thresholds.MaxMeanLatency < 0 {
		return errNegativeMeanLatency
	}
	if cfg.Thresholds.MaxP95Latency < 0 {
		return errNegativeP95Latency
	}
	if cfg.Thresholds.MaxP99Latency < 0 {
		return errNegativeP99Latency
	}
	if cfg.Thresholds.MaxDroppedRate < 0 {
		return errNegativeDroppedRate
	}
	if cfg.Thresholds.MaxDroppedRate > 1 {
		return errDroppedRateAboveOne
	}

	if !cfg.Adaptive.IsZero() {
		if cfg.Reporting.Interval == 0 {
			return errAdaptiveRequiresInterval
		}
		if cfg.Adaptive.MaxErrorRate < 0 || cfg.Adaptive.MaxErrorRate > 1 {
			return errAdaptiveInvalidErrorRate
		}
		if cfg.Adaptive.MaxP99 < 0 {
			return errAdaptiveInvalidP99
		}
	}

	if !cfg.Abort.IsZero() {
		if cfg.Reporting.Interval == 0 {
			return errAbortRequiresInterval
		}
		if cfg.Abort.MaxErrorRate < 0 || cfg.Abort.MaxErrorRate > 1 {
			return errAbortInvalidErrorRate
		}
		if cfg.Abort.MaxP99 < 0 {
			return errAbortInvalidP99
		}
	}

	if !cfg.Stress.IsZero() {
		if cfg.Reporting.Interval == 0 {
			return errStressRequiresInterval
		}
		if cfg.Stress.MaxErrorRate < 0 || cfg.Stress.MaxErrorRate > 1 {
			return errStressInvalidErrorRate
		}
		if cfg.Stress.MaxP99 < 0 {
			return errStressInvalidP99
		}
		if cfg.Stress.StepRPS < 0 || cfg.Stress.MaxRPS < 0 {
			return errStressNegativeRate
		}
		if !cfg.Adaptive.IsZero() {
			return errStressAdaptiveExclusive
		}
		if len(cfg.Workers) > 0 {
			return errStressDistributedUnsupported
		}
	}

	for _, p := range cfg.Percentiles {
		if p <= 0 || p >= 100 {
			return errInvalidPercentile
		}
	}

	return nil
}

func validateTest(test Test) error {
	if err := ValidateConfig(test.Config); err != nil {
		return err
	}
	if test.Scenario == nil {
		return errNilScenario
	}
	return nil
}

func toSnapshot(s metrics.Snapshot) Snapshot {
	return Snapshot{
		StartedAt:    s.StartedAt,
		Duration:     s.Duration,
		Total:        s.Total,
		Failed:       s.Failed,
		RPS:          s.RPS,
		Scheduled:    s.Scheduled,
		Started:      s.Started,
		Dropped:      s.Dropped,
		DroppedRate:  s.DroppedRate,
		Completed:    s.Completed,
		MaxActive:    s.MaxActive,
		Latency:      toLatencyStats(s.Latency),
		StatusCounts: s.StatusCounts,
		ErrorCounts:  s.ErrorCounts,
	}
}

func toSnapshots(snapshots []metrics.Snapshot) []Snapshot {
	if len(snapshots) == 0 {
		return nil
	}
	result := make([]Snapshot, len(snapshots))
	for i, snapshot := range snapshots {
		result[i] = Snapshot{
			StartedAt:    snapshot.StartedAt,
			Duration:     snapshot.Duration,
			Total:        snapshot.Total,
			Failed:       snapshot.Failed,
			RPS:          snapshot.RPS,
			Scheduled:    snapshot.Scheduled,
			Started:      snapshot.Started,
			Dropped:      snapshot.Dropped,
			DroppedRate:  snapshot.DroppedRate,
			Completed:    snapshot.Completed,
			MaxActive:    snapshot.MaxActive,
			Latency:      toLatencyStats(snapshot.Latency),
			TTFB:         toLatencyStats(snapshot.TTFB),
			BytesIn:      snapshot.BytesIn,
			BytesOut:     snapshot.BytesOut,
			StatusCounts: snapshot.StatusCounts,
			ErrorCounts:  snapshot.ErrorCounts,
		}
	}
	return result
}

func toLatencyStats(latency metrics.LatencyStats) LatencyStats {
	return LatencyStats{
		Min:  latency.Min,
		Mean: latency.Mean,
		P50:  latency.P50,
		P90:  latency.P90,
		P95:  latency.P95,
		P99:  latency.P99,
		Max:  latency.Max,
	}
}

func normalizedSaturationPolicy(policy SaturationPolicy) SaturationPolicy {
	if policy == "" {
		return SaturationPolicyDrop
	}
	return policy
}

func evaluateThresholds(thresholds Thresholds, result Result) ([]ThresholdOutcome, error) {
	var outcomes []ThresholdOutcome
	var errs []error

	if thresholds.ErrorRate > 0 {
		var errorRate float64
		if result.Total > 0 {
			errorRate = float64(result.Failed) / float64(result.Total)
		}

		limitStr := strconv.FormatFloat(thresholds.ErrorRate, 'f', -1, 64)
		desc := "error_rate < " + limitStr
		if errorRate > thresholds.ErrorRate {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      errorRate,
				Limit:       thresholds.ErrorRate,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxMeanLatency > 0 {
		desc := fmt.Sprintf("mean_latency < %v", thresholds.MaxMeanLatency)
		if result.Latency.Mean > thresholds.MaxMeanLatency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.Mean,
				Limit:       thresholds.MaxMeanLatency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxP95Latency > 0 {
		desc := fmt.Sprintf("p95_latency < %v", thresholds.MaxP95Latency)
		if result.Latency.P95 > thresholds.MaxP95Latency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.P95,
				Limit:       thresholds.MaxP95Latency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxP99Latency > 0 {
		desc := fmt.Sprintf("p99_latency < %v", thresholds.MaxP99Latency)
		if result.Latency.P99 > thresholds.MaxP99Latency {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.Latency.P99,
				Limit:       thresholds.MaxP99Latency,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	if thresholds.MaxDroppedRate > 0 {
		limitStr := strconv.FormatFloat(thresholds.MaxDroppedRate, 'f', -1, 64)
		desc := "dropped_rate < " + limitStr
		if result.DroppedRate > thresholds.MaxDroppedRate {
			outcomes = append(outcomes, ThresholdOutcome{Pass: false, Description: desc})
			errs = append(errs, &ThresholdViolationError{
				Description: desc,
				Actual:      result.DroppedRate,
				Limit:       thresholds.MaxDroppedRate,
			})
		} else {
			outcomes = append(outcomes, ThresholdOutcome{Pass: true, Description: desc})
		}
	}

	return outcomes, errors.Join(errs...)
}

// runDistributed fans out the test to the workers in test.Config.Workers and
// merges their results. It does not execute any scenario locally.
func runDistributed(ctx context.Context, test Test) (Result, error) {
	// The shared worker token is read from the environment (never from YAML) so
	// it does not end up in version-controlled configs. It must match the token
	// configured on each worker (PULSE_WORKER_TOKEN).
	c := coordinator.NewWithOptions(test.Config.Workers, coordinator.Options{
		AuthToken: os.Getenv("PULSE_WORKER_TOKEN"),
		Weights:   test.Config.WorkerWeights,
	})

	req := distributed.RunRequest{
		Phases:           toDistributedPhases(test.Config.Phases),
		MaxConcurrency:   test.Config.MaxConcurrency,
		SaturationPolicy: string(normalizedSaturationPolicy(test.Config.SaturationPolicy)),
		HTTPScenario:     toDistributedHTTPScenario(test.Config.DistributedHTTPScenario),
	}

	startedAt := time.Now()
	metricsResult, err := c.Run(ctx, req)

	result := metricsResultToResult(metricsResult)
	outcomes, threshErr := evaluateThresholds(test.Config.Thresholds, result)
	result.ThresholdOutcomes = outcomes

	passed := err == nil && threshErr == nil
	if test.Config.OnFabricEmit != nil {
		emit := ToFabricRunEmit(test.Config.Service, result, passed, startedAt)
		test.Config.OnFabricEmit(emit.RunEvent, emit.RunCompleted)
	}
	if test.Config.OnResult != nil {
		test.Config.OnResult(result, passed)
	}
	if err != nil {
		return result, errors.Join(err, threshErr)
	}
	return result, threshErr
}

func toDistributedHTTPScenario(cfg *HTTPScenarioConfig) *distributed.HTTPScenario {
	if cfg == nil {
		return nil
	}
	return &distributed.HTTPScenario{
		URL:     cfg.URL,
		Method:  cfg.Method,
		Headers: cfg.Headers,
		Body:    cfg.Body,
		Checks:  toDistributedChecks(cfg.Checks),
	}
}

func toDistributedChecks(c *transport.Checks) *distributed.HTTPChecks {
	if c == nil {
		return nil
	}
	return &distributed.HTTPChecks{
		Status:       c.Status,
		HeaderEquals: c.HeaderEquals,
		BodyContains: c.BodyContains,
		JSONEquals:   c.JSONEquals,
	}
}

func toDistributedPhases(phases []Phase) []distributed.Phase {
	out := make([]distributed.Phase, len(phases))
	for i, p := range phases {
		out[i] = distributed.Phase{
			Type:          string(p.Type),
			Duration:      p.Duration,
			ArrivalRate:   p.ArrivalRate,
			From:          p.From,
			To:            p.To,
			Steps:         p.Steps,
			SpikeAt:       p.SpikeAt,
			SpikeDuration: p.SpikeDuration,
		}
	}
	return out
}

func metricsResultToResult(m metrics.Result) Result {
	return Result{
		Total:        m.Total,
		Failed:       m.Failed,
		Duration:     m.Duration,
		RPS:          m.RPS,
		Scheduled:    m.Scheduled,
		Started:      m.Started,
		Dropped:      m.Dropped,
		DroppedRate:  m.DroppedRate,
		Completed:    m.Completed,
		MaxActive:    m.MaxActive,
		StatusCounts:     m.StatusCounts,
		ErrorCounts:      m.ErrorCounts,
		Snapshots:        toSnapshots(m.Snapshots),
		ExtraPercentiles: m.ExtraPercentiles,
		BytesIn:          m.BytesIn,
		BytesOut:         m.BytesOut,
		Stress:           m.Stress,
		Latency:          toLatencyStats(m.Latency),
		TTFB:             toLatencyStats(m.TTFB),
	}
}

func toSchedulerPhases(phases []Phase) []scheduler.Phase {
	schedulerPhases := make([]scheduler.Phase, len(phases))
	for i := range phases {
		schedulerPhases[i] = scheduler.Phase{
			Type:          PhaseType(strings.TrimSpace(string(phases[i].Type))),
			Duration:      phases[i].Duration,
			ArrivalRate:   phases[i].ArrivalRate,
			From:          phases[i].From,
			To:            phases[i].To,
			Steps:         phases[i].Steps,
			SpikeAt:       phases[i].SpikeAt,
			SpikeDuration: phases[i].SpikeDuration,
		}
	}

	return schedulerPhases
}

// ── Dashboard integration ─────────────────────────────────────────────────────

// dashboardSnapshotDTO is the JSON-serializable form of a Snapshot emitted to
// the dashboard SSE stream. Fields use snake_case and durations are in
// nanoseconds (matching JS expectations: divide by 1e6 to get milliseconds).
type dashboardSnapshotDTO struct {
	StartedAt   time.Time `json:"started_at"`
	DurationNs  int64     `json:"duration_ns"`
	Total       int64     `json:"total"`
	Failed      int64     `json:"failed"`
	RPS         float64   `json:"rps"`
	Scheduled   int64     `json:"scheduled"`
	Started     int64     `json:"started"`
	Dropped     int64     `json:"dropped"`
	DroppedRate float64   `json:"dropped_rate"`
	Completed   int64     `json:"completed"`
	MaxActive   int64     `json:"max_active"`
	BytesIn     int64     `json:"bytes_in"`
	BytesOut    int64     `json:"bytes_out"`
	Latency     dashboardLatencyDTO `json:"latency"`
	TTFB        dashboardLatencyDTO `json:"ttfb"`
}

type dashboardLatencyDTO struct {
	MinNs  int64 `json:"min_ns"`
	MeanNs int64 `json:"mean_ns"`
	P50Ns  int64 `json:"p50_ns"`
	P90Ns  int64 `json:"p90_ns"`
	P95Ns  int64 `json:"p95_ns"`
	P99Ns  int64 `json:"p99_ns"`
	MaxNs  int64 `json:"max_ns"`
}

type dashboardResultDTO struct {
	DurationMs  float64             `json:"duration_ms"`
	Total       int64               `json:"total"`
	Failed      int64               `json:"failed"`
	RPS         float64             `json:"rps"`
	Dropped     int64               `json:"dropped"`
	DroppedRate float64             `json:"dropped_rate"`
	BytesIn     int64               `json:"bytes_in"`
	BytesOut    int64               `json:"bytes_out"`
	Latency     dashboardLatencyDTO `json:"latency"`
	TTFB        dashboardLatencyDTO `json:"ttfb"`
	Passed      bool                `json:"passed"`
}

func toDashboardLatency(l LatencyStats) dashboardLatencyDTO {
	return dashboardLatencyDTO{
		MinNs:  int64(l.Min),
		MeanNs: int64(l.Mean),
		P50Ns:  int64(l.P50),
		P90Ns:  int64(l.P90),
		P95Ns:  int64(l.P95),
		P99Ns:  int64(l.P99),
		MaxNs:  int64(l.Max),
	}
}

func snapshotToDTO(s Snapshot) dashboardSnapshotDTO {
	return dashboardSnapshotDTO{
		StartedAt:   s.StartedAt,
		DurationNs:  int64(s.Duration),
		Total:       s.Total,
		Failed:      s.Failed,
		RPS:         s.RPS,
		Scheduled:   s.Scheduled,
		Started:     s.Started,
		Dropped:     s.Dropped,
		DroppedRate: s.DroppedRate,
		Completed:   s.Completed,
		MaxActive:   s.MaxActive,
		BytesIn:     s.BytesIn,
		BytesOut:    s.BytesOut,
		Latency:     toDashboardLatency(s.Latency),
		TTFB:        toDashboardLatency(s.TTFB),
	}
}

func resultToDTO(r Result, passed bool) dashboardResultDTO {
	return dashboardResultDTO{
		DurationMs:  float64(r.Duration.Milliseconds()),
		Total:       r.Total,
		Failed:      r.Failed,
		RPS:         r.RPS,
		Dropped:     r.Dropped,
		DroppedRate: r.DroppedRate,
		BytesIn:     r.BytesIn,
		BytesOut:    r.BytesOut,
		Passed:      passed,
		Latency:     toDashboardLatency(r.Latency),
		TTFB:        toDashboardLatency(r.TTFB),
	}
}

// startDashboard starts a dashboard server in the background and returns the
// wired OnSnapshot and completion callbacks. It prints the dashboard URL to
// stderr so the user knows where to connect.
func startDashboard(ctx context.Context, addr string) (onSnap func(Snapshot), onDone func(Result, bool)) {
	srv := dashboard.New()
	go func() {
		if err := srv.ListenAndServe(ctx, addr); err != nil {
			fmt.Fprintf(os.Stderr, "dashboard: %v\n", err)
		}
	}()
	onSnap = func(s Snapshot) { srv.Push(snapshotToDTO(s)) }
	onDone = func(r Result, passed bool) { srv.Complete(resultToDTO(r, passed), passed) }
	return
}
