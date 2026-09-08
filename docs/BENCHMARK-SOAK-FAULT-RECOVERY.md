# Soak, fault, and recovery test procedures

These procedures measure long-running stability and recovery under simulator
faults. They do not validate physical command-station timing or railway
hardware. Run Prometheus, Grafana, and `trainpilot-bench` outside the device
under test (DUT).

The benchmark report is authoritative for client-visible errors and safety
invariants. Prometheus recording rules calculate continuous trends. The
`TrainPilot / Soak and Recovery` dashboard is supporting evidence for resource,
latency, station-state, and process behavior.

## Common preparation

1. Build `dccd`, `dccctl`, and `trainpilot-bench` from the same revision.
2. Generate and import the `medium` fixture as described in
   `docs/BENCHMARKING.md`.
3. Configure the DUT with the simulator driver, `testAPI: true`, and private
   diagnostic metrics enabled.
4. Prepare benchmark users and a `0600` credentials file.
5. Configure an existing Prometheus with a 15-second scrape and evaluation
   interval. Load both soak rule files from `deploy/monitoring/prometheus/`.
6. Provision the Grafana dashboards and select the DUT's `instance` label.
7. Verify all targets and rules before the run:

   ```bash
   promtool check config /etc/prometheus/prometheus.yml
   promtool check rules /etc/prometheus/trainpilot-soak-recording-rules.yml
   promtool check rules /etc/prometheus/trainpilot-soak-alerting-rules.yml
   curl --fail http://PROMETHEUS:9090/-/ready
   curl --fail http://DUT:6060/metrics
   curl --fail http://DUT:9100/metrics
   ```

8. Record the build revision, configuration, DUT hardware, cooling, storage,
   Prometheus scrape interval, and wall-clock synchronization status.
9. Confirm there is enough Prometheus retention and disk space for the run.
10. Open the soak dashboard. Confirm RSS, heap, goroutines, file descriptors,
    threads, SQLite files, HTTP latency, availability, and station state are
    populated.

Do not change the fixture, seed, build, monitoring interval, or DUT
configuration during a measurement.

## Six-hour soak

1. Confirm that no soak warning is already active.
2. Start low-rate host diagnostics on the DUT if raw evidence is required:

   ```bash
   pidstat -h -r -u -p "$(pidof dccd)" 15
   iostat -xz 15
   ```

3. Start the benchmark from the load-generator host:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/soak-6h-medium.yaml \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --output results/soak-6h-medium.json
   ```

4. Note the `Measurement started at` timestamp printed after the ten-minute
   warm-up. Do not use the process start time as the Prometheus boundary.
5. During the run, check that the dashboard remains populated and that
   Prometheus has no scrape gap. Do not change load or fault state.
6. When the run ends, set Grafana to the absolute interval from
   `measurementStartedAt` through `endedAt` in the report.
7. Calculate the automated trend result:

   ```bash
   trainpilot-bench analyze-soak results/soak-6h-medium.json \
     --prometheus http://PROMETHEUS:9090 \
     --instance DUT:6060 \
     --scrape-interval 15s \
     --output results/soak-6h-medium-analysis.json
   ```

8. Review the first and last 30-minute averages, hourly slopes, coverage, and
   every active or historical `severity="warning"` alert.
9. If a resource warning is unexplained, capture heap and goroutine profiles
   before restarting the process. Do not diagnose a leak from RSS alone.
10. Accept the run only when the benchmark has zero unexpected errors and
    invariant violations, the analysis coverage is at least 99%, SQLite has no
    unexpected error, latency is stable, and goroutine/FD growth is bounded.

The default warning limits are initial operational values. Override the
`analyze-soak` warning flags and edit the example alert rules together when a
validated platform-specific limit is available.

## Twenty-four-hour soak

1. Repeat the common preparation and six-hour procedure with
   `benchmarks/profiles/soak-24h-medium.yaml`.
2. Keep the 15-second scrape interval unless measured monitoring overhead
   requires a documented alternative.
3. Use:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/soak-24h-medium.yaml \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --output results/soak-24h-medium.json
   ```

4. Run `analyze-soak` against the completed report with the same Prometheus
   instance and scrape interval.
5. In addition to the six-hour checks, inspect timer drift, session and lease
   accumulation, WebSocket sequences, process restarts, and database/WAL
   growth across the full day.

This run is manual or scheduled. It must not block pull requests.

## Command-station degradation and recovery

The scenario follows this measured-phase sequence: nominal, 100 ms operation
delay, 500 ms delay, degraded, offline for one minute, then online and nominal.

1. Reset the simulator and confirm station state is online.
2. Run:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/station-full-recovery-medium.yaml \
     --simulator-scenario benchmarks/scenarios/station-full-recovery.json \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --output results/station-full-recovery.json
   ```

3. The runner loads the scenario at measurement start and advances its logical
   clock at wall-clock speed. It records the actual application time of every
   step in `scenario.steps`.
4. Confirm the expected station transition order in Grafana and the report.
5. Confirm active commands are refused while offline, no unbounded queue or
   resource growth occurs, WebSockets recover, and clients receive a coherent
   snapshot.
6. Confirm `no_implicit_restart`, `server_availability`, and all other observed
   invariants pass. Any error outside its declared phase is unexpected.
7. Compare resource and latency levels before the first fault and after the
   final recovery. The final minute is the nominal recovery window.

## Intermittent fault recovery

This scenario injects every-N command failures, an error burst, accessory
confirmation timeout, wrong confirmation, electrical short circuit, feedback
bounce, and feedback loss.

1. Reset the simulator and confirm no fault remains active.
2. Run:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/intermittent-fault-recovery-medium.yaml \
     --simulator-scenario benchmarks/scenarios/intermittent-fault-recovery.json \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --output results/intermittent-fault-recovery.json
   ```

3. Match each recorded scenario step to command outcomes and station events in
   the same absolute Grafana interval.
4. Confirm periodic faults affect only every configured Nth matching command
   and that `remaining` counts injected failures, not attempted commands.
5. Confirm expected failures are counted separately. Any failure outside the
   time-bounded profile rule remains fatal.
6. Confirm snapshots resolve intentional WebSocket gaps and final accessory,
   feedback, electrical, and station state is coherent.
7. Confirm resources and latency return toward their pre-fault range.

## Controlled server restart

`trainpilot-bench` observes a restart but never performs one. The operator owns
the service action. The explicit outage gate prevents this profile from being
used accidentally.

1. Start the benchmark:

   ```bash
   trainpilot-bench run \
     --server http://DUT:8080 \
     --profile benchmarks/profiles/restart-recovery-medium.yaml \
     --credentials /secure/benchmark-credentials.json \
     --allow-active-commands \
     --allow-simulator-api \
     --allow-planned-outage \
     --output results/controlled-restart.json
   ```

2. Wait for `Measurement started at`. At measured offset 3 minutes, run on the
   DUT:

   ```bash
   sudo systemctl restart dccd.service
   ```

3. Do not perform another restart. The declared outage window is measured
   offsets 3:00 through 4:00.
4. Confirm `availability.expectedOutages` is at least one,
   `availability.unrecoveredOutages` is zero, and the measured downtime agrees
   with Prometheus `up` and systemd restart metrics.
5. Confirm WebSockets reconnect and request a valid snapshot.
6. Confirm no locomotive resumes a non-zero speed without a new explicit
   throttle command. An automatic replay is an invariant failure.
7. Confirm all errors outside the planned minute are unexpected and therefore
   fail the report.

## Abrupt crash recovery

Keep crash testing separate from the controlled restart result.

1. Use the same restart profile and safety gates, but write to a distinct
   report name.
2. At measured offset 3 minutes, terminate only the known `dccd` service
   process using the laboratory's approved crash method. Do not use a broad
   process match.
3. Let the service manager restart it. Record the exact signal, process ID,
   service policy, and timestamps as external evidence.
4. Apply the same availability, reconnection, snapshot, and safe-state checks.
5. Label the retained evidence as an abrupt crash. Never combine its result
   with controlled-restart repetitions.

## Evidence to retain

Keep the benchmark report, soak-analysis companion when applicable, effective
Prometheus configuration and rule files, dashboard export or screenshots,
alert history, build and hardware metadata, and any `pidstat`, `iostat`, systemd,
or pprof evidence. Reports and analyses are secret-free, but credentials and
access tokens must never be copied into the evidence bundle.
