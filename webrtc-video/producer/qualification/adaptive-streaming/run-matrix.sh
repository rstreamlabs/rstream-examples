#!/usr/bin/env bash

set -Eeuo pipefail

trap 'exit 130' INT
trap 'exit 143' TERM

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_directory="${1:-${script_directory}/.artifacts/matrix-${timestamp}}"
repetitions="${RSTREAM_QUALIFICATION_REPETITIONS:-1}"

if [[ ! "${repetitions}" =~ ^[1-9][0-9]*$ ]]; then
  printf 'RSTREAM_QUALIFICATION_REPETITIONS must be a positive integer\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && \
  [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

run_failures=0
for repetition in $(seq 1 "${repetitions}"); do
  for protection in nack-rtx nack-rtx-flexfec; do
    for path in direct relay; do
      name="run-${repetition}-${path}-${protection}"
      printf 'Running %s\n' "${name}"
      if ! (
        if [[ "${path}" == "direct" ]]; then
          unset RSTREAM_QUALIFICATION_PRODUCER_DOCKER_CONTEXT
          unset RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST
        fi
        RSTREAM_QUALIFICATION_PATH="${path}" \
          RSTREAM_QUALIFICATION_PROTECTION="${protection}" \
          "${script_directory}/run.sh" "${output_directory}/${name}"
      ); then
        run_failures=$((run_failures + 1))
      fi
    done
  done
done

comparison_status=0
node "${script_directory}/compare.mjs" \
  "${output_directory}" "${repetitions}" || comparison_status=$?
if ((comparison_status != 0)); then
  exit "${comparison_status}"
fi
printf 'Qualification matrix passed; %d diagnostic run(s) had individual failures\n' \
  "${run_failures}"
