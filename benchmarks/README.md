# TrainPilot benchmark scenarios

All profiles use schema version 1 and seed 650. Rates are operations per
second. Run the generator on a machine other than the measured server. Real
hardware is outside this benchmark matrix.

## Deterministic data sets

| Preset | Locomotives | Blocks | Turnouts | Routes | Active locomotives |
| --- | ---: | ---: | ---: | ---: | ---: |
| `small` | 50 | 20 | 10 | 10 | 3 |
| `medium` | 250 | 100 | 50 | 75 | 10 |
| `large` | 1,000 | 250 | 150 | 200 | 25 |
| `xlarge` | 5,000 | 1,000 | 500 | 1,000 | 50 |

IDs and archive timestamps are stable. Each fixture directory contains
`rolling-stock.dcclib`, `layout.dcclayout`, and `fixture.json`. Regenerate all
three files with `trainpilot-bench generate-fixture <preset> --output <dir>`.
Import both archives before a run. Data preparation is not part of the measured
phase. The runner checks the exact resource counts before starting.

## Capacity profiles

| Profile | Objective | Duration | Important metrics |
| --- | --- | --- | --- |
| `idle` | Server and one-WebSocket baseline | 2 min warm-up + 10 min | CPU, RSS, goroutines, idle WS traffic |
| `small` | Typical domestic layout | 2 min + 10 min | operation latency, WS and feedback latency |
| `medium` | Raspberry Pi 3 B+ target | 2 min + 10 min | p95/p99, CPU, WAL and scheduler drops |
| `large` | Large club layout | 2 min + 10 min | saturation, queues, memory and errors |
| `xlarge` | Deliberate limit search | 2 min + 10 min | throughput ceiling and recovery |

All capacity profiles require zero unexpected errors, invariant violations,
panics, crashes, invalid JSON, and SQLite corruption. Expected contention and
offline refusals belong to their named scenario operation, not normal traffic.

## Storm profiles

- `feedback-storm`: medium baseline plus simultaneous bursts of 50, 100, and
  250 feedback operations. Check WS gaps, snapshots, resynchronization, and
  final block state.
- `login-storm`: simultaneous groups of 10, 20, and 50 logins. Report login
  CPU and p95/p99 separately because password hashing dominates this path.
- `websocket-reconnect-storm`: 50 clients reconnect after each post-snapshot
  event. Check initial snapshots and goroutine/file-descriptor recovery.
- `lease-contention`: 10, 20, then 50 clients target one stable locomotive.
  HTTP 409 is expected; more than one accepted lease is an invariant failure.
- `route-contention`: 10, 20, then 50 operations use declared incompatible
  route pairs. HTTP 409 is expected; incompatible activation is a failure.

## Ramp-up

Use the generated `xlarge` data set. Run: idle 0-2 min, small 2-5 min, medium
5-8 min, large 8-12 min, xlarge 12-15 min, a storm profile 15-17 min, then
medium 17-20 min. `scripts/benchmark-run-ramp.sh` runs this sequence and uses
the xlarge fixture override for every stage. Keep the same seed. Compare
queues, goroutines, RSS, and latency before and after overload. Automated
single-report aggregation belongs to the reporting work item.

The `ramp-burst` stage injects 250 simultaneous feedback operations.

## Simulator failure scenarios

Files under `benchmarks/scenarios/` are simulator scenario v2 documents. Load
and start one immediately before running `medium`; use real-time mode.

- `station-latency-recovery`: 100 ms, then 500 ms delays on active operations,
  then nominal behavior. Expected errors are operation timeouts only if the
  configured timeout is exceeded.
- `station-degraded-recovery`: online, degraded, then online. Station events
  must be ordered and active commands must follow server policy.
- `station-offline-recovery`: online, degraded, offline, then online. Command
  refusals while offline are expected. No locomotive may restart implicitly.
- `feedback-loss-and-resync`: use the matching profile to discard selected WS
  events deterministically while the simulator suppresses one physical update.
  The next sequence exposes the gap. A snapshot must restore coherent state.

## Soak profiles

`soak-6h-medium` and `soak-24h-medium` use stable medium load, occasional
reconnections, snapshots, leases, feedback, routes, and functions. Check RSS,
Go heap and objects, goroutines, file descriptors, threads, DB/WAL size,
CPU, p95/p99, errors, and trends. The 24-hour run is manual and must not block
pull requests.
