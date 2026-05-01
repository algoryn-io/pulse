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

- **Go** — Schedules load across **constant, ramp, step, and spike** phases, bounds concurrency with a limiter, and runs scenarios at the target requests-per-second. This layer is where scale and determinism come from: token-bucket pacing, goroutine lifecycle, and the `pulse` / library API.
- **C++ (via cgo)** — A native **HDR-style logarithmic histogram** records every latency sample in **O(1)** time with a **fixed bucket count**. The engine covers roughly **1 microsecond to 60 seconds** on a single run with **sub-microsecond** resolution in the low-latency regime and a **stable, constant memory footprint** (no unbounded per-sample storage in the hot path). Percentiles (**P50, P90, P95, P99**) are derived from that histogram, then combined in Go with exact **min, max, and mean** tracking.

This split keeps the scheduler and network I/O in idiomatic, concurrent Go while entrusting percentile math to a compact, allocation-friendly path suited to long campaigns and high sample counts.

### Fabric integration

Pulse depends on [`algoryn.io/fabric`](https://github.com/algoryn-io/fabric) **v0.2.0+** for shared contracts, including **Protocol Buffer** messages under `algoryn.io/fabric/gen/go/fabric/v1`.

- **`ToRunEvent`** maps a `pulse.Result` into the legacy Go types `metrics.RunEvent` / `metrics.MetricSnapshot` (`algoryn.io/fabric/metrics`).
- **`ToRunEventProto`** returns the same data as **`fabric.v1.RunEvent`** (with **`fabric.v1.MetricSnapshot`** inside), using Fabric’s **`RunEventToProto`** helper so timestamps are **`google.protobuf.Timestamp`**.
- **`ToFabricRunEmit`** returns a matched pair: the full **`RunEvent`** protobuf and a **`fabric.v1.Event`** with **`EVENT_TYPE_RUN_COMPLETED`** (payload built with **`RunCompletedPayloadToProto`**), sharing one **`run_id`** so tools like **Beacon** can correlate summary events with detailed snapshots. The envelope timestamp uses **`timestamppb.Now()`**.
- Set **`Config.Service`** (optional) to populate **`MetricSnapshot.service`** and the run-completed payload; use **`Config.OnFabricEmit`** to receive both protobuf messages after each run (after threshold evaluation, same ordering as **`OnResult`**).

The CLI still prints human JSON for operators; wire **`OnFabricEmit`** (or call **`ToRunEventProto`**) when you need **proto/binary** or **protojson** on the wire.

---

## Key features

| Area | What you get |
|------|----------------|
| **Load model** | **Multi-phase** tests: **Constant**, **Ramp**, **Step**, and **Spike** — arrival-rate (RPS) driven, not a vague “VUs” count. |
| **Latency** | **P50, P90, P95, P99** (plus min, mean, max) from the **C++ histogram**; stable under load, bounded memory. |
| **Configuration** | **YAML** test definitions: target, phases, `maxConcurrency`, and optional **thresholds** (error rate, mean / P95 / P99 latency). |
| **Output** | **Text** (human-readable) and **JSON** (automation, CI artifacts); combine `--json` and `--out` to mirror JSON to a file. |
| **API** | Use **`pulse.Run`**, `OnResult` hooks, optional **`OnFabricEmit`** for **Fabric protobuf** (`RunEvent` + `RunCompleted` event), and **middleware** for chaos-style scenarios; **`RunT`** for `go test` integration. |
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
| `--json` | Print results as **JSON** on stdout. |
| `--out <file>` | Write the same JSON object to a file (can be combined with `--json`). |

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

maxConcurrency: 4

thresholds:
  maxMeanLatency: 100ms
  maxP95Latency: 150ms
  maxP99Latency: 200ms
```

Run against a live target or, for a quick check, start the bundled mock in another terminal: `go run ./cmd/mockserver -mode healthy` and point the URL at it. More examples live under [`examples/`](examples/).

### JSON result shape (summary)

The JSON report includes `summary` (totals, RPS, `duration_ms`), `latency` with **`min_ms`**, **`p50_ms`**, **`mean_ms`**, **`p90_ms`**, **`p95_ms`**, **`p99_ms`**, **`max_ms`**, `status_codes`, `errors`, per-threshold rows, and `passed`.

---

## Ecosystem

Pulse is part of the **Algoryn Fabric** ecosystem: shared contracts under `algoryn.io/fabric` help **Relay**, **Beacon**, and other tools present and consume performance and reliability data in a **consistent** way. Pulse focuses on *generating* evidence under load; Fabric types carry that evidence to the rest of the stack.

---

## License

MIT
