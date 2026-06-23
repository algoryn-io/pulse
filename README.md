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
| **Fail-fast / abort** | Stop a run early when a reporting interval breaches an error-rate or P99 limit. Set `Config.Abort` (or an `abort:` YAML section); `RunContext` returns the partial result wrapped with `pulse.ErrAborted`. Requires `Reporting.Interval > 0`. |
| **Sessions / cookies** | `transport.HTTPClient.Session()` gives each virtual-user iteration an isolated cookie jar over a shared connection pool — login → cookie → authenticated requests work without leaking sessions between concurrent iterations. |
| **Chaos injection** | Inject synthetic faults at the transport layer without touching scenario code. `transport.NewChaosRoundTripper` wraps any `http.RoundTripper` and applies configurable error injection (`ErrorRate`) and latency injection (`LatencyRate` + `Latency`) per request. |
| **Correlations** | `pulse.Extractor[T]` passes values extracted in one step (e.g. auth token from login) to subsequent steps. `transport.ExtractHeader`, `ExtractJSONString`, and `ExtractRegexp` pull values from a `*Response`. |
| **HAR import** | `har.LoadFile(path, cfg)` converts an HTTP Archive file into a `pulse.Scenario` that replays all recorded requests in sequence. Filter entries, supply a custom client, and use recorded Auth headers as-is. |
| **gRPC support** | `transport.NewGRPCClient` dials a gRPC server (insecure / TLS). `transport.CallGRPC(fn)` wraps a gRPC call and maps gRPC status codes to HTTP-equivalent integers so Pulse thresholds work across both transports. |
| **Scenario chaining** | `pulse.Sequence(steps...)` and `pulse.Flow(steps...)` compose multiple scenario functions into a single user journey. `Flow` wraps errors with the step name for easy identification. |
| **Data injection** | `pulse.NewFeeder[T](items)` supplies parameterized values (user IDs, payloads, tokens) to concurrent scenario invocations round-robin. `pulse.NewFeederFunc[T](fn)` supports generated or random data. Both are generic and allocation-free in the hot path. |
| **Response assertions** | `transport.HTTPClient.DoWithResponse` returns a `*transport.Response` (status, headers, pre-read body). Use `AssertStatus`, `AssertBodyContains`, `AssertBodyJSON`, and `AssertHeader` to validate responses inside scenarios. |
| **Plugin reporters** | Export metrics to external systems by implementing `pulse.Reporter` (`OnSnapshot` + `OnResult`). Built-in reporters: `reporter.NewPrometheusReporter` (Prometheus `/metrics`), `reporter.NewInfluxDBReporter` (InfluxDB v2 line protocol), `reporter.NewDatadogReporter` (DogStatsD UDP), `reporter.NewOTelReporter` (OpenTelemetry via any `metric.MeterProvider`), `reporter.NewCSVReporter` (CSV, also exposed as `--csv`). Wire them via `Config.Reporters`. |
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
| `--format text\|json` | Output format. `text` (default) is human-readable; `json` is for automation/CI. `--json` is a shorthand for `--format json`. |
| `--quiet` | Print only the final summary (no progress). Cannot be combined with `--format json`. |
| `--dry-run` | Validate config and print a phase summary without sending any traffic. Safe for pre-flight checks and PR pipelines. |
| `--seed <n>` | Seed all built-in randomness (jitter, error injection, etc.) for reproducible runs. |
| `--out <file>` | Write the JSON result object to a file (atomic, symlink-safe; can be combined with `--json`). |
| `--junit <file>` | Write a **JUnit XML** report for CI (thresholds become individual test cases). |
| `--csv <file>` | Write a **CSV** report (header + one row per snapshot + a final summary row) for spreadsheets/CI artifacts. |
| `--dashboard :port` | Start a live SSE metrics dashboard for the duration of the run (see [Live dashboard](#live-dashboard)). |
| `--workers host:port,...` | Run distributed across the listed workers (see [Distributed mode](#distributed-mode)). |

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

### Distributed mode

For load beyond a single machine, run Pulse as a **coordinator + workers**. Each worker is a small HTTP server that executes a share of the arrival rate locally and returns full histogram buckets, so the coordinator can merge **accurate** percentiles (not an average of averages).

Start one or more workers (each on its own host):

```sh
# On each worker host:
export PULSE_WORKER_TOKEN=$(openssl rand -hex 32)   # shared secret (see Security)
pulse worker --addr :9100
```

Then run the coordinator, pointing at the workers with `--workers`:

```sh
export PULSE_WORKER_TOKEN=...   # same token as the workers
pulse run config.yaml --workers 10.0.0.1:9100,10.0.0.2:9100
```

The coordinator splits each phase's arrival rate and `maxConcurrency` evenly across workers (the first worker absorbs any integer remainder), pings all workers before starting, fans out the run, and merges the results. Thresholds and JSON/JUnit output behave exactly as in single-node mode. From the Go API, set `Config.Workers` (coordinator) and serve with `worker.NewWithOptions(...)`.

#### Security

Workers accept arbitrary target URLs in each run request, so an **exposed, unauthenticated worker is a remote SSRF / arbitrary-load primitive**. Protect them:

| Variable | Where | Effect |
|----------|-------|--------|
| `PULSE_WORKER_TOKEN` | worker **and** coordinator | Shared bearer token. The worker requires `Authorization: Bearer <token>` on every request (constant-time compare) and returns `401` on mismatch. The coordinator sends it automatically. **Set this whenever a worker is reachable by anything other than localhost.** |
| `PULSE_WORKER_DENY_PRIVATE` | worker | When truthy (`1`/`true`/`yes`/`on`), worker-built HTTP scenarios are rejected if they target private, loopback, link-local, or cloud-metadata addresses (validated at dial time). Leave unset when intentionally load-testing internal services. |

The token is read from the environment (never from YAML or CLI flags) so it does not leak into version control or process listings. If `PULSE_WORKER_TOKEN` is unset the worker still runs but prints a warning — bind it to a private interface in that case.

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

### Fail-fast / abort

Stop a run early when live metrics breach a limit — useful in CI to fail quickly instead of running a doomed test to completion. Set `Config.Abort` (Go) or an `abort:` section (YAML); the run is cancelled and the error is wrapped with `pulse.ErrAborted`.

```yaml
reporting:
  interval: 500ms
abort:
  maxErrorRate: 0.25     # abort if a window exceeds 25 % errors
  maxP99: 750ms          # ...or if window P99 exceeds 750ms
  minRequests: 50        # only evaluate windows with >= 50 completed requests
```

```go
_, err := pulse.RunContext(ctx, test)
if errors.Is(err, pulse.ErrAborted) {
    // the SLO was breached mid-run; the returned Result holds partial metrics
}
```

`Abort` requires `Reporting.Interval > 0`. `MinRequests` guards against aborting on a tiny, noisy first window. On the CLI, an aborted run prints its partial summary plus a notice and exits non-zero.

### Sessions / cookies

By default an `HTTPClient` has no cookie jar, so requests are stateless. For session-based flows (login → cookie → authenticated requests), call `Session()` at the start of a scenario: it reuses the base client's connection pool but gets a **fresh** cookie jar, keeping each virtual user's session isolated from other concurrent iterations.

```go
base := transport.NewHTTPClientWith(transport.HTTPClientConfig{})

scenario := func(ctx context.Context) (int, error) {
    s := base.Session() // shared transport, private cookie jar
    if _, err := s.Do(ctx, http.MethodPost, loginURL, creds); err != nil {
        return 0, err
    }
    return s.Do(ctx, http.MethodGet, profileURL, nil) // sends the login cookie
}
```

Pass a custom jar to the base client with `HTTPClientConfig.Jar` when you want a single shared session instead.

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
| `NewOTelReporter` | OpenTelemetry gauges via any `metric.MeterProvider` | `algoryn.io/pulse/reporter` |
| `NewCSVReporter` | CSV rows to any `io.Writer` (also `--csv <file>`) | `algoryn.io/pulse/reporter` |

### Correlations

Use `pulse.Extractor[T]` to pass a value extracted in one step to subsequent steps. Combine it with `DoWithResponse` and the extraction helpers in `transport`:

```go
var token pulse.Extractor[string]

scenario := pulse.Flow(
    pulse.Step{Name: "login", Do: func(ctx context.Context) (int, error) {
        resp, err := client.DoWithResponse(ctx, "POST", "http://api/auth/login", body)
        if err != nil { return 0, err }
        if err := transport.AssertStatus(resp, 200); err != nil { return resp.StatusCode, err }
        tok, err := transport.ExtractJSONString(resp, "token")
        if err != nil { return resp.StatusCode, err }
        token.Set(tok)
        return resp.StatusCode, nil
    }},
    pulse.Step{Name: "get-orders", Do: func(ctx context.Context) (int, error) {
        req, _ := http.NewRequestWithContext(ctx, "GET", "http://api/orders", nil)
        req.Header.Set("Authorization", "Bearer "+token.MustGet())
        // ...
        return client.Do(ctx, "GET", "http://api/orders", nil)
    }},
)
```

Three extraction helpers work directly on `*transport.Response`:

| Helper | Extracts |
|--------|----------|
| `transport.ExtractHeader(resp, key)` | Response header value |
| `transport.ExtractJSONString(resp, field)` | Top-level JSON string field |
| `transport.ExtractRegexp(resp, pattern)` | First capture group of a regex |

### HAR import

Export a session from your browser's DevTools (Network tab → Save as HAR) and turn it into a load test in two lines:

```go
scenario, err := har.LoadFile("session.har", har.Config{
    // skip static assets and third-party requests
    Filter: func(req har.Request) bool {
        return strings.HasPrefix(req.URL, "https://api.myapp.com")
    },
})
if err != nil { ... }

pulse.Run(pulse.Test{Scenario: scenario, Config: cfg})
```

Recorded `Authorization` headers are forwarded as-is. Hop-by-hop headers (`Host`, `Connection`, `Content-Length`, etc.) are stripped automatically. Each request becomes a named `pulse.Flow` step so failures appear as `"POST https://api/checkout: HTTP 500"` in the result error map.

> **Warning:** HAR files contain session tokens and credentials. Do not commit them to version control.

### gRPC support

Use `transport.NewGRPCClient` to dial a gRPC server, then pass `Conn()` to any generated client constructor. Wrap calls with `transport.CallGRPC` to get a Pulse-compatible `(statusCode, error)` pair where the code is the HTTP equivalent of the gRPC status:

```go
client, err := transport.NewGRPCClient(transport.GRPCClientConfig{
    Target:   "localhost:50051",
    Insecure: true,
})
if err != nil { ... }
defer client.Close()

svc := pb.NewUserServiceClient(client.Conn())

scenario := func(ctx context.Context) (int, error) {
    return transport.CallGRPC(func() error {
        _, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: 42})
        return err
    })
}
```

gRPC status codes map to HTTP-equivalent integers (`NOT_FOUND` → 404, `UNAUTHENTICATED` → 401, `RESOURCE_EXHAUSTED` → 429, etc.) so existing Pulse thresholds and error metrics work without changes.

### Scenario chaining

`Sequence` composes independent steps into a single scenario. `Flow` does the same but names each step so failures are identifiable in the result error map.

```go
login := func(ctx context.Context) (int, error) {
    return client.Post(ctx, "http://api/login", body)
}
fetchProfile := func(ctx context.Context) (int, error) {
    return client.Get(ctx, "http://api/profile")
}
checkout := func(ctx context.Context) (int, error) {
    return client.Post(ctx, "http://api/checkout", cartBody)
}

// Unnamed — stops on first error, returns its status code
scenario := pulse.Sequence(login, fetchProfile, checkout)

// Named — same behaviour, errors include the step name
scenario = pulse.Flow(
    pulse.Step{Name: "login",    Do: login},
    pulse.Step{Name: "profile",  Do: fetchProfile},
    pulse.Step{Name: "checkout", Do: checkout},
)
```

Both functions integrate with `DoWithResponse` and `AssertStatus` — return an error from any step to halt the chain.

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

The `errors` map groups failures by category: `http_status_error` (status ≥ 400), `deadline_exceeded` (context deadline), `context_canceled` (run cancelled), `timeout` (network I/O timeout), `transport` (connection refused, DNS failures, other `net.Error`s), and `unknown_error` (everything else). The set is **open-ended** — new categories may be added additively, so consumers should not assume a fixed list of keys.

Set `reporting.interval` to enable temporal snapshots. Enabled intervals must be at least `10ms`, and a run may generate at most `10,000` snapshots. Windows are aligned to the run start. Scheduled arrivals, started requests, and dropped arrivals belong to the interval where they are handled. Completed requests, failures, status codes, errors, and latency belong to the interval where execution finishes. The text report remains a concise global summary; snapshots are emitted in JSON for automation and visualization.

`maxConcurrency: 0` retains the library zero-value behavior and runs with one execution slot. Set it explicitly for load campaigns. Values above `1,000,000`, unknown YAML fields, negative concurrency, invalid saturation policies, out-of-range spike windows, invalid URLs, and invalid threshold rates are rejected before execution.

The built-in HTTP transport applies a `30s` request timeout and drains at most `1MiB` from each response body by default. YAML configuration files are limited to `1MiB`. Override HTTP limits with `transport.HTTPClientConfig` when a campaign requires different values. Treat YAML files as trusted input: targets intentionally support arbitrary HTTP and HTTPS URLs, including private network addresses.

---

## Ecosystem

Pulse is part of the **Algoryn Fabric** ecosystem: shared contracts under `algoryn.io/fabric` help **Relay**, **Beacon**, and other tools present and consume performance and reliability data in a **consistent** way. Pulse focuses on *generating* evidence under load; Fabric types carry that evidence to the rest of the stack.

---

## License

MIT
