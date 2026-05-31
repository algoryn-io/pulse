# Changelog

All notable changes to this project will be documented in this file.

---
## [Unreleased]

### Added

- `RunContext(ctx, test)` for cancellation and global deadlines
- Explicit saturation policies: `drop` (default) and legacy-compatible `block`
- Load-fidelity result fields: scheduled, started, dropped, dropped rate, completed, and maximum active requests
- `thresholds.maxDroppedRate` and `saturationPolicy` YAML settings
- Optional interval snapshots through `Config.Reporting.Interval` and `reporting.interval`
- Snapshot JSON output for transient load, failure, and latency analysis

### Changed

- YAML loading now rejects unknown fields and invalid operational limits before execution
- Spike phases must fit entirely inside their enclosing phase
- CLI text and JSON output now report generator saturation metrics

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
