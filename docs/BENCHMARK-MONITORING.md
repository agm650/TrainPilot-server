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
deploy/monitoring/grafana/provisioning/datasources/prometheus.yml
deploy/monitoring/grafana/provisioning/dashboards/trainpilot.yml
deploy/monitoring/grafana/dashboards/trainpilot-overview.json
deploy/monitoring/grafana/dashboards/trainpilot-host.json
deploy/monitoring/grafana/dashboards/trainpilot-benchmark.json
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
provisioning directories. Copy the three JSON dashboards into the path declared
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
errors, feedback, WebSocket delivery, store activity, and SQLite growth.

The current `trainpilot-bench` process does not expose live Prometheus metrics.
Consequently, phase annotations, requested versus achieved client load, and
expected versus unexpected client errors are not fabricated in this dashboard.
They are tracked by `task/09-benchmark-live-metrics-grafana-annotations.md`.

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
