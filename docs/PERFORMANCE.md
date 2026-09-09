# TrainPilot performance methodology and validated hardware

This document defines how to reproduce a TrainPilot performance benchmark and
when a hardware and load-profile combination may be published as validated.
It measures the computer running `dccd`, not a physical DCC command station.
Unless a result explicitly says otherwise, the server uses the simulator.

No theoretical maximum number of locomotives is inferred from these results.
A result applies only to its recorded TrainPilot revision, profile, fixture,
configuration, and hardware.

## Test-bench architecture

Use three logical roles:

```text
load-generator host                 device under test (DUT)
trainpilot-bench  -- HTTP/WS ---->  dccd + SQLite + simulator
        |                                      |
        +---- report JSON                      +---- private metrics
                                                       |
monitoring host  <------ Prometheus scrapes -----------+
Prometheus + Grafana + retained host diagnostics
```

- The DUT runs only `dccd`, SQLite, node_exporter, and the selected platform
  collector. Do not run the load generator, Prometheus, or Grafana on it.
- The load-generator host runs `trainpilot-bench` and stores its JSON report.
- The monitoring host runs an existing Prometheus and Grafana installation.
  It may also run the load generator in a small laboratory.
- Use a wired, isolated network. Synchronize the hosts' clocks before a run.

Keep these roles, software versions, scrape intervals, and network paths
unchanged across runs that will be compared.

## Prerequisites

1. Check out the exact TrainPilot commit to test on both build hosts.
2. Build `dccd`, `dccctl`, and `trainpilot-bench` from that revision:

   ```bash
   CGO_ENABLED=0 go build -o dccd ./cmd/dccd
   CGO_ENABLED=0 go build -o dccctl ./cmd/dccctl
   CGO_ENABLED=0 go build -o trainpilot-bench ./cmd/trainpilot-bench
   ```

3. Configure the DUT with the simulator driver, `testAPI: true`, WAL-backed
   SQLite, and private diagnostics. Start it with
   `dccd serve --config config.json`.
4. Configure existing Prometheus, Grafana, and node_exporter installations as
   described in [BENCHMARK-MONITORING.md](BENCHMARK-MONITORING.md).
5. Prepare benchmark users and a credentials file outside the repository as
   described in [BENCHMARKING.md](BENCHMARKING.md). Never publish credentials.
6. Confirm that the load generator can reach the public API and that only the
   monitoring host can reach the DUT diagnostic and node_exporter listeners.
7. Record the idle temperature and ensure that no monitoring target is down.

Release binaries may be used instead of local builds. Record their version and
full commit. Do not compare locally modified binaries with published builds.

## Profiles and fixtures

Capacity profiles and deterministic fixtures are under `benchmarks/profiles/`
and `benchmarks/fixtures/`. Their detailed load composition is documented in
`benchmarks/README.md`.

| Profile | Intended use | Warm-up and measured duration |
|---|---|---|
| `idle` | Server resource baseline | 2 min + 10 min |
| `small` | Typical domestic layout | 2 min + 10 min |
| `medium` | Raspberry Pi 3 B+ target | 2 min + 10 min |
| `large` | Large club layout | 2 min + 10 min |
| `xlarge` | Deliberate limit search | 2 min + 10 min |
| `soak-6h-medium` | Required long-run validation | 10 min + 6 h |
| `soak-24h-medium` | Optional extended stability run | 10 min + 24 h |

Profiles are versioned YAML. The report records the complete effective profile,
its SHA-256 hash, the fixture hash, and the seed. A changed duration, seed,
rate, fixture, or profile file creates a different test condition and must not
be silently grouped with the original profile.

Generate and import the matching fixture before measurement. Fixture generation
and imports are not part of the measured phase. Validate the profile against
the fixture before running it:

```bash
trainpilot-bench validate-profile benchmarks/profiles/medium.yaml
```

See [BENCHMARKING.md](BENCHMARKING.md) for fixture generation and import
commands.

## Run procedure

1. Stop unrelated DUT services. Record every service intentionally left active.
2. Restore the same database, configuration, power mode, cooling, and network
   setup used by the other repetitions.
3. Let the DUT return to its recorded idle-temperature range.
4. Record DUT metadata and the database size immediately before warm-up.
5. Start the load generator from its own host:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/medium.yaml \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --output benchmarks/results/medium-run-01.json
   ```

6. Do not interact with the DUT or change monitoring during measurement.
7. Use `measurementStartedAt` and `endedAt` from the report as the exact query
   boundaries. Warm-up data is not part of published percentiles.
8. Record final database size, system metrics, maximum temperature, network
   drops, and throttling state. Enrich and validate the report:

   ```bash
   trainpilot-bench enrich-report benchmarks/results/medium-run-01.json \
     --metadata /secure/run-metadata.json \
     --metrics /secure/system-metrics.json

   trainpilot-bench validate-report --publication \
     benchmarks/results/medium-run-01.json
   ```

Active profiles must never target a physical command station unless the test
owner deliberately adds `--allow-real-hardware` and records that scope. Such a
run validates a different setup and must not be mixed with simulator results.

## Collected metrics

The benchmark report is authoritative for client-visible behavior:

- requested and achieved operation rates;
- successes, expected and unexpected errors, timeouts, and skipped schedules;
- per-operation HTTP p50, p90, p95, p99, and maximum latency;
- WebSocket connections, reconnects, gaps, overflows, and snapshots;
- feedback-to-WebSocket latency;
- availability, recovery, and safety-invariant observations.

External monitoring supplies process and host CPU, RSS and RAM, Go runtime
resources, goroutines, file descriptors, SQLite/WAL sizes and errors, disk and
network activity, swap, temperature, frequency, and throttling data.

### Percentile definitions

For a sorted set of observed latencies, p95 is the value at the nearest-rank
95th percentile: at least 95 percent of samples are less than or equal to it.
The runner retains measured samples and calculates exact per-run percentiles.
It does not derive percentiles from averages.

The hardware matrix uses these definitions:

- **CPU p95** is the 95th percentile of the DUT `dccd` process CPU samples over
  the measured phase. `100%` means one fully used logical CPU, so a multi-core
  process may exceed `100%`. With 15-second samples and a one-minute rate
  window, discard the first measured minute so every rate window is entirely
  inside the measured phase. Evaluate this PromQL query at `endedAt`, replacing
  `SAMPLE_RANGE` with the measured duration minus one minute and replacing the
  instance label:

  ```promql
  quantile_over_time(0.95,
    (100 * rate(process_cpu_seconds_total{job="trainpilot",instance="DUT:6060"}[1m]))
    [SAMPLE_RANGE:15s])
  ```

- **RAM max** is the maximum `process_resident_memory_bytes` value during the
  measured phase.
- **HTTP p95** is the highest client-observed p95 among HTTP operations with at
  least one measured sample. The result summary must name that operation.
- **Feedback-to-WS p95** is
  `webSocket.feedbackLatency.p95Milliseconds` in the report.

The version 1 external system-metrics document stores CPU maximum, not CPU p95.
Until CPU p95 is added to a versioned report schema, preserve the Prometheus
query, its exact boundaries, scrape interval, result, and source archive in the
Markdown summary. Display `N/A` when a reliable CPU p95 cannot be calculated;
never substitute an estimate or silently use CPU maximum.

## PASS, WARN, and FAIL

- `FAIL` means an unexpected operation error, invariant violation, unrecovered
  availability failure, invalid response/event, crash, or failed final health
  check occurred. A failed run cannot validate hardware.
- `WARN` means functional checks passed but an informational performance,
  resource, coverage, or stability threshold was exceeded. Investigate and
  repeat it. A warned run cannot validate hardware.
- `PASS` means neither a failure nor a warning was reported by the applicable
  benchmark and soak analyses.

The initial command p95 below 50 ms, command p99 below 100 ms, and
feedback-to-WebSocket p95 below 100 ms targets are operational starting points,
not API guarantees. Do not turn one platform's result into a universal product
limit.

## Repetition and comparison policy

A short-run baseline contains at least three completed, coherent repetitions.
Use the same hardware, profile hash, fixture hash, seed, TrainPilot commit,
configuration, monitoring setup, and environmental conditions. Alternate run
order when comparing two variants and use the median across runs. Do not select
the best run.

Compare groups with at least three reports each:

```bash
trainpilot-bench compare \
  --baseline baseline-1.json --baseline baseline-2.json --baseline baseline-3.json \
  --candidate candidate-1.json --candidate candidate-2.json --candidate candidate-3.json \
  --output comparison.json
```

The comparison uses medians. For an even-sized group, the median is the mean of
the two middle values. A candidate functional failure is `FAIL`; a comparable
median p95 regression of at least 10 percent is `WARN`. Current relative
performance thresholds never produce `FAIL`. Cross-profile, cross-hardware, or
cross-configuration comparisons are informational and have no verdict.

## Leak detection

Use the six-hour soak procedure in
[BENCHMARK-SOAK-FAULT-RECOVERY.md](BENCHMARK-SOAK-FAULT-RECOVERY.md). After the
run, execute `trainpilot-bench analyze-soak` over the exact measured interval.
It compares the first and last 30-minute windows and evaluates hourly slopes,
latency change, alerts, and scrape coverage.

Inspect RSS together with Go heap and objects, goroutines, file descriptors,
threads, SQLite/WAL size, and latency. RSS growth alone does not prove a leak.
If unexplained growth remains, capture heap and goroutine profiles before
restarting `dccd`, retain the raw evidence, and mark the result `WARN` or
`FAIL`; do not publish the platform as validated.

## Raspberry Pi 3 B+ procedure

In addition to the common procedure:

1. Use 64-bit Raspberry Pi OS or Debian and record the image, package versions,
   kernel, firmware, and architecture.
2. Record the exact power supply model and rating. Do not power the Pi from an
   unverified USB port.
3. Use wired Ethernet. Record negotiated link speed and the network topology.
4. Record the heatsink, fan, case, ambient temperature, CPU governor, and any
   frequency or thermal configuration.
5. Record whether SQLite is on microSD, USB SSD, or another device. Include the
   storage model, capacity, filesystem, mount options, and free space.
6. List disabled services. At minimum, stop unnecessary desktop, Bluetooth,
   Wi-Fi, indexing, scheduled update, printing, and file-sharing workloads when
   they are not required by the test setup. Keep the system logs needed as
   evidence. Do not disable services between repetitions.
7. Before warm-up, save all of the following:

   ```bash
   date --iso-8601=seconds
   uname -a
   vcgencmd measure_temp
   vcgencmd measure_clock arm
   vcgencmd get_throttled
   systemctl --state=running --type=service
   ```

8. Collect temperature and throttling metrics throughout the run with
   `trainpilot-rpi-textfile.sh`. Record the maximum temperature.
9. Immediately after measurement, repeat `vcgencmd measure_temp`,
   `vcgencmd measure_clock arm`, and `vcgencmd get_throttled`.
10. Decode and retain both `get_throttled` masks. Any unplanned current or
    historical under-voltage, frequency cap, throttling, or soft-temperature
    limit disqualifies the run until its cause is understood and removed.

Allow the Pi to return to the same idle-temperature range before every
repetition and before the six-hour soak.

## Publishing a baseline

Store small, reviewable evidence under a platform slug:

```text
benchmarks/baselines/
  raspberry-pi-3bplus/
  jetson-nano/
  macmini3-1/
```

Create a subdirectory per TrainPilot commit and profile hash when results exist.
Keep the enriched JSON reports, comparison JSON, soak-analysis JSON, and a
Markdown summary. Optional Grafana screenshots are acceptable. Do not commit
Prometheus databases, long raw time series, credentials, tokens, production
databases, or user data. Put large raw evidence in controlled external storage
and record its immutable path or link and checksum.

Every published result must identify:

- exact hardware model, CPU, logical core count, RAM, and storage;
- OS, kernel, architecture, and network setup;
- full TrainPilot commit and displayed version;
- profile name, profile hash/version, fixture hash, seed, warm-up, and duration;
- effective server configuration and observability configuration;
- load-generator identity and software version;
- temperature maximum and throttling/under-voltage state on embedded systems;
- all run IDs, commands, timestamps, verdicts, and retained evidence locations.

Run `validate-report --publication` on every JSON report before committing it.
Review the Markdown summary separately because CPU p95 and external archive
references are not currently part of that validation.

## Definition of validated hardware

A hardware configuration is validated for one profile only when:

1. at least three coherent short runs produce `PASS`;
2. the six-hour soak for the target load produces `PASS` in both its benchmark
   report and soak analysis;
3. no unexplained throttling or under-voltage occurs;
4. no business or safety invariant is violated;
5. the documented latency targets are met; and
6. the tested TrainPilot commit and all required metadata are published.

The claim must state the measured load. For example: “TrainPilot revision X was
validated on hardware Y with profile `medium`, containing 250 registered
locomotives, 10 active locomotives, 15 users, and 20 feedback operations per
second.” Never rewrite this as a maximum supported locomotive count.

## Validated hardware matrix

No hardware configuration has been validated and published yet. The table is
intentionally empty; values will be added only from accepted evidence.

For each row, report the median of the qualifying short runs for CPU p95, RAM
maximum, HTTP p95, and feedback-to-WebSocket p95. Retain every individual value
in the linked summary. Do not use the best run or mix the six-hour soak values
into these medians; identify the separate soak evidence in `Notes`.

| Hardware | OS | TrainPilot | Profile | Result | CPU p95 | RAM max | HTTP p95 | Feedback-to-WS p95 | Notes |
|---|---|---|---|---|---:|---:|---:|---:|---|

## Known limits

- No physical DUT measurements are currently published.
- CPU p95 is external evidence until the result schema supports it directly.
- The simulator exercises server load and safety behavior but does not validate
  command-station latency, decoder behavior, electrical load, or a railway.
- Network and operating-system scheduling remain non-deterministic even with a
  fixed profile seed.
- Prometheus histogram percentiles are estimates; client report percentiles are
  exact for retained samples and may legitimately differ.
- Initial performance thresholds are provisional and do not constitute a
  service-level objective or compatibility guarantee.
