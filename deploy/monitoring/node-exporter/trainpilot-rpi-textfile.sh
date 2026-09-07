#!/bin/sh
set -eu

output=${1:-/var/lib/node_exporter/textfile_collector/trainpilot-rpi.prom}

if ! command -v vcgencmd >/dev/null 2>&1; then
  echo "vcgencmd is required" >&2
  exit 1
fi

temporary=$(mktemp "${output}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM

temperature=$(vcgencmd measure_temp | sed -n "s/^temp=\([0-9.]*\).*/\1/p")
frequency=$(vcgencmd measure_clock arm | sed -n 's/^frequency([^)]*)=\([0-9][0-9]*\)$/\1/p')
throttled_hex=$(vcgencmd get_throttled | sed -n 's/^throttled=\(0x[0-9A-Fa-f][0-9A-Fa-f]*\)$/\1/p')

if [ -z "$temperature" ] || [ -z "$frequency" ] || [ -z "$throttled_hex" ]; then
  echo "unexpected vcgencmd output" >&2
  exit 1
fi

throttled=$(printf '%d' "$throttled_hex")

flag() {
  metric=$1
  history=$2
  bit=$3
  value=0
  if [ $((throttled & (1 << bit))) -ne 0 ]; then
    value=1
  fi
  printf 'trainpilot_raspberry_pi_%s{history="%s"} %s\n' "$metric" "$history" "$value"
}

{
  echo '# HELP trainpilot_host_temperature_celsius Host temperature reported by the platform collector.'
  echo '# TYPE trainpilot_host_temperature_celsius gauge'
  printf 'trainpilot_host_temperature_celsius{sensor="cpu"} %s\n' "$temperature"
  echo '# HELP trainpilot_host_frequency_hertz Host component frequency reported by the platform collector.'
  echo '# TYPE trainpilot_host_frequency_hertz gauge'
  printf 'trainpilot_host_frequency_hertz{component="arm"} %s\n' "$frequency"
  echo '# HELP trainpilot_raspberry_pi_throttled_raw Raw vcgencmd get_throttled bit mask.'
  echo '# TYPE trainpilot_raspberry_pi_throttled_raw gauge'
  printf 'trainpilot_raspberry_pi_throttled_raw %s\n' "$throttled"
  echo '# HELP trainpilot_raspberry_pi_under_voltage Raspberry Pi under-voltage flag.'
  echo '# TYPE trainpilot_raspberry_pi_under_voltage gauge'
  flag under_voltage current 0
  flag under_voltage occurred 16
  echo '# HELP trainpilot_raspberry_pi_frequency_capped Raspberry Pi frequency-capped flag.'
  echo '# TYPE trainpilot_raspberry_pi_frequency_capped gauge'
  flag frequency_capped current 1
  flag frequency_capped occurred 17
  echo '# HELP trainpilot_raspberry_pi_throttled Raspberry Pi throttling flag.'
  echo '# TYPE trainpilot_raspberry_pi_throttled gauge'
  flag throttled current 2
  flag throttled occurred 18
  echo '# HELP trainpilot_raspberry_pi_soft_temperature_limit Raspberry Pi soft-temperature-limit flag.'
  echo '# TYPE trainpilot_raspberry_pi_soft_temperature_limit gauge'
  flag soft_temperature_limit current 3
  flag soft_temperature_limit occurred 19
  echo '# HELP trainpilot_platform_collector_success Whether the platform textfile collection succeeded.'
  echo '# TYPE trainpilot_platform_collector_success gauge'
  echo 'trainpilot_platform_collector_success{platform="raspberry-pi"} 1'
} >"$temporary"

chmod 0644 "$temporary"
mv -f "$temporary" "$output"
trap - EXIT HUP INT TERM
