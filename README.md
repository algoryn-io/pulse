# Pulse

**Pulse** is a programmable reliability and load testing engine written in Go.

[![CI](https://github.com/algoryn-io/pulse/actions/workflows/ci.yml/badge.svg)](https://github.com/algoryn-io/pulse/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/algoryn-io/pulse)](https://go.dev/doc/install)
[![Latest Release](https://img.shields.io/github/v/release/algoryn-io/pulse)](https://github.com/algoryn-io/pulse/releases)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/algoryn.io/pulse.svg)](https://pkg.go.dev/algoryn.io/pulse)

Lightweight, deterministic, and designed for real-world automation.

It generates controlled HTTP load against a target, collects latency and error metrics, and evaluates configurable pass/fail thresholds. Tests are driven by a YAML config file and executed through the `pulse` CLI.

## Quick Start

From the repository root, with a **Go** toolchain matching [`go.mod`](go.mod):

**1. Start the mock HTTP server** (listens on `:8080` by default):

```sh
go run ./cmd/mockserver -mode healthy
```

**2. In another terminal, run a load test** against the examples (they target `http://localhost:8080`):

```sh
go run ./cmd/pulse run examples/baseline.yaml
```

**3. Print results as JSON** on stdout:

```sh
go run ./cmd/pulse run examples/baseline.yaml --json
```

After installing the binaries:

```sh
go install ./cmd/pulse
go install ./cmd/mockserver
```

You can run `pulse` and `mockserver` from your `PATH` instead of using `go run`.

**Use as a library** in your Go project:
```sh
go get algoryn.io/pulse@latest
```

**Expected results** (with the mock server in the suggested mode from [Examples](#examples)):

- [`baseline.yaml`](examples/baseline.yaml) → **PASS**
- [`mixed-errors.yaml`](examples/mixed-errors.yaml) → **FAIL** (thresholds)
- [`timeout.yaml`](examples/timeout.yaml) → **FAIL** (thresholds)

---

## Features

- **Arrival-rate scheduling** — request-driven load (requests/sec), with constant, ramp, step, and spike phases (not user/VU-based)
- **Bounded concurrency** — configurable goroutine limit prevents runaway resource usage
- **Metrics aggregation** — total, failed, RPS, latency (min, mean, p50, p95, p99, max), status code distribution, normalized error categories
- **Thresholds** — `error_rate`, `mean_latency`, `p95_latency`, `p99_latency` with PASS / FAIL in the text report
- **HTTP transport** — GET, POST, PUT, DELETE, PATCH; optional `headers`, `body`, and `timeout` in YAML
- **CLI** — `pulse run <config.yaml>` with human-readable text and JSON output modes
- **Result hook** — optional `OnResult` callback in `Config` for post-run integrations (CI systems, observability pipelines)
- **Middleware pipeline** — composable `Middleware` type with `Chain` and `Apply` helpers
- **Chaos engineering** — `WithLatency`, `WithErrorRate`, `WithJitter`, `WithTimeout`, `WithStatusCode`, `WithRetry`, `WithBulkhead`, `WithCircuitBreaker`
- **go test integration** — `RunT` and `SkipIfShort` for load testing inside `go test`
---

## Mock Server

Pulse includes a **built-in mock HTTP server** for local testing and demos (`cmd/mockserver`). It avoids external dependencies while you try the example YAML files.

**Run** (default address `:8080`):

```sh
go run ./cmd/mockserver -mode healthy
```

Optional: `-addr :9090` to listen on another port (then set `target.url` in your YAML accordingly).

| Mode | Behavior |
|------|----------|
| `healthy` | Always responds **200 OK** quickly with a short body. |
| `mixed-errors` | Alternates **200** and **500** on successive requests (deterministic). |
| `slow` | Sleeps **120ms** before each **200** — useful with `examples/timeout.yaml` (short client timeout). |

```sh
go run ./cmd/mockserver -mode mixed-errors
go run ./cmd/mockserver -mode slow
```

---

## Usage

### 1. Write a config file

```yaml
phases:
  - type: constant
    duration: 30s
    arrivalRate: 50

  - type: ramp
    duration: 30s
    from: 10
    to: 100

  - type: step
    duration: 60s
    from: 10
    to: 100
    steps: 5

  - type: spike
    duration: 60s
    from: 20
    to: 300
    spikeAt: 20s
    spikeDuration: 10s

target:
  method: GET
  url: https://api.example.com/health

maxConcurrency: 100

thresholds:
  errorRate: 0.01       # fail if error rate exceeds 1%
  maxMeanLatency: 200ms # fail if mean latency exceeds 200ms
```

### 2. Run the test

```sh
pulse run config.yaml
```

Optional flags:

| Flag | Description |
|---|---|
| `--json` | Print results as JSON to stdout |
| `--out <file>` | Write results as JSON to a file (can combine with `--json` to mirror the same JSON to stdout) |

---

## Examples

Ready-to-run scenarios live under [`examples/`](examples/). By default they use **`http://localhost:8080`** — pair them with **`go run ./cmd/mockserver`** in the matching mode (see above). Expected outcomes depend on server behavior.

| File | Intent | Suggested mock mode | Example command |
|------|--------|---------------------|-----------------|
| [`baseline.yaml`](examples/baseline.yaml) | Latency SLOs; all thresholds should **PASS** on a fast service | `healthy` | `go run ./cmd/pulse run examples/baseline.yaml` |
| [`mixed-errors.yaml`](examples/mixed-errors.yaml) | Strict `errorRate`; should **FAIL** when failures exceed the limit | `mixed-errors` | `go run ./cmd/pulse run examples/mixed-errors.yaml` |
| [`timeout.yaml`](examples/timeout.yaml) | Short client timeout vs slow responses; error rate should **FAIL** | `slow` | `go run ./cmd/pulse run examples/timeout.yaml` |
| [`post-json.yaml`](examples/post-json.yaml) | POST with JSON body and headers | `healthy` (POST body accepted) | `go run ./cmd/pulse run examples/post-json.yaml` |
| [`put-json.yaml`](examples/put-json.yaml) | PUT with JSON body | `healthy` | `go run ./cmd/pulse run examples/put-json.yaml` |
| [`step.yaml`](examples/step.yaml) | Step phase: discrete rate levels from 10 to 100 RPS in 5 steps | `healthy` | `go run ./cmd/pulse run examples/step.yaml` |
| [`spike.yaml`](examples/spike.yaml) | Spike phase: base 20 RPS, burst to 300 RPS for 10s | `healthy` | `go run ./cmd/pulse run examples/spike.yaml` |

---

## Exit Codes

The `pulse` CLI uses exit codes for automation (e.g. CI):

| Code | Meaning |
|------|--------|
| **0** | Run finished and **all configured thresholds passed** (`pulse.Run` returned no error). |
| **2** | Run finished but **at least one threshold failed** — the error chain contains only `*pulse.ThresholdViolationError` values. |
| **1** | Anything else: invalid usage, config/load failure, I/O error, scheduler/engine failure, or a **mix** of threshold and non-threshold errors. |

---

## JSON Output

With **`--json`**, the CLI prints one indented JSON object to stdout. With **`--out <path>`**, it writes the **same** object to a file. Without **`--json`**, stdout still shows the **text** report when a result is available; with **`--json`**, stdout is JSON only, and you can still add **`--out`** to persist a copy.

**Structure:**

```json
{
  "summary": {
    "total": 0,
    "failed": 0,
    "rps": 0,
    "duration_ms": 0
  },
  "latency": {
    "min_ms": 0,
    "p50_ms": 0,
    "mean_ms": 0,
    "p95_ms": 0,
    "p99_ms": 0,
    "max_ms": 0
  },
  "status_codes": { "200": 0 },
  "errors": { "http_status_error": 0 },
  "thresholds": [
    { "description": "string", "pass": true }
  ],
  "passed": true
}
```

- **Durations** — `summary.duration_ms` is the run length in **milliseconds** (integer). **`latency.*_ms`** values are also in **milliseconds** (floating-point).
- **`passed`** — `true` when **every** configured threshold evaluation succeeded; `false` if any failed. Aligns with [exit code](#exit-codes) **0** vs **2** for threshold-only failures.
- **`thresholds`** — ordered list of individual checks; each entry has a human-readable **`description`** and **`pass`**.

`{}` and `[]` are valid when that part of the result is empty—for instance, no recorded status codes, no classified errors, or no threshold outcomes to list.

---

## Example output

**Text (default):**

```
Total requests: 2250
Failed requests: 12
Duration: 1m0.41s
RPS: 37.25

Min latency: 18ms
P50 latency: 45ms
Mean latency: 52ms
P95 latency: 134ms
P99 latency: 198ms
Max latency: 312ms

Status codes:
  200: 2238
  503: 12

Errors:
  http_status_error: 12

Thresholds:
  PASS error_rate < 0.01
  PASS mean_latency < 200ms
```

**JSON (`--json`):**

```json
{
  "summary": {
    "total": 2250,
    "failed": 12,
    "rps": 37.25,
    "duration_ms": 60410
  },
  "latency": {
    "min_ms": 18,
    "p50_ms": 45,
    "mean_ms": 52,
    "p95_ms": 134,
    "p99_ms": 198,
    "max_ms": 312
  },
  "status_codes": { "200": 2238, "503": 12 },
  "errors": { "http_status_error": 12 },
  "thresholds": [
    { "description": "error_rate < 0.01", "pass": true },
    { "description": "mean_latency < 200ms", "pass": true }
  ],
  "passed": true
}
```

---

## Architecture

```
pulse run config.yaml
        │
        ▼
   config.Load()          Parses YAML → pulse.Test
        │
        ▼
    pulse.Run()           Validates inputs, evaluates thresholds
        │
        ▼
    engine.Run()          Orchestrates phases and concurrency
        │
        ▼
  scheduler.Run()         Token-bucket pacing; constant, ramp, step, and spike phases
        │
        ▼
   Scenario func          Executes the HTTP request via transport.HTTPClient
        │
        ▼
  metrics.Aggregator      Records latency, status code, and error per call
        │
        ▼
    pulse.Result          Returned to the CLI for text or JSON rendering
```

### Components

| Package | Responsibility |
|---|---|
| `pulse` (root) | Public API — `Test`, `Config`, `Phase`, `Run`, `Result`, `ResultHook` |
| `engine` | Runs phases in sequence; manages goroutine lifecycle and concurrency limiter |
| `scheduler` | Fires scenario calls at the configured arrival rate (token bucket) |
| `metrics` | Thread-safe aggregation of latency, status codes, and normalized error categories |
| `transport` | Minimal HTTP client (GET, POST, PUT, DELETE, PATCH) built on `net/http` |
| `config` | YAML loader — maps file config to `pulse.Test` |
| `internal` | Concurrency limiter (semaphore); token bucket helper |

---

## Roadmap

### v0.2.0 ✓
- **Step and spike phases** — discrete and burst arrival-rate scheduling
- **Full HTTP method support** — PUT, DELETE, PATCH
- **Result hook** — `OnResult` callback for post-run integrations

### v0.3.x ✓
- **Algoryn ecosystem** — module path migrated to `algoryn.io/pulse`
- **Fabric integration** — `ToRunEvent` connects Pulse to Algoryn ecosystem
- **go test integration** — `RunT` and `SkipIfShort`
- **Middleware pipeline** — `Chain`, `Apply`, `WithLatency`, `WithErrorRate`
- **Chaos toolkit** — `WithJitter`, `WithTimeout`, `WithStatusCode`, `WithRetry`, `WithBulkhead`, `WithCircuitBreaker`

### Upcoming
- **Export formats** — CSV, OpenTelemetry
- **gRPC transport**
- **docs/architecture.md** — technical design decisions
---

## Part of Algoryn Fabric

Pulse is part of the [Algoryn Fabric](https://github.com/algoryn-io/fabric) ecosystem —
an open source infrastructure toolkit for Go teams building reliable products.

| Tool | What it does | Status |
|------|-------------|--------|
| **Pulse** | Load testing & chaos engineering | `v0.3.6` |
| **Relay** | API Gateway & observability | `coming soon` |
| **Beacon** | Alerting & on-call | `planned` |

---

## License

MIT
