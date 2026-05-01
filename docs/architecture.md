# Pulse — Architecture

This document describes the key design decisions behind Pulse and
explains how the components fit together. It is intended for
contributors and developers who want to extend or embed Pulse.

---

## Design principles

Pulse is built around six principles that drive every decision:

**Code-first configuration** — load tests are Go code, not YAML scripts
or external DSLs. The `Scenario` type is a plain Go function, which
means the full power of the language is available inside every test.

**Library-first, CLI-second** — Pulse is designed to be imported as a
Go module before it is used as a command-line tool. The CLI is a thin
wrapper around the public API.

**Arrival-rate scheduling** — load is expressed as requests per second,
not as a number of virtual users. This models real-world traffic more
accurately and produces deterministic, reproducible results.

**Explicit concurrency model** — goroutine creation and synchronization
are controlled and measurable. The engine uses a semaphore to bound
concurrency and a token bucket to pace request generation.

**Low-overhead metrics** — metrics collection runs in a dedicated
goroutine and receives results through a buffered channel. This decouples
measurement from execution and avoids introducing noise into latency
measurements.

**Composable chaos** — fault injection is implemented as a middleware
pipeline. Each `Middleware` wraps a `Scenario` and can be composed with
others using `Chain` or `Apply`. This keeps the engine clean and makes
chaos primitives reusable across tests.

---

## Component overview
```
pulse.Run(test)
      │
      ▼
  engine.Run()          orchestrates phases, manages lifecycle
      │
      ▼
  scheduler.Run()       token-bucket pacing per phase
      │
      ▼
  Scenario func         user-defined workload (optionally wrapped with middleware)
      │
      ▼
  metrics.Aggregator    receives results via channel, computes percentiles
      │
      ▼
  pulse.Result          returned to the caller
```

---

## Scheduler and token bucket

The scheduler is the heart of Pulse. It controls when and how often
the `Scenario` is executed.

### Why arrival rate, not virtual users

Virtual user (VU) models tie concurrency to load — more VUs means more
load, but also more goroutines sleeping between requests. This makes
the relationship between VUs and RPS dependent on scenario latency,
which changes as the system degrades under load.

Arrival rate is independent of latency. If the target is 50 RPS, the
scheduler fires 50 executions per second regardless of how long each
one takes. This produces consistent, comparable results across runs.

### Token bucket implementation

The token bucket lives in `internal/tokenbucket.go`. Key properties:

- **Non-blocking** — `Allow(now time.Time)` returns immediately
- **Caller-controlled clock** — time is passed in, making the bucket
  deterministic and easy to test
- **Variable refill rate** — `SetRefillRate(rate, now)` applies pending
  refill at the current rate before switching, avoiding token leaks

The scheduler polls the bucket every millisecond. When a token is
available, it launches a goroutine bounded by the concurrency limiter.

### Phase types

| Type | Behavior | Key fields |
|------|----------|------------|
| `constant` | Fixed arrival rate | `ArrivalRate` |
| `ramp` | Linear interpolation | `From`, `To` |
| `step` | Discrete rate levels | `From`, `To`, `Steps` |
| `spike` | Base rate with a burst | `From`, `To`, `SpikeAt`, `SpikeDuration` |

---

## Concurrency model

The engine creates one goroutine per execution event. Concurrency is
bounded by a semaphore in `internal/concurrency.go`:
```go
type Limiter struct {
    slots chan struct{}
}
```

`Acquire` blocks until a slot is available or the context is cancelled.
`Release` frees the slot after the scenario completes. This prevents
goroutine explosion under high arrival rates with slow scenarios.

---

## Metrics pipeline

Results flow from scenario goroutines to the aggregator through a
buffered channel:
```
goroutine 1 ─┐
goroutine 2 ─┼──► metrics channel ──► Aggregator goroutine ──► Result
goroutine N ─┘
```

The aggregator maintains running totals for count, errors, and latency.
Percentiles (p50, p95, p99) are computed from a sorted slice of latency
samples at the end of the run, not incrementally.

---

## Middleware pipeline

The `Middleware` type is:
```go
type Middleware func(Scenario) Scenario
```

This is the same pattern as `net/http` handlers. Middlewares are applied
with `Chain` or `Apply`:
```go
Apply(myScenario,
    WithLatency(50*time.Millisecond, 0.1),
    WithCircuitBreaker(0.3, 10*time.Second, 5*time.Second),
)
```

`Chain` applies middlewares in order — the first middleware is the
outermost wrapper. The `Scenario` is called last.

### Built-in middlewares

| Middleware | Purpose |
|-----------|---------|
| `WithLatency` | Inject fixed latency |
| `WithJitter` | Inject random latency |
| `WithErrorRate` | Inject random failures |
| `WithStatusCode` | Inject specific HTTP status codes |
| `WithTimeout` | Enforce per-execution timeout |
| `WithRetry` | Retry on failure with backoff |
| `WithBulkhead` | Limit concurrent executions |
| `WithCircuitBreaker` | Simulate cascading failures |

---

## go test integration

`RunT` bridges Pulse with Go's testing infrastructure:
```go
func RunT(t TB, test Test) Result
```

It captures `startedAt` before calling `Run`, reports metrics via
`t.Logf`, and calls `t.Fatalf` on threshold violations. The `TB`
interface (not `*testing.T` directly) allows the function to be tested
with a fake implementation.

`SkipIfShort` integrates with `go test -short` to exclude load tests
from quick test runs.

---

## Algoryn Fabric integration

Pulse maps **`pulse.Result`** to Fabric contracts so Relay, Beacon, and other tools share one binary shape.

```go
// Legacy Go contracts (algoryn.io/fabric/metrics)
func ToRunEvent(result Result, passed bool, startedAt time.Time) fabricmetrics.RunEvent

// Protocol buffers (algoryn.io/fabric/gen/go/fabric/v1)
func ToRunEventProto(result Result, passed bool, startedAt time.Time) *fabricv1.RunEvent

// Matched RunEvent + EVENT_TYPE_RUN_COMPLETED envelope (common run id)
func ToFabricRunEmit(service string, result Result, passed bool, startedAt time.Time) FabricRunEmit
```

After each run, **`Run`** records **`startedAt`** before the engine executes, evaluates thresholds, then invokes optional **`Config.OnFabricEmit(run *fabricv1.RunEvent, completed *fabricv1.Event)`** before **`OnResult`**. Use **`Config.Service`** for **`MetricSnapshot.service`** and run-completed metadata.

Conversion helpers from the **`algoryn.io/fabric`** module (**`RunEventToProto`**, **`RunCompletedPayloadToProto`**, **`MetricSnapshotToProto`**) keep field mapping consistent with the `.proto` definitions.

---

## Extending Pulse

### Adding a new phase type

1. Add a constant to `model/phase.go`
2. Add the constant alias to `api.go`
3. Add validation in `validateTest` in `api.go`
4. Add validation in `validateConfig` in `config/config.go`
5. Add a `run{Type}` function in `scheduler/scheduler.go`
6. Add a case in `scheduler.Run`
7. Add YAML fields to `phaseConfig` in `config/config.go`
8. Add an example YAML to `examples/`

### Adding a new middleware

Create a function with this signature in `middleware.go` or a new file:
```go
func WithMyBehavior(params...) Middleware {
    return func(next Scenario) Scenario {
        return func(ctx context.Context) (int, error) {
            // before
            status, err := next(ctx)
            // after
            return status, err
        }
    }
}
```

### Adding a new transport

Implement a scenario function that uses your transport and pass it
as the `Scenario` field in `pulse.Test`. The transport layer is
intentionally not abstracted — the `Scenario` function is the extension
point.