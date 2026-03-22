# Changelog

All notable changes to this project will be documented in this file.

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
