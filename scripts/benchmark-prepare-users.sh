#!/bin/sh
set -eu

: "${TRAINPILOT_BENCH_PASSWORD:?set TRAINPILOT_BENCH_PASSWORD}"

DCCD_BIN=${DCCD_BIN:-dccd}
DCCD_SOCKET=${DCCD_SOCKET:-/tmp/dccd-admin.sock}
BENCHMARK_USER_COUNT=${BENCHMARK_USER_COUNT:-4}
BENCHMARK_USER_ROLE=${BENCHMARK_USER_ROLE:-dispatcher}

case "$BENCHMARK_USER_COUNT" in
  ''|*[!0-9]*)
    echo "BENCHMARK_USER_COUNT must be a positive integer" >&2
    exit 2
    ;;
  0)
    echo "BENCHMARK_USER_COUNT must be greater than zero" >&2
    exit 2
    ;;
esac

index=1
while [ "$index" -le "$BENCHMARK_USER_COUNT" ]; do
  username=$(printf 'benchmark-%02d' "$index")
  printf '%s\n' "$TRAINPILOT_BENCH_PASSWORD" | "$DCCD_BIN" user add \
    --socket "$DCCD_SOCKET" \
    --username "$username" \
    --display-name "Benchmark $index" \
    --role "$BENCHMARK_USER_ROLE" \
    --password-stdin
  index=$((index + 1))
done

echo "Created $BENCHMARK_USER_COUNT benchmark users with role $BENCHMARK_USER_ROLE"
