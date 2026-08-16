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
stop() {
  running=0
}
trap stop INT TERM

previous_epoch_milliseconds=""
while [ "${running}" -eq 1 ]; do
  read -r label user nice system idle iowait irq softirq steal _ < /proc/stat
  if [ "${label}" != "cpu" ]; then
    printf 'Linux /proc/stat did not start with aggregate CPU counters\n' >&2
    exit 1
  fi
  captured="$(date -u +'%s%3N|%Y-%m-%dT%H:%M:%S.%3NZ')"
  captured_epoch_milliseconds="${captured%%|*}"
  captured_at="${captured#*|}"
  gap_milliseconds=0
  if [ -n "${previous_epoch_milliseconds}" ]; then
    gap_milliseconds="$((captured_epoch_milliseconds - previous_epoch_milliseconds))"
  fi
  previous_epoch_milliseconds="${captured_epoch_milliseconds}"
  printf '{"capturedAt":"%s","gapMilliseconds":%s,"userTicks":%s,"niceTicks":%s,"systemTicks":%s,"idleTicks":%s,"ioWaitTicks":%s,"irqTicks":%s,"softIRQTicks":%s,"stealTicks":%s}\n' \
    "${captured_at}" "${gap_milliseconds}" "${user}" "${nice}" "${system}" "${idle}" \
    "${iowait}" "${irq}" "${softirq}" "${steal}" >> "${output}"
  sleep "${interval_seconds}" &
  wait $! || true
done
