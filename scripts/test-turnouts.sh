#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/test-turnouts.sh \
    --server URL \
    --username USER \
    --turnout ID \
    --positions POSITION[,POSITION...] \
    --acknowledge-hardware-risk \
    [options]

Required:
  --server URL                    TrainPilot server URL.
  --username USER                 Dispatcher or administrator account.
  --turnout ID                    Logical turnout identifier.
  --positions LIST                Ordered logical positions to exercise.
  --acknowledge-hardware-risk     Explicitly authorize real accessory commands.

Options:
  --password-env NAME             Environment variable read by dccctl.
  --dccctl PATH                   dccctl binary. Defaults to bin/dccctl, PATH,
                                  then "go run ./cmd/dccctl".
  --state-file PATH               dccctl state file. A temporary file is used
                                  by default and removed on exit.
  --cycles N                      Number of complete position sequences (20).
  --delay SECONDS                 Delay between endurance commands (0.5).
  --offline-check                Run the interactive disconnect/no-replay phase.
  --no-replay-wait SECONDS        Observation after reconnect (20).
  --external-check               Run an external-client observation phase.
  --external-expect report|none  Expected external report behavior (report).
  --log PATH                      Append timestamped commands and observations.
  --dry-run                       Print the planned commands without contacting
                                  the server or requiring the safety acknowledgement.
  -h, --help                      Show this help.

This script never claims a mechanical confirmation automatically. The operator
must observe the accessory or a safe LED/relay load and answer each checkpoint.
EOF
}

server=""
username=""
password_env=""
turnout=""
positions_text=""
dccctl_path=""
state_file=""
cycles=20
delay="0.5"
no_replay_wait="20"
external_expect="report"
log_path=""
acknowledged=false
offline_check=false
external_check=false
dry_run=false
temporary_state=false
temporary_state_dir=""

while (($# > 0)); do
  case "$1" in
    --server) server="${2:-}"; shift 2 ;;
    --username) username="${2:-}"; shift 2 ;;
    --password-env) password_env="${2:-}"; shift 2 ;;
    --turnout) turnout="${2:-}"; shift 2 ;;
    --positions) positions_text="${2:-}"; shift 2 ;;
    --dccctl) dccctl_path="${2:-}"; shift 2 ;;
    --state-file) state_file="${2:-}"; shift 2 ;;
    --cycles) cycles="${2:-}"; shift 2 ;;
    --delay) delay="${2:-}"; shift 2 ;;
    --offline-check) offline_check=true; shift ;;
    --no-replay-wait) no_replay_wait="${2:-}"; shift 2 ;;
    --external-check) external_check=true; shift ;;
    --external-expect) external_expect="${2:-}"; shift 2 ;;
    --log) log_path="${2:-}"; shift 2 ;;
    --acknowledge-hardware-risk) acknowledged=true; shift ;;
    --dry-run) dry_run=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "$server" || -z "$username" || -z "$turnout" || -z "$positions_text" ]]; then
  printf 'Missing required argument.\n' >&2
  usage >&2
  exit 2
fi
if [[ ! "$cycles" =~ ^[0-9]+$ ]] || ((cycles < 1)); then
  printf '%s\n' '--cycles must be a positive integer.' >&2
  exit 2
fi
if [[ ! "$delay" =~ ^[0-9]+([.][0-9]+)?$ ]] || [[ ! "$no_replay_wait" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  printf '%s\n' '--delay and --no-replay-wait must be non-negative seconds.' >&2
  exit 2
fi
if [[ "$external_expect" != "report" && "$external_expect" != "none" ]]; then
  printf '%s\n' '--external-expect must be report or none.' >&2
  exit 2
fi
if [[ "$dry_run" != true && "$acknowledged" != true ]]; then
  printf '%s\n' 'Refusing real commands without --acknowledge-hardware-risk.' >&2
  exit 2
fi

IFS=',' read -r -a positions <<<"$positions_text"
if ((${#positions[@]} < 2)); then
  printf '%s\n' '--positions must contain at least two comma-separated values.' >&2
  exit 2
fi
for index in "${!positions[@]}"; do
  positions[$index]="${positions[$index]#"${positions[$index]%%[![:space:]]*}"}"
  positions[$index]="${positions[$index]%"${positions[$index]##*[![:space:]]}"}"
  if [[ -z "${positions[$index]}" ]]; then
    printf '%s\n' '--positions contains an empty value.' >&2
    exit 2
  fi
done

if [[ -n "$dccctl_path" ]]; then
  ctl=("$dccctl_path")
elif [[ -x "bin/dccctl" ]]; then
  ctl=("bin/dccctl")
elif command -v dccctl >/dev/null 2>&1; then
  ctl=("$(command -v dccctl)")
else
  ctl=(go run ./cmd/dccctl)
fi

cleanup() {
  if [[ "$temporary_state" == true && -n "$state_file" ]]; then
    rm -f -- "$state_file"
    rmdir -- "$temporary_state_dir" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if [[ -z "$state_file" ]]; then
  if [[ "$dry_run" == true ]]; then
    state_file="/tmp/trainpilot-turnout-dry-run-state.json"
  else
    temporary_state_dir="$(mktemp -d "${TMPDIR:-/tmp}/trainpilot-turnout-state.XXXXXX")"
    state_file="$temporary_state_dir/state.json"
    temporary_state=true
  fi
fi

base=("${ctl[@]}" --server "$server" --username "$username" --state-file "$state_file")
if [[ -n "$password_env" ]]; then
  base+=(--password-env "$password_env")
fi

record() {
  local message="$1"
  local line
  line="$(date -u '+%Y-%m-%dT%H:%M:%SZ') $message"
  printf '%s\n' "$line"
  if [[ -n "$log_path" ]]; then
    printf '%s\n' "$line" >>"$log_path"
  fi
}

display_command() {
  local value
  printf '+'
  for value in "$@"; do
    printf ' %q' "$value"
  done
  printf '\n'
}

run_ctl() {
  display_command "${base[@]}" "$@"
  if [[ "$dry_run" == true ]]; then
    return 0
  fi
  local output
  if ! output="$("${base[@]}" "$@" 2>&1)"; then
    record "FAIL dccctl $*"
    printf '%s\n' "$output" >&2
    return 1
  fi
  printf '%s\n' "$output"
  if [[ -n "$log_path" ]]; then
    printf '%s\n' "$output" >>"$log_path"
  fi
  record "PASS dccctl $*"
}

expect_ctl_failure() {
  display_command "${base[@]}" "$@"
  if [[ "$dry_run" == true ]]; then
    record "DRY-RUN expected refusal: dccctl $*"
    return 0
  fi
  local output
  if output="$("${base[@]}" "$@" 2>&1)"; then
    printf '%s\n' "$output"
    record "FAIL command unexpectedly succeeded: dccctl $*"
    return 1
  fi
  printf '%s\n' "$output"
  if [[ -n "$log_path" ]]; then
    printf '%s\n' "$output" >>"$log_path"
  fi
  record "PASS command refused as expected: dccctl $*"
}

confirm() {
  local prompt="$1"
  if [[ "$dry_run" == true ]]; then
    record "DRY-RUN manual checkpoint: $prompt"
    return 0
  fi
  local answer
  printf '%s [y/N] ' "$prompt"
  read -r answer
  case "$answer" in
    y|Y|yes|YES|Yes) record "PASS operator confirmation: $prompt" ;;
    *) record "FAIL operator confirmation: $prompt"; return 1 ;;
  esac
}

pause_for_action() {
  local prompt="$1"
  if [[ "$dry_run" == true ]]; then
    record "DRY-RUN operator action: $prompt"
    return 0
  fi
  printf '%s Press Enter when ready. ' "$prompt"
  read -r _
  record "Operator action completed: $prompt"
}

sleep_duration() {
  local duration="$1"
  if [[ "$dry_run" == true ]]; then
    record "DRY-RUN wait $duration"
  else
    sleep "$duration"
  fi
}

record "START turnout=$turnout server=$server positions=$positions_text cycles=$cycles"
run_ctl turnouts
run_ctl turnout "$turnout" --positions

confirm "The selected output is isolated or connected to a safe visible load, and emergency shutdown is accessible."

for position in "${positions[@]}"; do
  run_ctl turnout "$turnout" "$position"
  confirm "Turnout $turnout reached logical position $position exactly once without abnormal heating."
done

confirm "Start $cycles complete endurance sequences with delay $delay."
for ((cycle = 1; cycle <= cycles; cycle++)); do
  for position in "${positions[@]}"; do
    run_ctl turnout "$turnout" "$position"
    sleep_duration "$delay"
  done
  record "Endurance sequence $cycle/$cycles completed"
done
run_ctl turnouts
confirm "No command was missed or duplicated during the endurance sequence, and the load remained safe."

if [[ "$external_check" == true ]]; then
  external_target="${positions[1]}"
  run_ctl turnout "$turnout" "${positions[0]}"
  pause_for_action "Use the manufacturer application or another controller to command $turnout to $external_target."
  run_ctl turnouts
  if [[ "$external_expect" == "report" ]]; then
    confirm "TrainPilot reports $external_target while desired remains ${positions[0]}, without automatically restoring ${positions[0]}."
  else
    confirm "TrainPilot did not invent a reliable external report; this protocol limitation is recorded."
  fi
fi

if [[ "$offline_check" == true ]]; then
  offline_from="${positions[0]}"
  offline_to="${positions[1]}"
  run_ctl turnout "$turnout" "$offline_from"
  confirm "Turnout $turnout is visibly in $offline_from."
  pause_for_action "Disconnect or power off the command station, then wait longer than station.offlineAfter."
  run_ctl power status || true
  expect_ctl_failure turnout "$turnout" "$offline_to"
  pause_for_action "Reconnect or power on the command station, then wait until TrainPilot reports online."
  run_ctl power status
  record "Observing $no_replay_wait without issuing an accessory command"
  sleep_duration "$no_replay_wait"
  confirm "Turnout $turnout did not move during reconnection and the observation interval."
  run_ctl turnout "$turnout" "$offline_to"
  confirm "The new explicit command moved $turnout to $offline_to exactly once."
fi

run_ctl turnouts
record "PASS campaign completed; copy the log and observations into docs/hardware-tests/turnouts/."
