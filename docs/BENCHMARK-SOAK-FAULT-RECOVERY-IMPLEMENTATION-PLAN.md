# Soak, Fault, and Recovery Benchmark Implementation Plan

Status: implemented in the current branch as of 11 September 2026.

The Go suite validates report logic and the structure of the Prometheus and
Grafana assets. A real `promtool` validation, live Grafana import, 6-hour or
24-hour run, and hardware campaign remain external acceptance work. The items
below record the implemented design; they are not evidence that those external
checks have run.

## Decision

Prometheus recording rules will calculate continuous trends. Alerting rules
will convert sustained threshold breaches into `WARN` states. Grafana will
correlate trends with station transitions and recovery events. The benchmark
JSON report remains authoritative for client-side invariants.

## 1. Prometheus rules

- Add `deploy/monitoring/prometheus/trainpilot-soak-recording-rules.yml`.
- Record 30-minute rolling averages and hourly slopes for RSS, Go heap, heap
  objects, goroutines, threads, file descriptors, database, and WAL.
- Record rolling p95/p99 latency, errors, WebSocket failures, and availability.
- Preserve labels only for the instance and existing bounded dimensions.
- Reference the rule file from `prometheus.example.yml`.

## 2. WARN policy

- Add `trainpilot-soak-alerting-rules.yml` with `severity="warning"`.
- Alert on sustained goroutine, thread, and file-descriptor growth.
- Alert on configurable RSS, heap-object, and SQLite slopes.
- Alert on p95 degradation between rolling beginning and end windows.
- Alert on unexpected SQLite errors, WebSocket overflows, and prolonged
  unavailability.
- Document initial thresholds as examples, not contractual limits.
- Test every rule with `promtool`.

## 3. Correlation and Grafana

- Add a `TrainPilot / Soak and Recovery` dashboard.
- Show raw values, rolling averages, slopes, and active `WARN` rules.
- Overlay station `online`, `degraded`, and `offline` state and transition
  counters.
- Show process restarts, uptime, command outcomes, and recovery latency.
- Use an absolute time range derived from the benchmark report.
- Keep exact benchmark phase annotations for ticket 09.

## 4. Automated soak analysis

- Add `trainpilot-bench analyze-soak`.
- Read `startedAt`, `endedAt`, and `duration` from the benchmark report.
- Query Prometheus for the exact measured interval.
- Compare the first and last 30-minute windows.
- Emit a versioned, secret-free soak-analysis JSON companion.
- Include data coverage, deltas, slopes, `WARN` reasons, and source metadata.
- Do not make normal benchmark execution depend on Prometheus.

## 5. Fault orchestration

- Add `--simulator-scenario` to `trainpilot-bench run`.
- Require the simulator, `testAPI`, and `--allow-simulator-api`.
- Start scenarios at measurement start.
- Advance their logical clock according to elapsed wall time.
- Record actual timestamps and outcomes for every injected phase.
- Never accelerate time during soak runs.

## 6. Expected fault handling

- Extend expected-error rules with optional measured-phase time windows.
- Permit expected health failures only inside an explicit planned outage.
- Never suppress errors outside their declared phase.
- Keep all invariant violations fatal.
- Add focused benchmark scenarios for periodic errors, bursts, accessory
  timeout, wrong confirmation, short circuit, feedback bounce, and feedback
  loss.

## 7. Restart recovery

- Add an explicit planned-restart mode. It will not restart `dccd` itself.
- Require a separate confirmation flag.
- Let the operator or systemd perform the controlled restart.
- Measure unavailable duration, client reconnection, and the first valid
  snapshot.
- Verify safe locomotive state and absence of implicit restart.
- Keep abrupt crash testing as a distinct documented procedure.

## 8. English procedures

- Add `docs/BENCHMARK-SOAK-FAULT-RECOVERY.md`.
- Provide step-by-step procedures for 6-hour and 24-hour soaks, station
  degradation, intermittent faults, controlled restart, and abrupt crash.
- Include preflight, commands, phase timing, Prometheus queries, dashboard
  checks, evidence retention, and acceptance checklists.
- Explain that Grafana validates server and system behavior while
  `trainpilot-bench` validates client-visible behavior and invariants.

## 9. Validation

- Unit-test trend calculations, time-window matching, and report schemas.
- Test scenario orchestration with the fake clock and simulator API.
- Validate all profiles and scenario documents.
- Run `promtool check rules` and `promtool check config`.
- Parse every Grafana dashboard as JSON.
- Run focused Go tests, `gofmt`, `git diff --check`, `go test ./...`,
  `CGO_ENABLED=0 go test ./...`, `go test -race`, and `go vet ./...`.
- Do not execute real 6-hour, 24-hour, or hardware runs during automated
  validation.
