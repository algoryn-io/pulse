# Pulse: High-Precision Performance Testing for Algoryn Fabric

[![CI](https://github.com/algoryn-io/pulse/actions/workflows/ci.yml/badge.svg)](https://github.com/algoryn-io/pulse/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/algoryn-io/pulse)](https://go.dev/doc/install)
[![Latest Release](https://img.shields.io/github/v/release/algoryn-io/pulse)](https://github.com/algoryn-io/pulse/releases)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/algoryn.io/pulse.svg)](https://pkg.go.dev/algoryn.io/pulse)

**Pulse** is a load and reliability testing tool built for the Algoryn Fabric stack. It drives HTTP workloads with predictable arrival rates, reports latency and error metrics you can trust in CI, and enforces SLOs through configurable pass/fail thresholds. The runtime combines **Go** for high-throughput orchestration and **C++** for **constant-memory, high-dynamic-range (HDR) latency histograms**, so performance metrics stay accurate and lightweight even on long, heavy runs.

---

## Architecture highlights

### Hybrid engine

- **Go** — Schedules load across **constant, ramp, step, and spike** phases, bounds concurrency with a limiter, and runs scenarios at the target requests-per-second. When concurrency is exhausted, the default `drop` saturation policy preserves the planned arrival rate and records discarded arrivals instead of silently slowing the scheduler. Use `block` only when backpressure is intentional.
- **C++ (via cgo)** — A native **HDR-style logarithmic histogram** records every latency sample in **O(1)** time with a **fixed bucket count**. The engine covers roughly **1 microsecond to 60 seconds** on a single run with **sub-microsecond** resolution in the low-latency regime and a **stable, constant memory footprint** (no unbounded per-sample storage in the hot path). Percentiles (**P50, P90, P95, P99**) are derived from that histogram, then combined in Go with exact **min, max, and mean** tracking.

This split keeps the scheduler and network I/O in idiomatic, concurrent Go while entrusting percentile math to a compact, allocation-friendly path suited to long campaigns and high sample counts.

### Fabric integration

Pulse depends on [`algoryn.io/fabric`](https://github.com/algoryn-io/fabric) **v0.2.0+** for shared contracts, including **Protocol Buffer** messages under `algoryn.io/fabric/gen/go/fabric/v1`.

- **`ToRunEvent`** maps a `pulse.Result` into the legacy Go types `metrics.RunEvent` / `metrics.MetricSnapshot` (`algoryn.io/fabric/metrics`).
- **`ToRunEventProto`** returns the same data as **`fabric.v1.RunEvent`** (with **`fabric.v1.MetricSnapshot`** inside), using Fabric’s **`RunEventToProto`** helper so timestamps are **`google.protobuf.Timestamp`**.
- **`ToFabricRunEmit`** returns a matched pair: the full **`RunEvent`** protobuf and a **`fabric.v1.Event`** with **`EVENT_TYPE_RUN_COMPLETED`** (payload built with **`RunCompletedPayloadToProto`**), sharing one **`run_id`** so tools like **Beacon** can correlate summary events with detailed snapshots. The envelope timestamp uses **`timestamppb.Now()`**.
- Set **`Config.Service`** (optional) to populate **`MetricSnapshot.service`** and the run-completed payload; use **`Config.OnFabricEmit`** to receive both protobuf messages after each run (after threshold evaluation, same ordering as **`OnResult`**).

The CLI still prints human JSON for operators; wire **`OnFabricEmit`** (or call **`ToRunEventProto`**) when you need **proto/binary** or **protojson** on the wire.

Load-fidelity fields (`scheduled`, `started`, `dropped`, `dropped_rate`, `completed`, and `max_active`) are currently available through `pulse.Result`, `OnResult`, and CLI JSON. They are not emitted in Fabric protobuf messages until the shared Fabric schema adds corresponding fields.

---

## Key features

| Area | What you get |
|------|----------------|
| **Load model** | **Multi-phase** tests: **Constant**, **Ramp**, **Step**, and **Spike** — arrival-rate (RPS) driven, with explicit `drop` or `block` saturation behavior. |
| **Latency** | **P50, P90, P95, P99** (plus min, mean, max) from the **C++ histogram**; stable under load, bounded memory. |
| **Configuration** | Strict **YAML** test definitions with **env var interpolation** (`${VAR}` / `${VAR:-default}`). Supports target, phases, `maxConcurrency`, saturation policy, and optional **thresholds** (error rate, dropped-arrival rate, mean / P95 / P99 latency). |
| **Output** | **Text** (human-readable) and **JSON** (automation, CI artifacts); optional interval snapshots expose transient behavior in JSON. Combine `--json` and `--out` to mirror JSON to a file. |
| **Live dashboard** | Stream metrics to a browser via SSE with `--dashboard :9090`. Displays live RPS, latency percentile charts, and error rate as the run progresses. Shuts down automatically when the run completes. |
| **Adaptive load shaping** | Auto-tune RPS in real time based on observed error rate and P99 latency. Set `Config.Adaptive` to define thresholds; the engine steps the arrival rate down when limits are exceeded and recovers when conditions improve. Requires `Reporting.Interval > 0`. |
| **Chaos injection** | Inject synthetic faults at the transport layer without touching scenario code. `transport.NewChaosRoundTripper` wraps any `http.RoundTripper` and applies configurable error injection (`ErrorRate`) and latency injection (`LatencyRate` + `Latency`) per request. |
| **Data injection** | `pulse.NewFeeder[T](items)` supplies parameterized values (user IDs, payloads, tokens) to concurrent scenario invocations round-robin. `pulse.NewFeederFunc[T](fn)` supports generated or random data. Both are generic and allocation-free in the hot path. |
| **Response assertions** | `transport.HTTPClient.DoWithResponse` returns a `*transport.Response` (status, headers, pre-read body). Use `AssertStatus`, `AssertBodyContains`, `AssertBodyJSON`, and `AssertHeader` to validate responses inside scenarios. |
| **Plugin reporters** | Export metrics to external systems by implementing `pulse.Reporter` (`OnSnapshot` + `OnResult`). Built-in reporters: `reporter.NewPrometheusReporter` (Prometheus `/metrics`), `reporter.NewInfluxDBReporter` (InfluxDB v2 line protocol), `reporter.NewDatadogReporter` (DogStatsD UDP). Wire them via `Config.Reporters`. |
| **API** | Use **`pulse.Run`** or cancelable **`pulse.RunContext`**, `OnResult` hooks, `OnSnapshot` for per-interval callbacks, optional **`OnFabricEmit`** for **Fabric protobuf** (`RunEvent` + `RunCompleted` event), and **middleware** for chaos-style scenarios; **`RunT`** for `go test` integration. |
| **Tooling** | Optional **`mockserver`** for local demos; see [`examples/`](examples/). |

---

## Installation

### Requirements

- **Go** — Version compatible with [`go.mod`](go.mod) (see badge above).
- **C/C++ toolchain for cgo** — Pulse links a small C++ stats library. You need a working C++17 compiler the Go toolchain can invoke:
  - **macOS**: Xcode Command Line Tools (`clang++`) are typically enough.
  - **Linux**: **GCC** or **Clang** with `g++` / `clang++` and `libstdc++` or `libc++` as appropriate for your distribution.

Set `CC` and `CXX` if you use a non-default compiler:

```sh
export CC=clang
export CXX=clang++
```

### Get Pulse

**From the module** (for library use):

```sh
go get algoryn.io/pulse@latest
```

**From a clone of this repository:**

```sh
go install ./cmd/pulse
go install ./cmd/mockserver   # optional, for local testing
```

Ensure your `GOBIN` (or `GOPATH/bin`) is on `PATH` so the `pulse` binary is found.

---

## Usage

### Run a test

```sh
pulse run path/to/config.yaml
```

**Useful flags**

| Flag | Description |
|------|-------------|
| `--dry-run` | Validate config and print a phase summary without sending any traffic. Safe for pre-flight checks and PR pipelines. |
| `--json` | Print results as **JSON** on stdout. |
| `--out <file>` | Write the same JSON object to a file (can be combined with `--json`). |
| `--junit <file>` | Write a **JUnit XML** report for CI (thresholds become individual test cases). |

**Exit codes** — `0` success; `2` run finished but **thresholds failed**; `1` for usage, config, I/O, or other failures (including mixed error types).

### Sample YAML

```yaml
# Steady load with latency SLOs (example: local mock server on :8080)
phases:
  - type: constant
    duration: 5s
    arrivalRate: 20

target:
  method: GET
  url: http://localhost:8080
  timeout: 10s          # per-request timeout (default: 30s)
  maxIdleConns: 100     # connection pool size across all hosts
  maxIdleConnsPerHost: 20

maxConcurrency: 4
saturationPolicy: drop

reporting:
  interval: 1s

thresholds:
  maxDroppedRate: 0.05
  maxMeanLatency: 100ms
  maxP95Latency: 150ms
  maxP99Latency: 200ms
```

Run against a live target or, for a quick check, start the bundled mock in another terminal and point the URL at it. More examples live under [`examples/`](examples/).

### Live dashboard

Add `--dashboard :9090` to any `pulse run` command to open a streaming metrics dashboard in your browser while the test runs:

```sh
pulse run config.yaml --dashboard :9090
# Dashboard: http://localhost:9090
```

The dashboard streams per-interval data via SSE and shows:
- Throughput (RPS) over time
- Latency percentiles (P50 / P95 / P99) over time
- Error and drop rate over time
- Current latency breakdown (min / P50 / P90 / P95 / P99 / max)

The page reconnects automatically if the connection drops and shows a "Run complete" banner when the test finishes. The dashboard also works from the Go API via `Config.DashboardAddr` and `Config.OnSnapshot` (for custom per-interval callbacks).

### Adaptive load shaping

Enable real-time RPS auto-tuning for `constant` phases by setting `Config.Adaptive`. The engine observes each reporting interval and steps the arrival rate **down** when either threshold is exceeded, then gradually **steps up** when conditions recover.

```go
pulse.Run(pulse.Test{
    Config: pulse.Config{
        Reporting: pulse.ReportingConfig{Interval: 500 * time.Millisecond},
        Adaptive: pulse.AdaptiveConfig{
            MaxErrorRate: 0.05,            // step down above 5 % errors
            MaxP99:       200 * time.Millisecond,
            MinRPS:       10,
            MaxRPS:       500,
            StepDown:     0.9,             // multiply rate by 0.9 on violation
            StepUp:       1.05,            // multiply rate by 1.05 on recovery
        },
        // ...
    },
})
```

`Adaptive` requires `Reporting.Interval > 0`. It only applies to `PhaseTypeConstant` phases; ramp, step, and spike phases run at their scheduled rates.

### Chaos / fault injection

Wrap any `http.RoundTripper` with `transport.NewChaosRoundTripper` to inject synthetic faults without modifying scenario code:

```go
chaos := transport.NewChaosRoundTripper(nil, transport.ChaosConfig{
    ErrorRate:   0.05,                    // 5 % of requests return ErrChaosInjected
    LatencyRate: 0.10,                    // 10 % of requests receive extra latency
    Latency:     100 * time.Millisecond,
})

client := transport.NewHTTPClientWith(transport.HTTPClientConfig{Transport: chaos})
```

Error injection short-circuits before the request is forwarded. Latency injection adds a sleep before forwarding and respects context cancellation. Use `errors.Is(err, transport.ErrChaosInjected)` to identify injected failures in results.

### Plugin reporters

Implement `pulse.Reporter` to stream metrics to any external system:

```go
type Reporter interface {
    OnSnapshot(Snapshot)           // called at each reporting interval
    OnResult(Result, passed bool)  // called once after the run completes
}
```

Wire one or more reporters via `Config.Reporters`:

```go
ctx := context.Background()
pulse.Run(pulse.Test{
    Config: pulse.Config{
        Reporters: []pulse.Reporter{
            reporter.NewPrometheusReporter(ctx, ":2112"),
            reporter.NewInfluxDBReporter(reporter.InfluxDBConfig{
                URL:    "http://localhost:8086",
                Token:  os.Getenv("INFLUX_TOKEN"),
                Org:    "myorg",
                Bucket: "pulse",
            }),
            reporter.NewDatadogReporter(reporter.DatadogConfig{
                Addr:      "localhost:8125",
                Namespace: "myapp",
                Tags:      []string{"env:prod"},
            }),
        },
        // ...
    },
})
```

| Reporter | Protocol | Package |
|----------|----------|---------|
| `NewPrometheusReporter` | HTTP `/metrics` — Prometheus text exposition | `algoryn.io/pulse/reporter` |
| `NewInfluxDBReporter` | HTTP — InfluxDB v2 line protocol (`/api/v2/write`) | `algoryn.io/pulse/reporter` |
| `NewDatadogReporter` | UDP — DogStatsD datagrams | `algoryn.io/pulse/reporter` |

### Environment variable interpolation

Any value in a YAML config file can reference an environment variable using `${VAR}` or `${VAR:-default}`:

```yaml
target:
  url: ${BASE_URL:-http://localhost:8080}
  method: GET
  headers:
    Authorization: Bearer ${API_TOKEN}

phases:
  - type: constant
    duration: ${DURATION:-30s}
    arrivalRate: ${RPS:-50}
```

- `${VAR}` — required; `config.Load` returns an error naming the missing variable if it is not set.
- `${VAR:-default}` — optional; the literal after `:-` is used when the variable is unset.

This keeps secrets out of YAML files and lets CI/CD pipelines override targets without editing config files.

### Data injection

Use `pulse.NewFeeder` to supply different values to each scenario invocation without managing concurrency yourself:

```go
type User struct {
    ID    int
    Token string
}

users := pulse.NewFeeder([]User{
    {ID: 1, Token: "tok-a"},
    {ID: 2, Token: "tok-b"},
    {ID: 3, Token: "tok-c"},
})

client := transport.NewHTTPClient()

scenario := func(ctx context.Context) (int, error) {
    u := users.Next()
    return client.Get(ctx, fmt.Sprintf("http://api/users/%d", u.ID))
}
```

`NewFeeder` cycles through the slice round-robin and is safe for concurrent use. For generated or random data, use `NewFeederFunc`:

```go
ids := pulse.NewFeederFunc(func() int {
    return rand.Intn(10_000) + 1
})
```

### Response assertions

Use `DoWithResponse` when you need to validate the response body or headers inside a scenario:

```go
scenario := func(ctx context.Context) (int, error) {
    resp, err := client.DoWithResponse(ctx, "GET", "http://api/orders/1", nil)
    if err != nil {
        return 0, err
    }
    if err := transport.AssertStatus(resp, 200); err != nil {
        return resp.StatusCode, err
    }
    if err := transport.AssertHeader(resp, "Content-Type", "application/json"); err != nil {
        return resp.StatusCode, err
    }
    var order struct{ Status string }
    if err := transport.AssertBodyJSON(resp, &order); err != nil {
        return resp.StatusCode, err
    }
    return resp.StatusCode, transport.AssertBodyContains(resp, `"status":"confirmed"`)
}
```

Unlike `Do`, `DoWithResponse` does not error on status >= 400, giving you full control over what counts as a failure.

**Mockserver modes**

| Flag | Description |
|------|-------------|
| `--mode healthy` | Always 200 OK (default). |
| `--mode mixed-errors` | Alternates 200 / 500 on successive requests. |
| `--mode slow` | Delays every response by `--slow-delay` (default `120ms`); respects context cancellation. |
| `--mode flaky` | Returns 500 for `--flaky-rate` fraction of requests (default `0.3`). |
| `--mode down` | Always 503 Service Unavailable. |

```sh
go run ./cmd/mockserver --mode slow --slow-delay 500ms
go run ./cmd/mockserver --mode flaky --flaky-rate 0.4
```

### JSON result shape (summary)

Pulse’s JSON output is a **stable contract** for CI tooling. The top-level object includes `schema_version` (currently `1`), plus `summary` (totals, RPS, `duration_ms`, scheduled / started / dropped / completed requests, dropped rate, and maximum active requests), `latency` with **`min_ms`**, **`p50_ms`**, **`mean_ms`**, **`p90_ms`**, **`p95_ms`**, **`p99_ms`**, **`max_ms`**, `status_codes`, `errors`, per-threshold rows, optional interval `snapshots`, and `passed`.

**Compatibility**: within `schema_version: 1`, changes are additive only. Breaking changes require a new schema version.

Set `reporting.interval` to enable temporal snapshots. Enabled intervals must be at least `10ms`, and a run may generate at most `10,000` snapshots. Windows are aligned to the run start. Scheduled arrivals, started requests, and dropped arrivals belong to the interval where they are handled. Completed requests, failures, status codes, errors, and latency belong to the interval where execution finishes. The text report remains a concise global summary; snapshots are emitted in JSON for automation and visualization.

`maxConcurrency: 0` retains the library zero-value behavior and runs with one execution slot. Set it explicitly for load campaigns. Values above `1,000,000`, unknown YAML fields, negative concurrency, invalid saturation policies, out-of-range spike windows, invalid URLs, and invalid threshold rates are rejected before execution.

The built-in HTTP transport applies a `30s` request timeout and drains at most `1MiB` from each response body by default. YAML configuration files are limited to `1MiB`. Override HTTP limits with `transport.HTTPClientConfig` when a campaign requires different values. Treat YAML files as trusted input: targets intentionally support arbitrary HTTP and HTTPS URLs, including private network addresses.

---

## Ecosystem

Pulse is part of the **Algoryn Fabric** ecosystem: shared contracts under `algoryn.io/fabric` help **Relay**, **Beacon**, and other tools present and consume performance and reliability data in a **consistent** way. Pulse focuses on *generating* evidence under load; Fabric types carry that evidence to the rest of the stack.

---

## License

MIT
