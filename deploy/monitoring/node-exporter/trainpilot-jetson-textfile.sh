#!/bin/sh
set -eu

output=${1:-/var/lib/node_exporter/textfile_collector/trainpilot-jetson.prom}
temporary=$(mktemp "${output}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM

escape_label() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

temperature_value() {
  raw=$1
  if [ "$raw" -ge 1000 ]; then
    printf '%d.%03d' "$((raw / 1000))" "$((raw % 1000))"
  else
    printf '%d' "$raw"
  fi
}

{
  echo '# HELP trainpilot_host_temperature_celsius Host temperature reported by the platform collector.'
  echo '# TYPE trainpilot_host_temperature_celsius gauge'
  for zone in /sys/class/thermal/thermal_zone*; do
    [ -r "$zone/temp" ] || continue
    raw=$(sed -n '1p' "$zone/temp")
    case "$raw" in ''|*[!0-9]*) continue ;; esac
    zone_name=$(basename "$zone")
    sensor=$zone_name
    if [ -r "$zone/type" ]; then
      sensor=$(sed -n '1p' "$zone/type")
    fi
    printf 'trainpilot_host_temperature_celsius{sensor="%s"} %s\n' \
      "$(escape_label "$sensor:$zone_name")" "$(temperature_value "$raw")"
  done
  echo '# HELP trainpilot_host_frequency_hertz Host component frequency reported by the platform collector.'
  echo '# TYPE trainpilot_host_frequency_hertz gauge'
  for policy in /sys/devices/system/cpu/cpufreq/policy*; do
    [ -r "$policy/scaling_cur_freq" ] || continue
    raw=$(sed -n '1p' "$policy/scaling_cur_freq")
    case "$raw" in ''|*[!0-9]*) continue ;; esac
    printf 'trainpilot_host_frequency_hertz{component="%s"} %s\n' \
      "$(basename "$policy")" "$((raw * 1000))"
  done
  echo '# HELP trainpilot_platform_collector_success Whether the platform textfile collection succeeded.'
  echo '# TYPE trainpilot_platform_collector_success gauge'
  echo 'trainpilot_platform_collector_success{platform="jetson"} 1'
} >"$temporary"

chmod 0644 "$temporary"
mv -f "$temporary" "$output"
trap - EXIT HUP INT TERM
