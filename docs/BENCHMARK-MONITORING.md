# Benchmark monitoring

This guide configures an existing Prometheus and Grafana installation to
observe a TrainPilot benchmark. The repository does not install or package
Prometheus, Grafana, or node_exporter.

Run Prometheus and Grafana outside the device under test (DUT). Run
`trainpilot-bench` outside the DUT as well when measuring capacity. Prometheus,
Grafana, and the load generator may share the monitoring host for a small lab.

## Compatibility

The dashboards use the classic Grafana JSON model, dashboard schema version 39,
and the version 1 file provisioning format. They target Grafana 10.4 through
13.x and use only the built-in Prometheus data source. Older Grafana versions
have not been validated. The JSON is checked structurally in this repository;
import into a live Grafana instance remains an external validation.

The Prometheus example uses stable `scrape_config` syntax supported by
Prometheus 2.45 and 3.x. node_exporter 1.6 or newer is recommended. Exact
package versions remain the responsibility of the monitoring operator.

Required software is:

- an existing Prometheus and Grafana installation on the monitoring host;
- `dccd` and node_exporter on the DUT;
- `sysstat` on the DUT for `pidstat`, `iostat`, and `sar` diagnostics;
- `smartmontools` when the DUT storage supports SMART;
- `vcgencmd` on Raspberry Pi, or readable Linux thermal sensors on other DUTs.

## Repository assets

```text
deploy/monitoring/prometheus/prometheus.example.yml
deploy/monitoring/prometheus/trainpilot-soak-recording-rules.yml
deploy/monitoring/prometheus/trainpilot-soak-alerting-rules.yml
deploy/monitoring/grafana/provisioning/datasources/prometheus.yml
deploy/monitoring/grafana/provisioning/dashboards/trainpilot.yml
deploy/monitoring/grafana/dashboards/trainpilot-overview.json
deploy/monitoring/grafana/dashboards/trainpilot-host.json
deploy/monitoring/grafana/dashboards/trainpilot-benchmark.json
deploy/monitoring/grafana/dashboards/trainpilot-soak-recovery.json
deploy/monitoring/node-exporter/trainpilot-*-textfile.sh
deploy/monitoring/systemd/trainpilot-textfile@.service
deploy/monitoring/systemd/trainpilot-textfile@.timer
```

## Expose TrainPilot metrics

Metrics are disabled with the diagnostic listener by default. Enable them in
the DUT configuration and bind the listener to a private DUT address:

```json
{
  "diagnostics": {
    "enabled": true,
    "listen": "192.0.2.10:6060",
    "metrics": true,
    "pprof": false
  }
}
```

The diagnostic listener has no application authentication. Permit TCP port
6060 only from the monitoring host. Never expose it directly to the Internet.
An SSH tunnel is an alternative when a private monitoring network is not
available.

Check both DUT endpoints from the Prometheus host:

```bash
curl --fail http://192.0.2.10:6060/metrics
curl --fail http://192.0.2.10:9100/metrics
```

## Expose load-generator metrics

`trainpilot-bench` does not open a metrics listener by default. Pass an explicit
address to expose its isolated registry:

```bash
trainpilot-bench run \
  --profile benchmarks/profiles/medium.yaml \
  --credentials /run/secrets/benchmark-credentials.json \
  --output benchmarks/results/medium.json \
  --metrics-listen 192.0.2.20:6061
```

The listener serves only `/metrics`. It has no authentication. Bind it to a
private address, restrict port `6061` to the Prometheus host, and never expose
it directly to the Internet. Use `127.0.0.1:6061` when Prometheus runs on the
load-generator host. On completion, the command keeps the listener available
for three seconds so a two-second scrape can collect the terminal phases.

The example Prometheus configuration contains a separate `trainpilot-bench`
job. Replace `192.0.2.20:6061` with the load generator address, then check it
from the Prometheus host:

```bash
curl --fail http://192.0.2.20:6061/metrics
```

The live registry exposes:

- one-hot `trainpilot_benchmark_phase` and bounded phase transitions for
  `setup`, `warmup`, `measurement`, `stopping`, `cleanup`, and `finished`;
- the requested rate and requested burst count for each bounded operation;
- executed and skipped operations by phase, with `success`, `expected_error`,
  and `unexpected_error` results and operation latency histograms;
- client-observed WebSocket connections, events, gaps, snapshots,
  resynchronizations, invalid messages, and feedback latency.

Live operation series cover every phase. The versioned JSON report retains its
existing contract, including measurement-only operation summaries. A
benchmark client cannot identify why the server closed a WebSocket. Therefore,
the dashboard uses the existing server-side
`trainpilot_websocket_queue_overflows_total` metric for overflow evidence and
does not infer client-side overflows.

## Configure node_exporter

Use the node_exporter service supplied by the DUT operating system. The Host
dashboard expects the default Linux collectors. Add these flags when textfile
and `dccd.service` restart metrics are required:

```text
--collector.textfile.directory=/var/lib/node_exporter/textfile_collector
--collector.systemd
--collector.systemd.unit-include=dccd.service
--collector.systemd.enable-restarts-metrics
```

The systemd collector and restart metric are optional. A missing optional
collector produces an empty panel rather than invalid data.

### Platform textfile collectors

The scripts accept the destination `.prom` file as their first argument. They
write a temporary file in the destination directory and rename it atomically.

- `trainpilot-rpi-textfile.sh` uses `vcgencmd`. It exports CPU temperature, ARM
  frequency, the raw throttling mask, and current and historical throttling,
  under-voltage, frequency-cap, and soft-temperature-limit flags.
- `trainpilot-jetson-textfile.sh` reads Linux thermal zones and CPU frequency
  policies. Keep `tegrastats` as an additional interactive diagnostic tool.
- `trainpilot-mac-linux-textfile.sh` reads Linux `hwmon` temperature sensors.
  node_exporter may already expose the same sensors when its `hwmon` collector
  supports the machine.

Install only the script matching the DUT. The systemd unit is a template. The
instance name selects the script name:

```bash
sudo install -d -m 0755 /usr/local/libexec
sudo install -m 0755 deploy/monitoring/node-exporter/trainpilot-rpi-textfile.sh \
  /usr/local/libexec/trainpilot-rpi-textfile.sh
sudo install -m 0644 deploy/monitoring/systemd/trainpilot-textfile@.service \
  /etc/systemd/system/trainpilot-textfile@.service
sudo install -m 0644 deploy/monitoring/systemd/trainpilot-textfile@.timer \
  /etc/systemd/system/trainpilot-textfile@.timer
sudo systemctl daemon-reload
sudo systemctl enable --now trainpilot-textfile@rpi.timer
```

Use `jetson` or `mac-linux` instead of `rpi` for the other scripts. The default
timer interval is 10 seconds. Override `OnUnitActiveSec` to 5 seconds for a
short thermal investigation or to 15 seconds for a soak test.

Validate the generated metrics before enabling Prometheus scraping:

```bash
systemctl status trainpilot-textfile@rpi.service
cat /var/lib/node_exporter/textfile_collector/trainpilot-rpi.prom
curl --silent http://127.0.0.1:9100/metrics | grep '^trainpilot_'
```

`smartctl`, `sensors`, and `tegrastats` remain diagnostic tools. The supplied
dashboards do not claim SMART or Jetson throttling metrics that the repository
does not currently export.

Useful platform checks are:

```bash
# Raspberry Pi
vcgencmd measure_temp
vcgencmd measure_clock arm
vcgencmd get_throttled

# Jetson Nano
tegrastats

# Mac mini running Linux
sensors
sudo smartctl -a /dev/sda
```

Replace `/dev/sda` with the actual SSD device. Do not poll SMART at a high
frequency during a capacity run.

## Configure Prometheus

Copy `deploy/monitoring/prometheus/prometheus.example.yml` into the existing
Prometheus configuration directory. Replace both `192.0.2.10` targets with the
DUT address, then reload Prometheus.

Copy the two soak rule files beside that configuration. The example references
their relative filenames through `rule_files`. Validate the installed paths
before reloading:

```bash
promtool check rules /etc/prometheus/trainpilot-soak-recording-rules.yml
promtool check rules /etc/prometheus/trainpilot-soak-alerting-rules.yml
promtool check config /etc/prometheus/prometheus.yml
```

The recording rules calculate 30-minute averages, hourly linear slopes,
latency percentiles, error increases, and five-minute availability. The alert
rules expose sustained threshold breaches through Prometheus `ALERTS` series
with `severity="warning"`. Their limits are editable starting values, not
product guarantees. Keep alert limits aligned with the corresponding
`trainpilot-bench analyze-soak --warn-*` flags.

The example is the short-run profile:

```text
scrape_interval: 2s
scrape_timeout: 1s
```

For 6-hour or 24-hour soak tests, use an explicit copy with:

```text
scrape_interval: 15s
scrape_timeout: 5s
evaluation_interval: 15s
```

Also set the provisioned Grafana data source `jsonData.timeInterval` to the
same 15-second value. This keeps `$__rate_interval` large enough for reliable
rate queries.

Keep the selected configuration with the benchmark report. Do not change the
interval during a run. Prometheus and node_exporter expose their own collection
cost through `scrape_duration_seconds`, `scrape_samples_scraped`, and
`process_cpu_seconds_total`.

## Provision Grafana

The provisioning example uses the stable data source UID
`trainpilot-prometheus`. Adjust its URL for the existing Prometheus server:

```yaml
url: http://127.0.0.1:9090
```

Copy the data source and dashboard provider YAML files into Grafana's
provisioning directories. Copy the four JSON dashboards into the path declared
by the provider, `/var/lib/grafana/dashboards/trainpilot` by default. Restart or
reload the existing Grafana instance according to its deployment method.

The dashboards assume the Prometheus job names from the example:

- `trainpilot` for the `dccd` diagnostic listener;
- `node` for node_exporter.

The instance selectors at the top of each dashboard allow several DUTs to use
the same Prometheus server.

### Dashboard coverage

`TrainPilot / Overview` shows `dccd` CPU and RSS, Go runtime, HTTP traffic and
latency, WebSocket activity, feedback processing, leases, command-station
state, errors, and SQLite file sizes.

`TrainPilot / Host` shows per-core CPU, load, memory, swap, disk I/O, network,
filesystem capacity, temperatures, frequency, Raspberry Pi flags, `dccd` file
descriptors, and optional systemd restart data.

`TrainPilot / Benchmark` shows server-side throughput, latency percentiles,
errors, feedback, WebSocket delivery, store activity, and SQLite growth. When
the `trainpilot-bench` scrape job is enabled, it also shows all generator
phases, requested versus executed load, skipped operations, outcome rates,
client latency, and WebSocket resynchronizations. Dashboard annotations mark
each observed phase transition.

`TrainPilot / Soak and Recovery` shows 30-minute rolling averages, resource
slopes, rule-generated warnings, station transitions, availability, and process
restart evidence. Select the exact absolute measurement interval from the
benchmark report before interpreting the panels.

If the optional generator listener is disabled, the generator panels and phase
annotations remain empty. Server and host panels continue to work.

### Diagnose turnout command latency

The private `dccd` `/metrics` endpoint exposes
`trainpilot_turnout_command_duration_seconds{result}` for the complete
logical command and `trainpilot_turnout_command_phase_duration_seconds{phase}`
for bounded phases. `lock_wait` covers per-turnout serialization; `prepare`
covers validation and other work before or between station commands; `station`
covers driver calls; `confirmation` covers the wait for reported state,
including its store reads; and `finalize` covers terminal persistence and
state publication.
No-op or rejected commands do not necessarily visit every phase. Multi-step
turnouts may emit several observations for one phase. Neither metric exposes
turnout IDs. Buckets span 0.5 ms to 8.192 s, plus `+Inf`; there is no new
configuration setting. Phase percentiles cannot be added to reconstruct a
request percentile.

For a one-minute p95 trend by phase, use:

```promql
1000 * histogram_quantile(0.95,
  sum by (le, phase) (
    rate(trainpilot_turnout_command_phase_duration_seconds_bucket{job="trainpilot"}[1m])
  ))
```

This is in milliseconds. Compare it with the total command histogram, the
HTTP route histogram, and `trainpilot_station_command_duration_seconds` over
the report's measured interval. Prometheus histogram quantiles are bucket
estimates, unlike the exact client percentiles in the benchmark report.

`trainpilot_turnout_confirmation_detail_duration_seconds{stage}` separates
the confirmation path without changing command behavior. `event_delivery`
measures from a station event's `ObservedAt` to handler entry, when the
timestamp is present and not in the future; it is not physical actuation
latency, and synthetic simulator clocks may make it unavailable or
incomparable. `event_handler` covers an accessory event from dequeue through
notification; `event_lookup` covers its address-to-turnout lookup and store
reads; `event_persist` covers the observation write; and `event_publish`
covers the subsequent state read and event publication. On the command side,
`wait_read` covers each store read
used to verify the reported position, and `wait_update` covers each wait for
an update, cancellation, or timeout. Event stages can also include unsolicited
accessory reports. `wait_update` overlaps event-handler work and scheduling;
these percentiles must not be added together. Labels are limited to these seven
stages plus `other`, with the same buckets as the command phase histogram.
There is no new configuration setting.

```promql
1000 * histogram_quantile(0.95,
  sum by (le, stage) (
    rate(trainpilot_turnout_confirmation_detail_duration_seconds_bucket{job="trainpilot"}[1m])
  ))
```

Compare this one-minute trend with the existing `get_turnout` and
`update_turnout` store-operation histograms and SQLite connection-wait rate.
Correlated peaks are diagnostic clues, not proof of causality.
The confirmation waiter and state-event publisher now use the single-row
`get_turnout_state` store operation. The accessory-event lookup still uses
`get_turnout` to load and validate the complete turnout definition. Compare
`wait_read`, `event_publish`, and SQLite wait metrics before and after this
change; do not compare the two store-operation counts as if they represented
the same work. The SQLite pool remains limited to one connection.

After a soak, query the recorded series over the report's exact measured
interval and create the versioned trend companion:

```bash
trainpilot-bench analyze-soak results/soak-6h-medium.json \
  --prometheus http://127.0.0.1:9090 \
  --instance 192.0.2.10:6060 \
  --scrape-interval 15s \
  --output results/soak-6h-medium-analysis.json
```

Grafana helps validate and explain behavior. The benchmark report remains the
authority for expected-error windows and safety invariants. See
`docs/BENCHMARK-SOAK-FAULT-RECOVERY.md` for the complete procedures.

## Measure monitoring overhead

The goal is to estimate the effect of TrainPilot metrics, node_exporter, and
Prometheus scrapes on the DUT. Grafana remains external and does not contribute
meaningful DUT load.

Use one DUT, one TrainPilot build, one fixture, one profile, and one seed. Keep
power, cooling, storage, network, and background services unchanged.

1. Run at least three baseline repetitions with `diagnostics.enabled=false`,
   node_exporter stopped, and no Prometheus target for the DUT.
2. Run at least three monitored repetitions with TrainPilot metrics enabled,
   node_exporter running, and Prometheus scraping both targets at the selected
   interval.
3. Alternate baseline and monitored runs when possible. Allow the DUT to return
   to the same idle temperature before each run.
4. Collect `pidstat` for `dccd` in both variants. This identical low-rate probe
   provides comparable CPU and RSS data when Prometheus is intentionally absent
   from the baseline.
5. Compare the median of the repetitions. Record the raw benchmark reports,
   `pidstat` output, scrape configuration, and thermal or throttling state.

Suggested commands for the common probe are:

```bash
pidstat -h -r -u -p "$(pidof dccd)" 1
iostat -xz 1
vmstat 1
sar -n DEV 1
ss -s
free -m
```

Compare at least:

- achieved throughput and skipped schedules;
- HTTP and feedback p50, p95, p99, and maximum latency;
- unexpected errors and WebSocket overflows;
- median and peak `dccd` CPU and RSS;
- node_exporter CPU and RSS in the monitored runs;
- `scrape_duration_seconds` and scrape failures;
- temperature, frequency, and throttling state when available.

Calculate a relative change as `(monitored - baseline) / baseline * 100` using
the medians. Treat a change as monitoring overhead only when it is repeatable
and larger than normal baseline variation. Report absolute values with the
percentage because small baselines can produce misleading percentages. Do not
define a universal PASS/FAIL threshold from one machine or one run.

No hardware overhead values are included in the repository until they have
been measured. Simulator and development-host checks do not validate thermal,
power, storage, or capacity behavior on another DUT.

## Troubleshooting

- `up{job="trainpilot"} == 0`: check the diagnostic bind address, firewall, and
  `diagnostics.metrics` setting.
- `up{job="node"} == 0`: check node_exporter and TCP port 9100.
- Empty platform panels: check the textfile path, service status, script
  prerequisites, and `node_textfile_scrape_error`.
- Empty systemd panels: enable the systemd collector and restart-count flag, or
  accept that these optional metrics are unavailable.
- Disk latency differs from `iostat`: Prometheus panels derive averages from
  cumulative kernel counters. Preserve `iostat -xz` output for diagnosis.
