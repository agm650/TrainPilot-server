# TrainPilot benchmark guide

`trainpilot-bench` generates reproducible HTTP and WebSocket load against a
running TrainPilot server. Run it on a separate machine when measuring server
performance so generator resource use is not attributed to the server.

Use `docs/BENCHMARK-MONITORING.md` to connect an existing Prometheus and
Grafana installation and to measure monitoring overhead.

## Safety

The tool is passive unless its profile contains active operations. Active
profiles require `--allow-active-commands`.

When the server reports a driver other than `simulator`, active profiles also
require `--allow-real-hardware`. This second confirmation is deliberate.

Simulator feedback injection requires all of the following:

- the server uses the simulator driver;
- `testAPI` is enabled on the server;
- the profile defines a positive feedback rate;
- `--allow-simulator-api` is present.

The runner records the initial track-power state. It enables power for active
operations and restores an initially disabled state during cleanup. It also
requests zero speed before releasing leases.

## Build

During development:

```bash
go build -o trainpilot-bench ./cmd/trainpilot-bench
```

Release archives contain Linux and macOS builds for amd64 and arm64.

## Prepare benchmark users

Users must be created through the local administration socket. The load
generator never creates users through the remote API.

Run this once on the server:

```bash
export TRAINPILOT_BENCH_PASSWORD='replace-with-a-test-password'
export BENCHMARK_USER_COUNT=4
scripts/benchmark-prepare-users.sh
```

The default role is `dispatcher`, which can exercise driving, turnout, and
route operations. Override it with `BENCHMARK_USER_ROLE`. Override the binary
or socket with `DCCD_BIN` and `DCCD_SOCKET`.

The script reads the password from the environment and pipes it to
`dccd user add --password-stdin`. It does not print the password.

## Credentials

Copy `benchmarks/fixtures/credentials.example.json` outside the repository or
use it as a template. It references environment variables and contains no
passwords.

```bash
cp benchmarks/fixtures/credentials.example.json /tmp/benchmark-credentials.json
chmod 600 /tmp/benchmark-credentials.json
export TRAINPILOT_BENCH_PASSWORD='replace-with-a-test-password'
```

A credentials document is versioned:

```json
{
  "schemaVersion": 1,
  "accounts": [
    {
      "username": "benchmark-01",
      "passwordEnv": "TRAINPILOT_BENCH_PASSWORD"
    }
  ]
}
```

Each account must define exactly one of `password` or `passwordEnv`. Plaintext
password files are accepted for offline preparation, but must have `0600`
permissions on Unix. Neither passwords nor tokens are copied into reports.

Credentials can instead be supplied without a file:

```bash
--credential benchmark-01=TRAINPILOT_BENCH_PASSWORD
```

Repeat `--credential` for multiple pre-provisioned accounts. The runner can
create several sessions per account.

## Profiles and fixtures

Profiles use strict, versioned YAML. Unknown fields are rejected.

```yaml
schema_version: 1
name: example
warmup: 5s
duration: 60s
seed: 650
fixture: ../fixtures/simulator-default.json
operation_timeout: 5s

clients:
  users: 4
  websockets: 4
  active_locomotives: 2
  workers: 16

rates:
  login_per_second: 0.2
  refresh_per_second: 0.2
  lease_acquire_per_second: 0.2
  lease_heartbeat_per_second: 2
  lease_release_per_second: 0.2
  throttle_per_second: 10
  functions_per_second: 3
  feedback_per_second: 10
  accessories_per_second: 2
  route_operations_per_second: 1
  reads_per_second: 5

behavior:
  reconnect_probability: 0.001
  snapshot_probability: 0.001
```

Rates are operations per second. `warmup` runs the same workload without adding
operation samples to measured percentiles. `duration` is the measured
phase. `operation_timeout` applies to individual HTTP and connection attempts.

Fixtures select stable locomotive IDs, map simulator feedback addresses to
expected block events, and may declare incompatible route pairs. The broader
small-to-xlarge data sets live under `benchmarks/fixtures/`. Each generated
data set contains importable rolling-stock and layout archives plus the runner
fixture. The runner rejects a server whose resource counts do not match the
selected data set.
Profile fixture paths are relative to the profile file. A `--fixture` override
is relative to the current working directory.

Regenerate a deterministic data set outside the measured phase:

```bash
trainpilot-bench generate-fixture medium --output /tmp/trainpilot-medium
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-rolling-stock \
  /tmp/trainpilot-medium/rolling-stock.dcclib --replace
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-layout \
  /tmp/trainpilot-medium/layout.dcclayout --replace
```

Profiles may declare simultaneous scheduled operations:

```yaml
bursts:
  - at: 1m
    operation: feedback
    count: 50
```

Burst offsets start when the run starts and may fall in warm-up or measurement.
`behavior.drop_event_probability` deterministically discards selected received
events so the next sequence triggers the normal snapshot resynchronization.
See `benchmarks/README.md` for the capacity, storm, ramp-up, failure, and soak
scenario matrix.

Validate a profile before use:

```bash
trainpilot-bench validate-profile benchmarks/profiles/smoke.yaml
```

## Run the simulator smoke profile

Start a server with the simulator, `testAPI: true`, and `seedDemo: true`. Then
run the generator from another machine:

```bash
trainpilot-bench run \
  --server http://192.168.1.20:8080 \
  --profile benchmarks/profiles/smoke.yaml \
  --credentials /tmp/benchmark-credentials.json \
  --allow-active-commands \
  --allow-simulator-api \
  --output benchmarks/results/simulator-smoke.json
```

Use `--duration 10s` for a short development check. Use `--seed` to override
the profile seed.

## Generated load

The runner supports:

- login and refresh;
- WebSocket connections, heartbeats, reconnects, and snapshot requests;
- lease acquisition, heartbeat, and release;
- throttle and locomotive functions;
- turnout commands;
- route reservation, activation, and release;
- resource and health reads;
- simulator feedback injection when explicitly enabled.

Scheduled operation timing and operation choices derive from the profile seed.
Network and server scheduling remain real-time and are therefore not fully
deterministic.

## Report

The JSON report has `schemaVersion: 1`. It includes:

- benchmark, server, API, event API, and station-driver versions;
- start and end timestamps;
- warm-up and measured durations;
- the full effective profile and profile/fixture SHA-256 hashes;
- seed and client host metadata;
- totals, successes, failures, timeouts, skipped schedules, throughput, p50,
  p90, p95, p99, and maximum latency per operation;
- WebSocket connections, reconnects, sequence gaps, snapshots, events, and
  feedback-to-event latency;
- invariant observations and the overall `PASS` or `FAIL` result.

Latency samples are retained with `time.Duration` precision for the run, then
sorted once when the report is produced. This provides exact sample
percentiles without adding measurement work to the server.

The output is written atomically with `0600` permissions. Real files under
`benchmarks/results/` are ignored by Git.

## Invariants

The runner monitors:

- exclusive locomotive leases;
- rejection of a command with an invalid lease;
- configured incompatible routes;
- WebSocket gap resynchronization;
- absence of implicit locomotive restart after reconnection;
- action-to-event consistency;
- valid JSON responses and events;
- continued server health.

Any observed violation sets `overallResult` to `FAIL`, independently of
latency. An invariant that cannot be exercised by the selected fixture is
reported as `NOT_OBSERVED` rather than silently claimed as tested.

Comparison thresholds and historical report comparison are intentionally
deferred to the reporting and comparison work item.
