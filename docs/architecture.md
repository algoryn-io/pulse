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
not as a number of virtual users. The default `drop` saturation policy
keeps scheduling independent of target latency and reports arrivals that
cannot start immediately.

**Explicit concurrency model** — goroutine creation and synchronization
are controlled and measurable. The engine uses a semaphore to bound
concurrency and a token bucket to pace request generation.

**Bounded-memory metrics** — completed scenarios record totals, exact
min / max / mean values, and latency samples in a fixed-size native
logarithmic histogram. Memory usage does not grow with sample count.

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
  metrics.Aggregator    records results in a fixed-size native histogram
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

Arrival rate is independent of latency when the default `drop` policy is
used. If the target is 50 RPS, the scheduler attempts 50 executions per
second regardless of how long each one takes. Arrivals that cannot start
because `maxConcurrency` is exhausted are counted as dropped. This
produces honest, comparable results across runs.

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

`TryAcquire` reserves a slot without waiting. It is used by the default
`drop` saturation policy so a slow target cannot apply implicit
backpressure to the scheduler. `Acquire` remains available for the
optional `block` policy, which waits until a slot is available or the
context is cancelled. `Release` frees the slot after the scenario
completes.

The engine reports `Scheduled`, `Started`, `Dropped`, `DroppedRate`,
`Completed`, and `MaxActive`. These values make generator saturation
visible independently of target-side failures.

When `Config.Reporting.Interval` is greater than zero, the engine also
records interval snapshots aligned to the run start. Arrival handling
metrics (`Scheduled`, `Started`, and `Dropped`) belong to the interval
where the arrival is handled. Completion metrics (`Completed`, failures,
status codes, errors, and latency) belong to the interval where scenario
execution finishes. This attribution makes slow requests visible in the
window where they consume time rather than the window where they were
scheduled.

Enabled intervals must be at least 10 milliseconds, and a run may
produce at most 10,000 snapshots. `MaxConcurrency` is capped at
1,000,000. These validation limits reject configurations that could
allocate excessive memory before useful work begins.

The built-in HTTP transport uses a 30-second request timeout and drains
at most 1 MiB from each response body by default. Both limits are
configurable. YAML files are limited to 1 MiB. YAML targets intentionally
allow arbitrary HTTP and HTTPS URLs, so automated systems must only
execute trusted configuration files.

---

## Metrics pipeline

Completed scenario goroutines record directly into the aggregator:
```
goroutine 1 ─┐
goroutine 2 ─┼──► metrics.Aggregator mutex ──► C++ histogram
goroutine N ─┘
```

The aggregator maintains running totals for count, errors, exact min /
max / mean latency, and a native fixed-size histogram with 800
logarithmic buckets from 1 microsecond to 60 seconds. P50, P90, P95, and
P99 are estimates derived from that histogram. Samples outside the
histogram range are clamped for percentile bucketing.

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

Library callers that need cancellation or a global deadline should use:
```go
func RunContext(ctx context.Context, test Test) (Result, error)
```
`Run(test)` remains a convenience wrapper using `context.Background()`.

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

Load-fidelity fields are exposed through `pulse.Result`, `OnResult`, and CLI JSON. They are not part of Fabric protobuf output until the shared Fabric schema defines matching fields.

Interval snapshots are exposed through `pulse.Result.Snapshots`,
`OnResult`, and CLI JSON. The text CLI output remains a global summary.
Snapshots are not emitted in Fabric protobuf messages until the shared
schema defines an interval representation.

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
