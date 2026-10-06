#!/bin/sh

set -eu

output="${1:-}"
interval_milliseconds="${2:-250}"

if [ -z "${output}" ]; then
  printf 'usage: sample-host-cpu.sh <output> [interval-milliseconds]\n' >&2
  exit 1
fi
case "${interval_milliseconds}" in
  '' | *[!0-9]* | 0*)
    printf 'interval-milliseconds must be a positive integer\n' >&2
    exit 1
    ;;
esac
if [ "${interval_milliseconds}" -gt 60000 ]; then
  printf 'interval-milliseconds must not exceed 60000\n' >&2
  exit 1
fi
interval_seconds="$(printf '%d.%03d' "$((interval_milliseconds / 1000))" "$((interval_milliseconds % 1000))")"

running=1
sleep_pid=""
stop() {
  running=0
  if [ -n "${sleep_pid}" ]; then
    kill -TERM "${sleep_pid}" 2>/dev/null || true
  fi
}
trap stop INT TERM

previous_boot_milliseconds=""
while [ "${running}" -eq 1 ]; do
  # Linux exposes CLOCK_BOOTTIME at centisecond precision. Unlike realtime,
  # it cannot step when the host synchronizes its civil clock; suspend still
  # counts as a pause. Keep UTC below only for correlation with phase events.
  read -r uptime _ < /proc/uptime
  uptime_seconds="${uptime%.*}"
  uptime_centiseconds="${uptime#*.}"
  case "${uptime_seconds}" in
    '' | *[!0-9]*)
      printf 'Linux /proc/uptime did not contain a valid boot time\n' >&2
      exit 1
      ;;
  esac
  case "${uptime_centiseconds}" in
    [0-9][0-9]) ;;
    *)
      printf 'Linux /proc/uptime did not contain centisecond precision\n' >&2
      exit 1
      ;;
  esac
  # Prefix the fractional part to avoid POSIX shell octal arithmetic (08/09).
  boot_milliseconds="$((uptime_seconds * 1000 + (1${uptime_centiseconds} - 100) * 10))"
  read -r label user nice system idle iowait irq softirq steal _ < /proc/stat
  if [ "${label}" != "cpu" ]; then
    printf 'Linux /proc/stat did not start with aggregate CPU counters\n' >&2
    exit 1
  fi
  captured_at="$(date -u +'%Y-%m-%dT%H:%M:%S.%3NZ')"
  gap_milliseconds=0
  if [ -n "${previous_boot_milliseconds}" ]; then
    gap_milliseconds="$((boot_milliseconds - previous_boot_milliseconds))"
    if [ "${gap_milliseconds}" -lt 0 ]; then
      printf 'Linux boot time moved backwards while sampling host CPU\n' >&2
      exit 1
    fi
  fi
  previous_boot_milliseconds="${boot_milliseconds}"
  printf '{"capturedAt":"%s","gapClock":"linux-boottime","bootMilliseconds":%s,"gapMilliseconds":%s,"userTicks":%s,"niceTicks":%s,"systemTicks":%s,"idleTicks":%s,"ioWaitTicks":%s,"irqTicks":%s,"softIRQTicks":%s,"stealTicks":%s}\n' \
    "${captured_at}" "${boot_milliseconds}" "${gap_milliseconds}" "${user}" "${nice}" "${system}" "${idle}" \
    "${iowait}" "${irq}" "${softirq}" "${steal}" >> "${output}"
  sleep "${interval_seconds}" &
  sleep_pid=$!
  if [ "${running}" -eq 0 ]; then
    kill -TERM "${sleep_pid}" 2>/dev/null || true
  fi
  wait "${sleep_pid}" || true
  if [ "${running}" -eq 0 ]; then
    wait "${sleep_pid}" 2>/dev/null || true
  fi
  sleep_pid=""
done
