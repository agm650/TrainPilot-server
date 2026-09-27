# TrainPilot benchmark scenarios

All profiles use schema version 1 and seed 650. Rates are operations per
second. Run the generator on a machine other than the measured server. Real
hardware is outside this benchmark matrix.

## Deterministic data sets

| Preset | Locomotives | Blocks | Turnouts | Routes | Active locomotives |
| --- | ---: | ---: | ---: | ---: | ---: |
| `small` | 50 | 20 | 10 | 10 | 3 |
| `small-real` | 12 | 20 | 10 | 10 | 3 |
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
| `small` | Original small capacity load | 2 min + 10 min | operation latency, WS and feedback latency |
| `small-real` | Three-operator domestic workload | 2 min + 10 min | command and feedback latency, disk activity |
| `medium` | Raspberry Pi 3 B+ target | 2 min + 10 min | p95/p99, CPU, WAL and scheduler drops |
| `large` | Large club layout | 2 min + 10 min | saturation, queues, memory and errors |
| `xlarge` | Deliberate limit search | 2 min + 10 min | throughput ceiling and recovery |

All capacity profiles require zero unexpected errors, invariant violations,
panics, crashes, invalid JSON, and SQLite corruption. Normal mixed traffic may
receive HTTP 409 while a released lease is stopping, a block is occupied, or a
route conflicts. Those safety refusals are declared with profile
`expected_errors` rules. Dedicated contention profiles still exercise and
verify deliberate collisions separately.

`small-real` is a separate simulator-only workload. Its fixture has 12
registered locomotives, with three selected for control, 20 blocks, 10 turnouts,
and 10 routes. Three virtual users each acquire one initial locomotive lease;
no scheduled acquire or release changes that count. The aggregate rates target
an average of 50 speed, 3 function, and 2 turnout commands per minute per user,
plus 10 simulated occupancy changes per minute per active locomotive. This is 165
control commands and 30 feedback injections per minute in total. It also
schedules 3 route operations, 6 reads, and 6 lease heartbeats per minute;
the runner always adds 60 health checks per minute. Scheduled login and token
refresh operations are disabled. Initial logins and token refresh before
expiry still occur. The planned total is 270 operations per minute, excluding
WebSocket setup and automatic token maintenance.

The 12 registered locomotives represent a capacity of four per user, but the
runner does not assign roster ownership or enforce exact per-user rates.

The benchmark currently selects feedback targets at random and toggles their
state. `small-real` therefore models a lower feedback rate, not train movement
through a physically coherent sequence of blocks. Control commands also pick
their selected locomotive, turnout, and values at random. Run it only against
the simulator with `--allow-active-commands --allow-simulator-api`; do not
interpret a pass as validation of physical railway behavior.

Import `benchmarks/fixtures/small-real/rolling-stock.dcclib` and
`benchmarks/fixtures/small-real/layout.dcclayout` with `--replace` before the
run. The runner requires the exact resource counts in the fixture. Use
`benchmarks/profiles/small-real.yaml` and the matching `fixture.json` for the
run; do not compare its report directly with `small` as if the loads matched.
Replacing rolling stock requires no active locomotive lease; replacing the
layout requires no pending turnout configuration.

```bash
./bin/dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-rolling-stock --replace \
  benchmarks/fixtures/small-real/rolling-stock.dcclib
./bin/dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-layout --replace \
  benchmarks/fixtures/small-real/layout.dcclayout
./bin/trainpilot-bench validate-profile benchmarks/profiles/small-real.yaml \
  --fixture benchmarks/fixtures/small-real/fixture.json
./bin/trainpilot-bench run --server "$TRAINPILOT_URL" \
  --profile benchmarks/profiles/small-real.yaml \
  --fixture benchmarks/fixtures/small-real/fixture.json \
  --credentials /tmp/benchmark-credentials.json \
  --allow-active-commands --allow-simulator-api \
  --output benchmarks/results/jetson-nano/small-real.json
```

## CI smoke profiles

The Ubuntu `benchmark-smoke` job imports the `small` data set and runs three
short, fixed-seed phases. `smoke` covers active operations and a feedback burst,
`ci-websocket-reconnect` requires an observed reconnect, and
`ci-websocket-resync` requires an observed and recovered sequence gap. These
profiles detect functional regressions. They define no runner-dependent latency
or resource threshold.

## Storm profiles

- `feedback-storm`: medium baseline plus simultaneous bursts of 50, 100, and
  250 feedback operations. Check WS gaps, snapshots, resynchronization, and
  final block state.
- `login-storm`: simultaneous groups of 10, 20, and 50 logins. Report login
  CPU and p95/p99 separately because password hashing dominates this path.
- `websocket-reconnect-storm`: 50 clients reconnect after each post-snapshot
  event. Check initial snapshots and goroutine/file-descriptor recovery.
- `lease-contention`: 10, 20, then 50 clients target one stable locomotive.
  HTTP 409 is declared as expected; more than one accepted lease is an invariant failure.
- `route-contention`: 10, 20, then 50 operations use declared incompatible
  route pairs. HTTP 409 is declared as expected; incompatible activation is a failure.

## Ramp-up

Use the generated `xlarge` data set. Run: idle 0-2 min, small 2-5 min, medium
5-8 min, large 8-12 min, xlarge 12-15 min, a storm profile 15-17 min, then
medium 17-20 min. `scripts/benchmark-run-ramp.sh` runs this sequence and uses
the xlarge fixture override for every stage. Keep the same seed. Compare
queues, goroutines, RSS, and latency before and after overload. Use
`trainpilot-bench compare` with at least three reports in each group to compare
the medians of repeated runs.

The `ramp-burst` stage injects 250 simultaneous feedback operations.

## Simulator failure scenarios

Files under `benchmarks/scenarios/` are simulator scenario v2 documents. Pass
the scenario to `trainpilot-bench run --simulator-scenario`; the runner starts
it at measurement start and advances its manual clock at wall-clock speed.

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
- `station-full-recovery`: applies 100 ms and 500 ms delays, then degraded,
  offline, and online states under the matching medium profile.
- `intermittent-fault-recovery`: applies every-N and burst command errors,
  accessory confirmation faults, a short circuit, bounce, and feedback loss.

Expected errors in the matching profiles are bounded by measured-phase `from`
and `to` offsets. Errors outside those windows fail the report.

## Soak profiles

`soak-6h-medium` and `soak-24h-medium` use stable medium load, occasional
reconnections, snapshots, leases, feedback, routes, and functions. Check RSS,
Go heap and objects, goroutines, file descriptors, threads, DB/WAL size,
CPU, p95/p99, errors, and trends. The 24-hour run is manual and must not block
pull requests.

`restart-recovery-medium` observes one operator-controlled process outage in
its 3:00-4:00 measured window. It requires `--allow-planned-outage` and never
restarts the service itself. Full step-by-step procedures are in
`docs/BENCHMARK-SOAK-FAULT-RECOVERY.md`.
