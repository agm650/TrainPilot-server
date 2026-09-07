#!/bin/sh
set -eu

output=${1:-/var/lib/node_exporter/textfile_collector/trainpilot-mac-linux.prom}
temporary=$(mktemp "${output}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM

escape_label() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

{
  echo '# HELP trainpilot_host_temperature_celsius Host temperature reported by the platform collector.'
  echo '# TYPE trainpilot_host_temperature_celsius gauge'
  for chip in /sys/class/hwmon/hwmon*; do
    [ -d "$chip" ] || continue
    chip_id=$(basename "$chip")
    chip_name=$chip_id
    if [ -r "$chip/name" ]; then
      chip_name=$(sed -n '1p' "$chip/name")
    fi
    for input in "$chip"/temp*_input; do
      [ -r "$input" ] || continue
      raw=$(sed -n '1p' "$input")
      case "$raw" in ''|*[!0-9]*) continue ;; esac
      sensor=$(basename "$input" _input)
      label_file=${input%_input}_label
      if [ -r "$label_file" ]; then
        sensor=$(sed -n '1p' "$label_file")
      fi
      printf 'trainpilot_host_temperature_celsius{sensor="%s:%s:%s"} %d.%03d\n' \
        "$(escape_label "$chip_id")" "$(escape_label "$chip_name")" \
        "$(escape_label "$sensor")" \
        "$((raw / 1000))" "$((raw % 1000))"
    done
  done
  echo '# HELP trainpilot_platform_collector_success Whether the platform textfile collection succeeded.'
  echo '# TYPE trainpilot_platform_collector_success gauge'
  echo 'trainpilot_platform_collector_success{platform="mac-linux"} 1'
} >"$temporary"

chmod 0644 "$temporary"
mv -f "$temporary" "$output"
trap - EXIT HUP INT TERM
