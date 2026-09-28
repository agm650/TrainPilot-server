# `small-real` short-run evidence, 2026-09-27

These are draft benchmark artifacts for three Mac mini runs and three Jetson Nano
USB HDD runs. The original JSON reports are in the separate `TrainPilot-server`
checkout under `benchmarks/results/mac-mini/small-real{1,2,3}.json` and
`benchmarks/results/jetson-nano/small-hdd{6,7,8}.json`. The `enriched/` files
are copies; the originals were not changed. All six client reports are `PASS`.
Original report SHA-256 digests are in `prometheus-observations.json`. They
share profile hash `c6c1cf7aa5d1c4918ca3c2f154c44bba77f93b2686dc250df56dbbd71b2a856b`,
fixture hash `3087f0dbebe627e94a3b5df1b99e0a80119c02cce4f89ec76034780aef7fd0e5`,
and seed 650. The user confirmed that both servers ran the binaries built from
commit `8ad2755dcc35f389aeaf036179a52020390c48c7` with Go 1.26.8 and
`CGO_ENABLED=0`.

## Run measurements

Prometheus was queried at `http://192.168.0.50:9090` for each report's exact
`measurementStartedAt` to `endedAt` interval. The actual scrape interval was
**2 seconds**, verified from the Prometheus configuration and raw sample
timestamps. Each run has 300 server and 300 node-exporter scrapes in its
10-minute measured interval. Full values and run IDs are in
`prometheus-observations.json` and the six `*-system-metrics.json` files.

| Run | DUT | Process CPU p95, % of one core | Process CPU max, % | RSS max, MiB | `sda` busy average, % | CPU temperature max, °C |
|---|---|---:|---:|---:|---:|---:|
| `small-real1` | Mac mini SSD | 2.191 | 2.207 | 36.28 | 0.282 | 56 |
| `small-real2` | Mac mini SSD | 2.242 | 2.259 | 38.24 | 0.279 | 55 |
| `small-real3` | Mac mini SSD | 2.293 | 2.293 | 38.98 | 0.263 | 57 |
| `small-hdd6` | Jetson USB HDD | 3.732 | 4.000 | 35.95 | 37.286 | N/A |
| `small-hdd7` | Jetson USB HDD | 4.569 | 4.569 | 36.59 | 36.453 | N/A |
| `small-hdd8` | Jetson USB HDD | 3.712 | 3.827 | 37.16 | 36.809 | N/A |

The CPU values use a one-minute `rate(process_cpu_seconds_total)` sampled
every 15 seconds, with `quantile_over_time(0.95, ...)` and `max_over_time(...)`
over the measured phase. Disk busy is the mean of one-minute
`rate(node_disk_io_time_seconds_total{device="sda"})`, also sampled every
15 seconds. RSS uses the maximum observed `process_resident_memory_bytes`.
The remaining maxima and counter deltas use the same measured boundaries.
For example, the CPU p95 query evaluated at each report's `endedAt` is:

```promql
quantile_over_time(0.95,
  (100 * rate(process_cpu_seconds_total{job="SERVER_JOB"}[1m]))[600s:15s])
```

Replace `SERVER_JOB` with the job recorded for that run in
`prometheus-observations.json`. The same file retains exact UTC boundaries.
Prometheus is the live source; a raw time-series archive was not exported.

`databaseInitialBytes` is the `file="database"` Prometheus gauge sampled just
before warm-up; `databaseFinalBytes` is sampled 20 seconds after the run.
These are monitored file sizes, not direct filesystem snapshots. The WAL is
tracked separately in `prometheus-observations.json`. The Mac mini database
was 9,138,176 bytes before each run; its WAL was 1,788,112 bytes before the
first run and 4,120,032 bytes thereafter. The Jetson database was 1,871,872
bytes, with a 0-byte WAL at these sample points. These initial database states
differ materially despite matching fixture hashes, so a direct hardware-only
performance attribution is not justified.

### SQLite state audit after the runs

A read-only audit of the live databases on 2026-09-27 found 4,096-byte pages,
`auto_vacuum=0`, and WAL mode on both hosts. The Mac mini had 2,231 pages,
including 646 free pages (2,646,016 bytes, 28.96%). The Jetson had 457 pages,
including 7 free pages (28,672 bytes, 1.53%). Current row counts differed in
`sessions` (7,523 versus 1,983) and `control_leases` (9 versus 3); all other
application-table counts matched. This audit was performed after the runs and
does not establish the row counts at each earlier measurement boundary.

`VACUUM` could reclaim the Mac mini's free pages, but it would not remove live
session or lease rows. The 1,585 non-free Mac pages alone occupy 6,492,160
bytes, versus 1,843,200 bytes for the Jetson's 450 non-free pages. A fresh
comparison should start both hosts from equivalent database contents and then
rerun the benchmark; vacuuming the current Mac database cannot change the
recorded six runs.

All measured swap, network drops and errors on the benchmark interface, and
WebSocket queue overflow deltas were zero. No SQLite error-labelled operation
or transaction series appeared while store activity and scrape coverage were
present; the external summaries record zero SQLite errors on that basis.

The Mac mini's CPU temperature is from node-exporter's `platform_coretemp_0`
sensors. Its `node_cpu_core_throttles_total` delta was zero. The Jetson only
exposed `thermal_fan_est` through node-exporter during these runs; this is not
a CPU temperature or a thermal-throttling counter. Jetson temperature and
`thermalThrottlingEvents` are therefore omitted from its external summaries.

## Server and host configuration

| Setting | Mac mini | Jetson Nano |
|---|---|---|
| Hardware | Macmini3,1; Core 2 Duo P7350; 2 logical cores; 3,844,476,928 B RAM | Developer Kit; Cortex-A57; 4 logical cores; 4,156,489,728 B RAM |
| OS | Debian 13.7, kernel `6.12.107+deb13-amd64`, amd64 | Ubuntu 20.04.6, kernel `4.9.337-tegra`, arm64 |
| SQLite storage | Fanxiang S101 256 GB SATA SSD, LVM/ext4 at `/` | ST1000LM035-1RK172 1 TB USB HDD, `/dev/sda1` ext4 at `/opt/TrainPilot` |
| Network to load generator `192.168.0.50` | `enp0s10`, 192.168.0.60, 100 Mb/s full duplex | `eth0`, 192.168.0.54, 100 Mb/s full duplex |
| Prometheus jobs | `trainpilot`, `node` | `trainpilot-jetson`, `node-jetson` |

Both live processes ran `./bin/dccd serve --config ./config.json` from their
respective deployment directories. A read-only, selected-field inspection
found the same effective configuration: HTTP `:8080`, SQLite
`./dcc-control.db` in WAL mode, simulator driver, diagnostics and metrics on
`:6060`, `pprof` enabled, test API enabled, demo seed enabled, lease TTL 10 min,
turnout confirmation timeout 2 s. The per-host metadata JSON files list all
captured settings. The SQLite journal mode was also confirmed by a read-only
`PRAGMA journal_mode` query on each database. The Mac configuration file mtime
was 2026-09-27 01:14:19 UTC; the Jetson's was 2026-09-25 22:17:05 UTC. Both
precede their first run. The settings were read after the runs, so this is
configuration evidence with a pre-run modification time, not an archived
snapshot taken during each run.
The currently running Mac process started at 03:14:53 CEST (01:14:53 UTC) on
September 27. The Jetson process started at 17:55:39 CEST (15:55:39 UTC) on
September 26. Both configuration mtimes precede these process starts, and both
process starts precede their first recorded benchmark run.

## Publication status

`validate-report --publication` accepts the three Mac mini enriched JSONs.
It rejects each Jetson enriched JSON because
`systemMetrics.thermalThrottlingEvents` was unavailable. The validator cannot
check the separate six-hour soak, exact cooling and power-supply details, or
whether the Prometheus file-size samples meet a direct pre-run filesystem
snapshot requirement. No hardware configuration is validated by these short
runs alone. For a new Jetson soak, retain CPU temperature and throttling or
power-limit evidence from a dedicated collector throughout the run.
