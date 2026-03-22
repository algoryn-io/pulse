# Pulse

**Pulse** is a programmable reliability and load testing engine written in Go.

It generates controlled HTTP load against a target, collects latency and error metrics, and evaluates configurable pass/fail thresholds. Tests are driven by a YAML config file and executed through the `pulse` CLI.

---

## Features

- **Arrival-rate scheduling** — request-driven load (requests/sec), with constant and ramp phases (not user/VU-based)
- **Bounded concurrency** — configurable goroutine limit prevents runaway resource usage
- **Metrics aggregation** — total, failed, RPS, latency (min, mean, p50, p95, p99, max), status code distribution, normalized error categories
- **Thresholds** — `error_rate` and `mean_latency` with PASS / FAIL output
- **HTTP transport** — GET and POST support via `net/http`
- **CLI** — `pulse run <config.yaml>` with human-readable text and JSON output modes

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
| `--out <file>` | Write JSON results to a file |

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
  "Total": 2250,
  "Failed": 12,
  "Duration": 60410000000,
  "RPS": 37.25,
  "Latency": {
    "Min": 18000000,
    "Mean": 52000000,
    "P50": 45000000,
    "P95": 134000000,
    "P99": 198000000,
    "Max": 312000000
  },
  "StatusCounts": { "200": 2238, "503": 12 },
  "ErrorCounts": { "http_status_error": 12 }
}
```

> **Note:** durations in JSON are encoded in nanoseconds (Go `time.Duration` default representation).

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
  scheduler.Run()         Fires scenario calls at the target arrival rate
  (constant / ramp)
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
| `pulse` (root) | Public API — `Test`, `Config`, `Phase`, `Run`, `Result` |
| `engine` | Runs phases in sequence; manages goroutine lifecycle and concurrency limiter |
| `scheduler` | Fires scenario calls at the configured arrival rate (ticker for constant, interpolated interval for ramp) |
| `metrics` | Thread-safe aggregation of latency, status codes, and normalized error categories |
| `transport` | Minimal HTTP client (GET / POST) built on `net/http` |
| `config` | YAML loader — maps file config to `pulse.Test` |
| `internal` | Concurrency limiter (semaphore) |

---

## Roadmap

- **Token bucket scheduler** — smoother burst control for constant and ramp phases (stub exists in `internal/tokenbucket.go`)
- **Additional phase types** — step, spike
- **More HTTP methods** — PUT, DELETE, PATCH
- **Export formats** — CSV, OpenTelemetry
- **gRPC transport**

---

## License

MIT
