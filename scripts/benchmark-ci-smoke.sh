#!/usr/bin/env bash
set -Eeuo pipefail

dccd_bin=${DCCD_BIN:-bin/dccd}
dccctl_bin=${DCCCTL_BIN:-bin/dccctl}
benchmark_bin=${TRAINPILOT_BENCH_BIN:-bin/trainpilot-bench}
profile_path=${CI_SMOKE_PROFILE:-benchmarks/profiles/smoke.yaml}
reconnect_profile_path=${CI_RECONNECT_PROFILE:-benchmarks/profiles/ci-websocket-reconnect.yaml}
resync_profile_path=${CI_RESYNC_PROFILE:-benchmarks/profiles/ci-websocket-resync.yaml}
artifact_dir=${CI_SMOKE_ARTIFACT_DIR:-benchmarks/results/ci-smoke}
runtime_parent=${RUNNER_TEMP:-${TMPDIR:-/tmp}}
runtime_dir=$(mktemp -d "$runtime_parent/trainpilot-ci-smoke.XXXXXX")
server_pid=""
benchmark_pid=""
api_url=http://127.0.0.1:18080
diagnostics_url=http://127.0.0.1:16060

mkdir -p "$artifact_dir"

capture_failure_artifacts() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    curl --fail --silent --show-error "$diagnostics_url/metrics" \
      --output "$artifact_dir/metrics.prom" || true
    curl --fail --silent --show-error "$diagnostics_url/debug/pprof/goroutine?debug=2" \
      --output "$artifact_dir/goroutines.txt" || true
    curl --fail --silent --show-error "$diagnostics_url/debug/pprof/heap" \
      --output "$artifact_dir/heap.pb.gz" || true
  fi
}

stop_server() {
  local process_status=0

  if [[ -z "$server_pid" ]]; then
    return 0
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    wait "$server_pid" || process_status=$?
    server_pid=""
    return "$process_status"
  fi

  kill -TERM "$server_pid"
  for _ in {1..100}; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      wait "$server_pid" || process_status=$?
      server_pid=""
      return "$process_status"
    fi
    sleep 0.1
  done

  echo "dccd did not stop within 10 seconds" >&2
  kill -KILL "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  server_pid=""
  return 1
}

finish() {
  local status=$?
  trap - EXIT
  set +e
  if [[ -n "$benchmark_pid" ]] && kill -0 "$benchmark_pid" 2>/dev/null; then
    kill -TERM "$benchmark_pid" 2>/dev/null || true
    wait "$benchmark_pid" 2>/dev/null || true
  fi
  if ((status != 0)); then
    capture_failure_artifacts
  fi
  if ! stop_server; then
    status=1
  fi
  rm -rf -- "$runtime_dir"
  exit "$status"
}

trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_benchmark() {
  local selected_profile=$1
  local report_path=$2
  local log_path=$3
  local timeout_marker=$4
  local watchdog_pid
  local benchmark_status

  set +e
  "$benchmark_bin" run \
    --server "$api_url" \
    --profile "$selected_profile" \
    --credentials "$credentials_path" \
    --allow-active-commands \
    --allow-simulator-api \
    --output "$report_path" \
    >"$log_path" 2>&1 &
  benchmark_pid=$!
  (
    sleep 150
    if kill -0 "$benchmark_pid" 2>/dev/null; then
      touch "$timeout_marker"
      kill -TERM "$benchmark_pid" 2>/dev/null || true
      sleep 10
      kill -KILL "$benchmark_pid" 2>/dev/null || true
    fi
  ) &
  watchdog_pid=$!
  wait "$benchmark_pid"
  benchmark_status=$?
  benchmark_pid=""
  kill "$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
  set -e

  cat "$log_path"
  if [[ -f "$timeout_marker" ]]; then
    echo "benchmark did not finish within 150 seconds" >&2
    benchmark_status=124
  fi
  if ((benchmark_status != 0)); then
    return "$benchmark_status"
  fi
  "$benchmark_bin" validate-report "$report_path"
}

for required in "$dccd_bin" "$dccctl_bin" "$benchmark_bin"; do
  if [[ ! -x "$required" ]]; then
    echo "missing executable: $required" >&2
    exit 2
  fi
done

admin_socket=$runtime_dir/dccd-admin.sock
database_path=$runtime_dir/dcc-control.db
credentials_path=$runtime_dir/credentials.json
state_path=$runtime_dir/dccctl-state.json
benchmark_password="ci-smoke-${RANDOM}-${RANDOM}-${RANDOM}"

cat >"$artifact_dir/config.json" <<EOF
{
  "http": {"listen": "127.0.0.1:18080"},
  "diagnostics": {
    "enabled": true,
    "listen": "127.0.0.1:16060",
    "metrics": true,
    "pprof": true
  },
  "admin": {"socket": "$admin_socket", "mode": 384},
  "database": {"path": "$database_path"},
  "station": {"driver": "simulator"},
  "testAPI": true,
  "seedDemo": false
}
EOF

cat >"$credentials_path" <<'EOF'
{
  "schemaVersion": 1,
  "accounts": [
    {"username": "benchmark-01", "passwordEnv": "TRAINPILOT_BENCH_PASSWORD"},
    {"username": "benchmark-02", "passwordEnv": "TRAINPILOT_BENCH_PASSWORD"},
    {"username": "benchmark-03", "passwordEnv": "TRAINPILOT_BENCH_PASSWORD"},
    {"username": "benchmark-04", "passwordEnv": "TRAINPILOT_BENCH_PASSWORD"}
  ]
}
EOF
chmod 600 "$credentials_path"

"$dccd_bin" serve --config "$artifact_dir/config.json" >"$artifact_dir/server.log" 2>&1 &
server_pid=$!

ready=false
for _ in {1..300}; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "dccd exited during startup" >&2
    tail -100 "$artifact_dir/server.log" >&2
    exit 1
  fi
  if [[ -S "$admin_socket" ]] && curl --fail --silent "$api_url/healthz" >/dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  echo "dccd did not become ready within 30 seconds" >&2
  exit 1
fi

printf '%s\n' "$benchmark_password" | "$dccd_bin" user bootstrap \
  --socket "$admin_socket" \
  --username ci-admin \
  --display-name "CI Administrator" \
  --role administrator \
  --password-stdin

TRAINPILOT_BENCH_PASSWORD=$benchmark_password \
  DCCD_BIN=$dccd_bin \
  DCCD_SOCKET=$admin_socket \
  BENCHMARK_USER_COUNT=4 \
  BENCHMARK_USER_ROLE=dispatcher \
  scripts/benchmark-prepare-users.sh

export DCC_ADMIN_PASSWORD=$benchmark_password
"$dccctl_bin" --server "$api_url" --username ci-admin \
  --password-env DCC_ADMIN_PASSWORD --state-file "$state_path" \
  import-rolling-stock benchmarks/fixtures/small/rolling-stock.dcclib --replace
"$dccctl_bin" --server "$api_url" --username ci-admin \
  --password-env DCC_ADMIN_PASSWORD --state-file "$state_path" \
  import-layout benchmarks/fixtures/small/layout.dcclayout --replace
unset DCC_ADMIN_PASSWORD

export TRAINPILOT_BENCH_PASSWORD=$benchmark_password
run_benchmark "$profile_path" \
  "$artifact_dir/report.json" \
  "$artifact_dir/benchmark.log" \
  "$runtime_dir/benchmark-timeout"
run_benchmark "$reconnect_profile_path" \
  "$artifact_dir/websocket-reconnect-report.json" \
  "$artifact_dir/websocket-reconnect.log" \
  "$runtime_dir/websocket-reconnect-timeout"
run_benchmark "$resync_profile_path" \
  "$artifact_dir/websocket-resync-report.json" \
  "$artifact_dir/websocket-resync.log" \
  "$runtime_dir/websocket-resync-timeout"
unset TRAINPILOT_BENCH_PASSWORD
jq --exit-status '.webSocket.reconnects > 0' \
  "$artifact_dir/websocket-reconnect-report.json" >/dev/null
jq --exit-status '
  .webSocket.sequenceGaps > 0 and
  .webSocket.unresolvedSequenceGaps == 0 and
  any(.invariants[]; .name == "websocket_resynchronization" and .status == "PASS")
' "$artifact_dir/websocket-resync-report.json" >/dev/null
curl --fail --silent --show-error "$api_url/healthz" >/dev/null
