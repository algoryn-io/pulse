# Changelog

All notable changes to this project will be documented in this file.

---
## [Unreleased]

### Added

- **Env var interpolation in YAML** — `config.Load` now expands `${VAR}` and `${VAR:-default}` placeholders in YAML files before unmarshalling; unset required variables return a descriptive error naming the missing variable; useful for secrets (`${API_TOKEN}`), environment-specific URLs (`${BASE_URL}`), and CI overrides

- **Data injection / Feeder** — `pulse.NewFeeder[T](items []T)` returns a generic, thread-safe feeder that supplies values to concurrent scenario invocations round-robin; `pulse.NewFeederFunc[T](fn func() T)` supports generated or random values; both expose a single `Next() T` method with no allocations in the hot path
- **Response assertions** — `transport.Response` type returned by the new `HTTPClient.DoWithResponse(ctx, method, url, body)` method; unlike `Do`, status >= 400 does not produce an error, giving callers full control via assertion helpers: `AssertStatus(resp, expected)`, `AssertBodyContains(resp, substr)`, `AssertBodyJSON(resp, &v)`, `AssertHeader(resp, key, expected)`; the body is pre-read into memory (up to `MaxResponseBytes`) so helpers can inspect it without draining

---
## [v0.5.0] — 2026-06-20

### Added

- **Adaptive load shaping** — `Config.Adaptive` (`AdaptiveConfig`) enables real-time RPS auto-tuning for `PhaseTypeConstant` phases; the engine checks each reporting interval and multiplies the arrival rate by `StepDown` when `MaxErrorRate` or `MaxP99` is exceeded, and by `StepUp` when conditions recover; rate is clamped to `[MinRPS, MaxRPS]`; requires `Reporting.Interval > 0`
- `scheduler.Phase.RateFunc func() float64` — optional per-tick rate override; when non-nil, the scheduler calls it before each token-bucket refill so external controllers (e.g. `adaptiveController`) can adjust arrival rate without restarting the phase
- **Chaos / fault injection** — `transport.ChaosRoundTripper` wraps any `http.RoundTripper` and injects configurable synthetic faults: `ChaosConfig.ErrorRate` (fraction of requests that return `ErrChaosInjected` without forwarding) and `ChaosConfig.LatencyRate` + `ChaosConfig.Latency` (fraction of requests that receive an extra sleep before forwarding); error injection takes precedence over latency; latency sleep respects context cancellation; construct with `transport.NewChaosRoundTripper(inner, cfg)`; panics if rates are outside `[0, 1]`
- `transport.ErrChaosInjected` — sentinel error returned by `ChaosRoundTripper` on injected failures; detect with `errors.Is`
- **Plugin reporters** — `pulse.Reporter` interface (`OnSnapshot(Snapshot)` + `OnResult(Result, bool)`) for extensible metric export; wire via `Config.Reporters []Reporter`; `OnSnapshot` is called at each reporting interval, `OnResult` once after the run and threshold evaluation complete
- `reporter.NewPrometheusReporter(ctx, addr)` — serves Prometheus text exposition format at `/metrics`; live snapshot gauge updates on each interval; final result written on `OnResult`; server shuts down when `ctx` is cancelled
- `reporter.NewInfluxDBReporter(cfg)` — writes InfluxDB v2 line protocol to `/api/v2/write` via HTTP with Bearer token auth; snapshots are sent asynchronously (non-blocking); final result is sent synchronously on `OnResult`
- `reporter.NewDatadogReporter(cfg)` — emits DogStatsD UDP datagrams; supports `Namespace` prefix and global `Tags` suffix; fires on each snapshot and on final result
- **Live dashboard** — `Config.DashboardAddr` (e.g. `":9090"`) or `--dashboard :9090` on the CLI starts an SSE-based HTTP server that streams per-interval metrics to a browser in real time; the page displays RPS, latency percentile, and error-rate charts updated every reporting interval; a "Run complete" banner is shown when the test finishes; the server shuts down when the run context is cancelled; `Config.OnSnapshot func(Snapshot)` exposes the same data to Go callers; `dashboard/` package is embeddable separately via `dashboard.Server`
- `engine.Options.OnLiveSnapshot func(metrics.Snapshot)` — per-interval callback invoked from a background goroutine at the end of each completed reporting window; enables real-time metric streaming without polling

- `RunContext(ctx, test)` for cancellation and global deadlines
- Explicit saturation policies: `drop` (default) and legacy-compatible `block`
- Load-fidelity result fields: scheduled, started, dropped, dropped rate, completed, and maximum active requests
- `thresholds.maxDroppedRate` and `saturationPolicy` YAML settings
- Optional interval snapshots through `Config.Reporting.Interval` and `reporting.interval`
- Snapshot JSON output for transient load, failure, and latency analysis
- `pulse.ValidateConfig(cfg Config) error` — exported function so packages that build a `pulse.Config` (e.g. `config/`) can reuse phase, threshold, concurrency, and reporting validation without duplicating rules
- `transport.HTTPClientConfig` now exposes `MaxIdleConns`, `MaxIdleConnsPerHost`, and `DisableKeepAlives`; YAML config supports the same fields under `target` so connection-pool behavior can be tuned without writing Go code
- `--dry-run` CLI flag: validates the config file, prints a per-phase summary and total duration, then exits without sending any traffic — safe to use in pre-flight checks and PR pipelines
- `mockserver` modes `slow` (configurable `--slow-delay`, default 120ms), `flaky` (configurable `--flaky-rate`, default 0.3), and `down` (always 503); context-aware sleep in `slow` releases goroutines immediately on client disconnect
- Benchmarks: `BenchmarkSchedulerConstant`, `BenchmarkSchedulerRamp`, `BenchmarkSchedulerStep` (scheduler), `BenchmarkEngineRun`, `BenchmarkEngineRunWithConcurrencyLimit`, `BenchmarkEngineRunDropPolicy`, `BenchmarkEngineRunMultiPhase` (engine), `BenchmarkAggregatorRecord`, `BenchmarkAggregatorRecordWithError`, `BenchmarkAggregatorRecord_Parallel`, `BenchmarkAggregatorResult` (metrics)
- Optional SSRF policy: allowlist and denylist of hosts and CIDR ranges, enforced at HTTP client construction; opt-in via `transport.SSRFPolicy` so trusted YAML targets remain unrestricted by default

### Changed

- YAML loading now rejects unknown fields and invalid operational limits before execution
- Spike phases must fit entirely inside their enclosing phase
- CLI text and JSON output now report generator saturation metrics
- `config/` validation no longer duplicates phase, threshold, concurrency, or reporting rules — these are fully delegated to `pulse.ValidateConfig`; `config.validateConfig` only checks target-specific fields (method, URL, timeout)
- Scheduler poll loop uses `time.NewTimer` + `Reset` instead of `time.After` to avoid leaking one timer channel allocation per poll iteration under high arrival rates
- `WithRetry` middleware now checks `ctx.Err()` before each retry attempt and returns immediately on cancellation, instead of waiting for the next scheduled attempt
- `mockserver` extracts each mode into a named constructor function (`healthyHandler`, `mixedErrorsHandler`, `slowHandler`, `flakyHandler`, `downHandler`) and requires an explicit `--mode` flag; the previous implicit default behavior is unchanged
- CLI now requires exactly one positional `<config.yaml>` argument and returns a usage error when it is missing; the undocumented httpbin.org fallback has been removed

### Fixed

- Scheduler poll loop: replaced `time.After` with `time.NewTimer` + `Reset` to prevent a timer-channel leak on every poll tick at high arrival rates
- `WithRetry`: context cancellation now aborts immediately instead of waiting for the retry delay to elapse
- Middleware RNG: replaced a single global `rand.Source` protected by a mutex with a `sync.Pool` of per-goroutine RNG sources, eliminating lock contention under parallel scenario execution
- Circuit breaker half-open: concurrent probes are now bounded by an atomic counter so only the first probe is admitted while the rest continue to be rejected, preventing thundering-herd re-admission during recovery
- `--out` file writing: output is written to a temp file in the same directory and atomically renamed to the final path, preventing partial writes and defending against symlink-following attacks

---
## [v0.2.0] — 2026-03-24

### Added

**Scheduler**
- `step` phase: moves arrival rate from `from` to `to` in discrete levels over a given duration (`steps` controls how many levels)
- `spike` phase: maintains a base rate (`from`), bursts to a peak rate (`to`) for `spikeDuration` starting at `spikeAt`, then returns to base

**Transport**
- HTTP client now supports PUT, DELETE, and PATCH in addition to GET and POST
- Generic `Do(ctx, method, url, body)` method for method-agnostic execution

**Public API (`pulse` package)**
- `ResultHook` type: `func(Result, bool)` — optional callback invoked after every run
- `OnResult` field in `Config` — receives the full `Result` and a `passed` bool after threshold evaluation
- `PhaseTypeStep` and `PhaseTypeSpike` constants
- `Steps`, `SpikeAt`, `SpikeDuration` fields in `Phase`

**Config (YAML)**
- Supports `step` and `spike` phase types
- `target.method` now accepts PUT, DELETE, PATCH
- New fields: `steps`, `spikeAt`, `spikeDuration`

**Examples**
- `examples/put-json.yaml` — PUT request with JSON body
- `examples/step.yaml` — step phase from 10 to 100 RPS in 5 levels
- `examples/spike.yaml` — spike from 20 RPS base to 300 RPS burst

---

## [v0.1.0] — 2026-03-22

Initial release of Pulse.

### Added

**Engine**
- Phased execution model: runs phases sequentially through the scheduler
- Bounded concurrency via an internal semaphore limiter (`maxConcurrency`)

**Scheduler**
- `constant` phase: fires requests at a fixed arrival rate (requests/sec) for a given duration
- `ramp` phase: linearly interpolates arrival rate between `from` and `to` over a given duration

**Metrics**
- Total and failed request counts
- Throughput (RPS) computed from wall-clock duration
- Latency: min, mean, p50, p95, p99, max (thread-safe, incremental computation)
- Status code distribution (HTTP status → count)
- Normalized error categories: `http_status_error`, `deadline_exceeded`, `context_canceled`, `unknown_error`

**Thresholds**
- `error_rate`: fail if observed error rate exceeds the configured fraction
- `maxMeanLatency`: fail if mean latency exceeds the configured duration
- Outcomes reported as `PASS` / `FAIL` in CLI output

**Transport**
- HTTP client with GET and POST support (`net/http`)
- Responses with status ≥ 400 are counted as failures, tracked in status code distribution, and categorized as `http_status_error`

**CLI**
- `pulse run <config.yaml>` — runs a load test from a YAML config file
- `--json` — prints results as JSON to stdout
- `--out <file>` — writes JSON results to a file
- Human-readable text output by default (totals, latency, status codes, errors, thresholds)

**Config (YAML)**
- Supports `constant` and `ramp` phase types
- `target.method` (GET / POST) and `target.url`
- `maxConcurrency`
- `thresholds.errorRate` and `thresholds.maxMeanLatency`

**Public API (`pulse` package)**
- `Test`, `Config`, `Phase`, `Thresholds`, `Result`, `LatencyStats`, `ThresholdOutcome`
- `Run(Test) (Result, error)` as the single entry point
