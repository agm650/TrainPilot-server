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
The runner refreshes each virtual user's access token before expiry. These
maintenance refreshes are separate from the scheduled `refresh_per_second`
operations, which remain part of the measured workload. An authentication
failure during a WebSocket handshake triggers one forced refresh. Reconnection
failures use bounded exponential backoff.

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
Expected failures are explicit and scoped to an operation. A rule matches when
its HTTP status, problem code, or supported kind matches:

```yaml
expected_errors:
  - operation: lease_contention
    http_statuses: [409]
    problem_codes: [lease_conflict]
  - operation: throttle
    kinds: [timeout]
    from: 1m
    to: 2m
```

`from` is inclusive and `to` is exclusive. Both are measured-phase offsets and
must be supplied together. Expected health failures require a bounded window;
the profile then also requires `--allow-planned-outage`. Errors outside a
declared window remain unexpected.

`behavior.drop_event_probability` deterministically discards selected received
events so the next sequence triggers the normal snapshot resynchronization.
See `benchmarks/README.md` for the capacity, storm, ramp-up, failure, and soak
scenario matrix.

A simulator scenario can be synchronized with the measured phase:

```bash
trainpilot-bench run \
  --profile benchmarks/profiles/station-full-recovery-medium.yaml \
  --simulator-scenario benchmarks/scenarios/station-full-recovery.json \
  --allow-active-commands --allow-simulator-api \
  --credentials /tmp/benchmark-credentials.json \
  --output benchmarks/results/station-full-recovery.json
```

Add `--metrics-listen 127.0.0.1:6061` to expose optional live generator
metrics to a Prometheus process on the same host. The option is disabled by
default. For a remote scrape, bind a private load-generator address and follow
the network restrictions in `docs/BENCHMARK-MONITORING.md`.

The runner loads and starts the scenario at measurement start. It advances the
manual simulator clock at wall-clock speed and records each applied step. This
mode never accelerates soak time.

Validate a profile before use:

```bash
trainpilot-bench validate-profile benchmarks/profiles/smoke.yaml
```

## Run the simulator smoke profile

The `benchmark-smoke` GitHub Actions job runs this profile on Ubuntu for every
push and pull request. It imports the deterministic `small` fixture, provisions
benchmark users through the local Unix socket, and runs three phases. The main
phase uses 5 seconds of warm-up plus 30 measured seconds, a fixed seed, active
commands, and a feedback burst. A 1 + 15 second phase exercises WebSocket
reconnection. A final 1 + 10 second phase drops events and exercises snapshot
resynchronization. Keeping these behaviors sequential avoids hiding their
individual results. Functional failures, missing reconnects or gaps, unresolved
gaps, timeouts, crashes, and a failed final health check fail the job. Latency
and host-resource values never fail this CI job.

On failure, the job retains any reports already produced, benchmark and server
logs, the secret-free server configuration, Prometheus metrics, and Go heap and
goroutine profiles. Credentials, tokens, and the temporary database are not
uploaded.

For a manual run, start a server with the simulator and `testAPI: true`. Import
the `small` rolling-stock and layout archives, then run the generator from
another machine:

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

The CI smoke benchmark detects functional regressions. It is not a performance
baseline. Run real performance tests manually, through `workflow_dispatch` on
self-hosted hardware, or on dedicated infrastructure. A future scheduled
Raspberry Pi 3 B+ `medium` run must remain optional and must not block pull
request merges.

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

The JSON report has `schemaVersion: 3`. Its schema is
`benchmarks/schema/result-v3.json`. Version 2 reports remain readable and are
migrated in memory. The report includes:

- a UUID identifying the run;
- benchmark, server, API, event API, and station-driver versions;
- process start, measurement start, and end timestamps;
- warm-up and measured durations;
- the full effective profile and profile/fixture SHA-256 hashes;
- seed and client host metadata;
- requested and achieved rates, counts, successes, expected and unexpected
  errors, timeouts, skipped schedules, and latency percentiles per operation;
- WebSocket connections, reconnects, sequence gaps, snapshots, events, and
  feedback-to-event latency;
- expected and unexpected availability outages, recoveries, and downtime;
- synchronized simulator scenario identity, timestamps, status, and steps;
- invariant observations, informational threshold warnings, and the overall
  `PASS`, `WARN`, or `FAIL` result.

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

Any observed violation or unexpected operation error sets `overallResult` to
`FAIL`, independently of latency. An invariant that cannot be exercised by the
selected fixture is reported as `NOT_OBSERVED` rather than silently claimed as
tested. Expected errors must be declared by a named scenario profile.
The contention profiles declare HTTP 409 as expected and report it separately.

For `medium` and `large`, the initial latency and host targets are informative.
Exceeding them produces `WARN`, never `FAIL`. The targets are command p95 below
50 ms, command p99 below 100 ms, feedback-to-WebSocket p95 below 100 ms, and
zero WebSocket queue overflows, SQLite errors, network drops, swap use, and
thermal-throttling events. They are not API guarantees.
Command latency covers lease acquire/heartbeat/release, throttle, function,
accessory, and route operations. Login and read latency remain visible but do
not use these command targets.

## External metadata and metrics

Normal benchmark execution does not access Prometheus. An external script may
query Prometheus or use system tools, then write a summary matching
`benchmarks/schema/system-metrics-v1.json`. Static DUT and server metadata use
`benchmarks/schema/run-metadata-v1.json`. Complete examples are available at
`benchmarks/examples/run-metadata.json` and
`benchmarks/examples/system-metrics.json`. Unknown fields are rejected.

The external summary uses bytes for sizes, degrees Celsius for temperature,
and percentages for CPU. Process CPU follows `pidstat`: 100 percent is one
fully used logical CPU. Host CPU uses 100 percent for the whole host. Maxima
cover the measured phase. Error, drop, overflow, and throttling fields are
counter deltas over that phase. Capture `databaseInitialBytes` immediately
before warm-up and `databaseFinalBytes` after the measured phase and cleanup.

Attach either document after the benchmark. Omitting `--output` replaces the
input report atomically:

```bash
trainpilot-bench enrich-report benchmarks/results/run.json \
  --metadata /tmp/run-metadata.json \
  --metrics /tmp/system-metrics.json
```

Configuration keys containing `password`, `secret`, `token`, `credential`,
`apiKey`, `authorization`, or `cookie` are rejected. Values must also be
reviewed before publication. The server URL written by the runner has user
information, query parameters, and fragments removed.

Ordinary local reports may omit external metadata. A published baseline must
pass the stricter validation:

```bash
trainpilot-bench validate-report --publication benchmarks/results/run.json
```

Publication requires run/profile identity, load-generator metadata, hardware
identity, TrainPilot commit, server Go/OS/arch, relevant configuration, the
metrics source, database sizes, CPU/RSS, swap, SQLite errors, network drops,
WebSocket overflows, and thermal-throttling data.

For runs of at least one hour, `analyze-soak` queries the repository's
30-minute Prometheus recording rules at the exact measured boundaries. It
compares the first and last windows, evaluates resource slopes and latency
change, verifies scrape coverage, and writes a secret-free
`soak-analysis-v1` companion:

```bash
trainpilot-bench analyze-soak benchmarks/results/soak-6h-medium.json \
  --prometheus http://127.0.0.1:9090 \
  --instance 192.0.2.10:6060 \
  --scrape-interval 15s \
  --output benchmarks/results/soak-6h-medium-analysis.json
```

The command produces `PASS` or `WARN`; functional failures remain in the
benchmark report. See `docs/BENCHMARK-SOAK-FAULT-RECOVERY.md` for full
procedures, restart gates, evidence, and acceptance checks.

## Compare repeated runs

Each group accepts any number of reports greater than or equal to three. The
tool compares the median p50/p95/p99, achieved throughput, expected and
unexpected errors, WebSocket gaps and overflows, and imported CPU/RAM maxima.
For an even run count, the median is the mean of the two middle values.

```bash
trainpilot-bench compare \
  --baseline baseline-1.json --baseline baseline-2.json --baseline baseline-3.json \
  --candidate candidate-1.json --candidate candidate-2.json --candidate candidate-3.json \
  --output comparison.json
```

Reports inside one group must share the same profile hash and hardware
description, benchmark version, server version/commit, and configuration. A
relative verdict requires matching profile hashes, hardware, and server
configuration between groups. Cross-profile, cross-hardware, or
cross-configuration comparisons remain informative and omit the verdict. On comparable groups, any candidate
functional failure gives `FAIL`; a median p95 increase of at least 10 percent
gives `WARN`. No relative performance threshold currently gives `FAIL`.
