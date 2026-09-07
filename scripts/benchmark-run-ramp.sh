#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <trainpilot-bench> <server-url> <credentials.json> <output-dir>" >&2
  exit 2
fi

bench_bin=$1
server_url=$2
credentials_file=$3
output_dir=$4
fixture=benchmarks/fixtures/xlarge/fixture.json

mkdir -p "$output_dir"

run_stage() {
  local stage=$1
  local profile=$2
  local duration=$3
  "$bench_bin" run \
    --server "$server_url" \
    --profile "benchmarks/profiles/$profile.yaml" \
    --fixture "$fixture" \
    --credentials "$credentials_file" \
    --warmup 0s \
    --duration "$duration" \
    --allow-active-commands \
    --allow-simulator-api \
    --output "$output_dir/$stage.json"
}

run_stage 01-idle idle 2m
run_stage 02-small small 3m
run_stage 03-medium medium 3m
run_stage 04-large large 4m
run_stage 05-xlarge xlarge 3m
run_stage 06-burst ramp-burst 2m
run_stage 07-medium-recovery medium 3m
