#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=phase.sh
# shellcheck disable=SC1091
source "${script_directory}/phase.sh"
fixture_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-phase.XXXXXX")"
writer_pid=""
cleanup() {
  if [[ -n "${writer_pid}" ]]; then
    kill "${writer_pid}" >/dev/null 2>&1 || true
    wait "${writer_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${fixture_directory}"
}
trap cleanup EXIT INT TERM

phase_file="${fixture_directory}/phase.json"
write_phase_file "${phase_file}" initial
(
  for iteration in $(seq 1 1000); do
    write_phase_file "${phase_file}" "phase-${iteration}"
  done
) &
writer_pid=$!
while kill -0 "${writer_pid}" >/dev/null 2>&1; do
  jq -e '
    .name | type == "string" and
    (input_filename | type == "string")
  ' "${phase_file}" >/dev/null
done
wait "${writer_pid}"
writer_pid=""
jq -e '.name == "phase-1000" and (.startedAt | fromdateiso8601 | type == "number")' \
  "${phase_file}" >/dev/null
if find "${fixture_directory}" -name '.phase.json.tmp.*' -print -quit | grep -q .; then
  printf 'atomic phase writer left a temporary file behind\n' >&2
  exit 1
fi
