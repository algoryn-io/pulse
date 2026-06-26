# Changelog

All notable changes to this project will be documented in this file.

---
## [Unreleased]

### Security

- **Distributed worker authentication** — workers now support a shared bearer token. Set `PULSE_WORKER_TOKEN` on both the worker (`pulse worker`) and the coordinator process; the worker requires `Authorization: Bearer <token>` on `/ping` and `/run` (constant-time comparison) and rejects mismatches with `401`. When the variable is unset the worker still accepts unauthenticated requests (backward compatible) but prints a prominent warning. Library API: `worker.NewWithOptions(scenario, worker.Options{AuthToken, DenyPrivate})` and `coordinator.NewWithOptions(workers, coordinator.Options{AuthToken})`. This closes an unauthenticated remote SSRF / arbitrary-load primitive on exposed workers
- **Worker SSRF policy (opt-in)** — set `PULSE_WORKER_DENY_PRIVATE=1` (or `worker.Options.DenyPrivate`) so worker-built HTTP scenarios dial through an SSRF-validating transport that rejects private, loopback, link-local, and cloud-metadata targets. Off by default because load-testing internal services is a legitimate use case
- **Worker DoS hardening** — `/run` request bodies are now capped at 1 MiB via `http.MaxBytesReader`, and the worker HTTP server sets `ReadHeaderTimeout`, `ReadTimeout`, and `IdleTimeout` to resist slowloris-style attacks
- **SSRF bypass fixes** (`internal/ssrf`) — the policy now blocks a request if **any** resolved address is private (previously only the first was checked, allowing multi-A and DNS-rebinding bypasses); IPv4-mapped IPv6 literals (e.g. `::ffff:169.254.169.254`) are normalized before matching; NAT64 prefixes (`64:ff9b::/96`, `64:ff9b:1::/48`) are blocked; new `ssrf.NewSafeTransport(policy, base)` validates the target **at dial time** and pins the validated IP, closing the DNS-rebinding TOCTOU window that `Check` alone cannot

### Added

- **YAML WebSocket targets** — a `ws://` or `wss://` `target.url` switches the built-in scenario to WebSocket mode: each iteration dials the endpoint, sends `target.message` (a text frame), reads one reply (`expectReply`, default true) so latency covers the round trip, and closes. `subprotocol` is optional and `target.timeout` bounds each exchange. WebSocket targets reject the HTTP-only fields (`method`/`body`/`checks`/`query`/`headers`) and are not supported with feeders or distributed mode — all reported at load time. A fresh connection per iteration is used because a WebSocket connection is single-threaded

- **YAML multipart uploads** — a `target.multipart` block (text `fields` + `files` with `field`/`path`/`filename`/`contentType`) builds a `multipart/form-data` body at load time and sends it with the matching Content-Type on every request, so file-upload endpoints can be load-tested from YAML with no Go code. Files are read relative to the config file's directory (capped at 100 MiB each). Composes with `checks`, `query`, and `timeout`; mutually exclusive with `body`; rejected with feeders or distributed mode (the binary body cannot be safely JSON-encoded for workers)

- **CLI error-category summary** — the text report now lists errors under `Errors (by frequency):` sorted by descending count with each category's share of total failures (e.g. `http_status_error: 50 (60.0%)`), making the dominant failure cause obvious at a glance. `--quiet` runs gain a single `Top error: <category> (NN% of failures)` line when any request failed, useful in CI logs. JSON output is unchanged

- **WebSocket transport** — `transport.NewWebSocketClient(WebSocketConfig{URL, Origin, Subprotocol, Header, TLSConfig})` dials a `ws://`/`wss://` endpoint (via `golang.org/x/net/websocket`); `SendText`/`SendBinary`/`Receive`/`Roundtrip` exchange messages, mirror the context deadline onto the connection, and record message bytes as Pulse throughput. `transport.CallWebSocket(fn)` adapts an interaction to the `(statusCode, error)` scenario shape (200 on success, 0 on error). A client carries one message stream and is not safe for concurrent use — dial per scenario iteration or pool per goroutine

- **Multipart uploads** — `transport.BuildMultipart(fields, files)` assembles a `multipart/form-data` body (text fields written in sorted order plus `MultipartFile` parts with per-file Content-Type) and returns it with the matching Content-Type header; `HTTPClient.DoMultipart(ctx, method, url, body, contentType)` sends it with that header per request (TTFB and byte metrics recorded as usual), for load-testing file-upload endpoints

- **TTFB & throughput in reporters and the live dashboard** — every reporter now exports time-to-first-byte percentiles and request/response byte counts alongside latency: Prometheus (`pulse_ttfb_ms{quantile=...}`, `pulse_bytes_in_total`, `pulse_bytes_out_total`), OpenTelemetry (`pulse.ttfb.p50/p99`, `pulse.bytes.in/out`), InfluxDB (`ttfb_p50_ms`, `ttfb_p99_ms`, `bytes_in`, `bytes_out`), Datadog (`pulse.ttfb.p50_ms/p99_ms`, `pulse.bytes.in/out`), and CSV (four columns appended after `passed`, preserving the existing stable order). The live SSE dashboard gains a **P99 TTFB** card and a **Throughput** card (in/out bytes/sec), and its snapshot/result DTOs carry a `ttfb` block plus `bytes_in`/`bytes_out`

- **`user_error` error category** — `pulse.UserError(err)` wraps a scenario-originated failure (bad fixtures, business-rule violations, client-side validation) so the run counts it under a new `user_error` category instead of `unknown_error`, separating test-side failures from target-side ones in `ErrorCounts`. `pulse.ErrUser` is the sentinel (detect with `errors.Is`); the marker is checked before the transport/status classifications, so it wins even when wrapping a network or status error. The wrapped error unwraps to both `ErrUser` and the original

- **YAML query parameters and per-request timeout** — the built-in HTTP target accepts a structured `query:` map whose entries are URL-encoded and appended to `url` in deterministic (sorted) order, without re-parsing the URL (so existing query strings and `{{feeder}}` placeholders are preserved). `target.timeout` is now applied as a per-request `context` deadline in addition to the client timeout, so each request is bounded independently and the deadline composes with run cancellation (a breach is counted under `deadline_exceeded`). Empty query keys are rejected at load

- **Data-driven runs / YAML feeders** — the built-in HTTP scenario can now be parameterized from a CSV or JSONL data file via a `feeder:` section, substituting `{{variable}}` placeholders in the target URL and body (e.g. `url: /users/{{id}}`). CSV rows are keyed by the header columns; JSONL lines are flat JSON objects. Iteration is `round-robin` (deterministic, default) or `random` (seeded for reproducibility, falling back to `config.seed`). The `{{...}}` delimiter avoids the `${...}` used by environment-variable interpolation. Every placeholder in the URL/body must exist in every row — a missing variable is reported at load time naming the variable and row; placeholders in headers are rejected. Feeders are local-only (rejected in distributed mode, since the data file is on the coordinator) and the data file is capped at 50 MiB

- **Stress / ramp-to-failure mode (capacity discovery)** — `pulse.Config.Stress` (`StressConfig`) or a `stress:` YAML section raises the arrival rate from the phase's starting rate by `stepRPS` every healthy reporting interval until a window's error rate (`maxErrorRate`) or P99 latency (`maxP99`) breaches its threshold, then stops and reports the sustained capacity. Reaching the failure point is the expected, successful outcome: `RunContext` returns no error and `Result.Stress` (`*StressResult`) holds `MaxHealthyRPS`, `FailedAtRPS`, and `Reason` (`error_rate`/`p99_latency`); `sustainedIntervals` requires N consecutive breached windows as a noise guard and `minRequests` skips tiny windows. The CLI prints a `Capacity (stress): …` line and JSON adds a `stress` block. Requires `Reporting.Interval > 0`, is mutually exclusive with `Adaptive`, and is rejected in distributed mode (`Workers`)

- **Time-to-first-byte (TTFB) metrics** — Pulse now measures TTFB per HTTP request via `httptrace` and aggregates it in its own HDR histogram, independent of total latency. Results expose `Result.TTFB` (min/mean/P50/P90/P95/P99/max), the CLI prints a `TTFB P50/P90/P99` line, and JSON output gains a `ttfb` block (and per-snapshot `ttfb`). TTFB is reported only when the transport measured it (HTTP scenarios); custom/gRPC scenarios leave it zero. Merged accurately across distributed workers via a parallel TTFB bucket transfer

- **Throughput / byte counters** — runs now track total response and request bytes (`Result.BytesIn` / `Result.BytesOut`). The CLI prints bytes in/out and throughput (bytes/sec); JSON `summary` gains `bytes_in`, `bytes_out`, `bytes_in_per_sec`, and `bytes_out_per_sec`. Byte counts are summed across distributed workers. The transport surfaces these (and TTFB) to the engine through a per-iteration `internal/reqmetrics.Sample` carried in the request context, keeping the `func(context.Context) (int, error)` scenario signature unchanged

- **YAML response checks** — the built-in HTTP scenario now supports declarative response assertions under `target.checks`: `status` (expected code), `headerEquals` (exact header match), `bodyContains` (required substrings), and `jsonEquals` (top-level JSON field equals a value). A failing check marks the request as failed under a new **`check_failed`** error category, so runs can be gated on check failures via `thresholds.errorRate`. When a `status` check is set it fully governs status evaluation (e.g. `status: 404` makes a 404 a success); without one, the default "status >= 400 fails" behavior is preserved. The same assertions are available programmatically via `transport.Checks{...}.Run(resp)`, which wraps the first failure with `transport.ErrCheckFailed`. Checks apply to both local and distributed runs: the coordinator forwards them (via `distributed.HTTPChecks` on the wire) to each CLI worker, which evaluates them with identical semantics

- **`check_failed` error category** — `ErrorCounts` now distinguishes responses that completed but failed a configured check from server errors (`http_status_error`) and transport failures; mapped from `transport.ErrCheckFailed` in `metrics/normalize.go`

- **Capacity-aware distributed split** — `pulse.Config.WorkerWeights []int` (or `workerWeights:` in YAML; `coordinator.Options.Weights`) splits arrival rate and concurrency across workers proportionally to their relative capacity (e.g. `{2,1}` sends a 2:1 share). When unset, workers are weighted equally

- **Configurable percentiles** — `pulse.Config.Percentiles []float64` (or a `percentiles:` YAML list, e.g. `[99.9, 99.99]`) computes additional latency percentiles for the final result alongside the always-reported P50/P90/P95/P99. Values appear in `Result.ExtraPercentiles` (keyed `"p99.9"`), in CLI text output, and in JSON under `extra_percentiles` (additive, JSON v1-compatible). Values must be in (0,100); the C++ histogram already supported arbitrary percentiles, this exposes them

- **Cookie jar / sessions** — `transport.HTTPClient.Session()` returns a client that shares the base client's pooled transport but has its own fresh in-memory cookie jar, so each virtual-user iteration gets an isolated session (a login cookie set in one iteration is resent on later requests in that iteration, but never leaks to other concurrent iterations). `transport.HTTPClientConfig.Jar` allows attaching a custom `http.CookieJar` to the base client

- **Threshold-abort / fail-fast** — `pulse.Config.Abort` (`AbortConfig{MaxErrorRate, MaxP99, MinRequests}`) stops a run early when a reporting interval breaches the configured error-rate or P99-latency limit; `RunContext` returns the partial result wrapped with `pulse.ErrAborted` (detect with `errors.Is`). Configurable in YAML via an `abort:` section. Requires `reporting.interval > 0`. The CLI prints the partial summary and a notice on abort

- **CSV exporter** — `reporter.NewCSVReporter(w io.Writer, cfg CSVConfig)` writes Pulse metrics as CSV with a stable column order (a header row, an optional row per snapshot when `CSVConfig.Snapshots` is true, and a final `kind=result` summary row). On the CLI, `--csv <file>` writes the run's snapshots and final result to a file (atomic, symlink-safe) for CI artifacts

- **Extended error taxonomy** — `ErrorCounts` now distinguishes network failures that previously fell into `unknown_error`: `timeout` (I/O timeouts not driven by the context) and `transport` (connection refused, DNS failures, and other `net.Error`s). Existing categories (`context_canceled`, `deadline_exceeded`, `http_status_error`, `unknown_error`) are unchanged; the map remains open-ended, consistent with the additive JSON v1 contract

- **Correlations / value extraction** — `pulse.Extractor[T]` is a generic, thread-safe container for passing values extracted in one scenario step to subsequent steps (e.g. an auth token from a login response); `transport.ExtractHeader(resp, key)`, `transport.ExtractJSONString(resp, field)`, and `transport.ExtractRegexp(resp, pattern)` extract values from a `*transport.Response` for storing in an Extractor

- **HAR import** — `har.LoadFile(path, cfg)` and `har.Load(r, cfg)` parse HTTP Archive files and return a `pulse.Scenario` that replays all recorded requests in sequence via `pulse.Flow`; hop-by-hop headers are stripped automatically; `Config.Filter` skips entries (e.g. static assets); `Config.Client` accepts a custom `*http.Client`; errors include the step name (`"GET https://api/users: HTTP 404"`) for easy diagnosis

- **gRPC support** — `transport.NewGRPCClient(cfg GRPCClientConfig)` dials a gRPC server (insecure, system TLS, or custom TLS) and exposes `Conn() *grpc.ClientConn` for passing to generated client constructors; `transport.CallGRPC(fn func() error) (int, error)` wraps a gRPC call and maps the gRPC status code to an HTTP-equivalent integer so Pulse thresholds and error metrics work consistently across transports; all 17 gRPC codes are mapped

- **Scenario chaining** — `pulse.Sequence(steps ...Scenario) Scenario` runs steps in order and stops on the first error, returning that step's status code; `pulse.Flow(steps ...Step) Scenario` does the same but wraps errors with the step name (`"login: unauthorized"`) for easier identification in result error maps; `pulse.Step{Name, Do}` pairs a name with a scenario function

- **OpenTelemetry reporter** — `reporter.NewOTelReporter(provider metric.MeterProvider)` exports Pulse metrics as OTEL gauges (`pulse.rps`, `pulse.error_rate`, `pulse.latency.p50/p90/p95/p99`, `pulse.requests.total/failed`); the caller owns the provider and its exporter (OTLP, stdout, etc.), keeping transport decisions outside the reporter

- **Env var interpolation in YAML** — `config.Load` now expands `${VAR}` and `${VAR:-default}` placeholders in YAML files before unmarshalling; unset required variables return a descriptive error naming the missing variable; useful for secrets (`${API_TOKEN}`), environment-specific URLs (`${BASE_URL}`), and CI overrides

- **Data injection / Feeder** — `pulse.NewFeeder[T](items []T)` returns a generic, thread-safe feeder that supplies values to concurrent scenario invocations round-robin; `pulse.NewFeederFunc[T](fn func() T)` supports generated or random values; both expose a single `Next() T` method with no allocations in the hot path
- **Response assertions** — `transport.Response` type returned by the new `HTTPClient.DoWithResponse(ctx, method, url, body)` method; unlike `Do`, status >= 400 does not produce an error, giving callers full control via assertion helpers: `AssertStatus(resp, expected)`, `AssertBodyContains(resp, substr)`, `AssertBodyJSON(resp, &v)`, `AssertHeader(resp, key, expected)`; the body is pre-read into memory (up to `MaxResponseBytes`) so helpers can inspect it without draining

### Changed

- **Distributed rate split & failure reporting** — the coordinator now divides arrival rate and concurrency using the largest-remainder method, so any integer remainder is spread fairly across workers instead of being dumped on the first worker. When workers fail, `Run` now joins **every** worker error (prefixed with an `N of M workers failed` summary) instead of returning only the first, while still merging the successful workers' results

- **CI lint** — CI now runs `golangci-lint` (pinned `@v2.6.0`; config in `.golangci.yml` covering errcheck/govet/ineffassign/staticcheck/unused) and `go test -race`. Pre-existing lint findings were fixed, including a dead overflow guard in `metrics.nsToDuration` and a stale unused type in the dashboard server

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
