#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
qualification_directory="$(cd "${script_directory}/.." && pwd -P)"
comparison_runner="${qualification_directory}/comparison/run.sh"
fanout_runner="${qualification_directory}/fanout/run.sh"
source_runner="${qualification_directory}/source-network/run.sh"
context_name="${RSTREAM_CONTEXT:-}"
output_directory="${1:-}"
run_count="${RSTREAM_DISTRIBUTOR_MATRIX_RUNS:-3}"

if [[ -z "${context_name}" ]]; then
  printf 'RSTREAM_CONTEXT must name an explicit qualification context\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" ]]; then
  printf 'usage: RSTREAM_CONTEXT=<context> %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! [[ "${run_count}" =~ ^[0-9]+$ ]] || ((run_count < 3 || run_count > 10)); then
  printf 'RSTREAM_DISTRIBUTOR_MATRIX_RUNS must be from 3 through 10\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

printf 'Qualifying MediaMTX fan-out with one upstream source\n'
fanout_status=0
RSTREAM_DISTRIBUTOR_FANOUT_RUNS="${run_count}" \
  "${fanout_runner}" "${output_directory}/fanout" || fanout_status=$?
if [[ ! -s "${output_directory}/fanout/summary.json" ]]; then
  printf 'fan-out qualification did not produce a summary (status %d)\n' "${fanout_status}" >&2
  exit 1
fi

printf 'Qualifying direct adaptation and the MediaMTX single-rendition boundary\n'
capacity_status=0
RSTREAM_DISTRIBUTOR_COMPARISON_RUNS="${run_count}" \
RSTREAM_DISTRIBUTOR_WARMUP_SECONDS=20 \
RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS=15 \
RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS=45 \
RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS=4000 \
RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS=0 \
RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS=0 \
RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT=0 \
  "${comparison_runner}" "${output_directory}/capacity" || capacity_status=$?
if [[ ! -s "${output_directory}/capacity/summary.json" ]]; then
  printf 'capacity qualification did not produce a summary (status %d)\n' "${capacity_status}" >&2
  exit 1
fi

printf 'Qualifying transport repair under delay, jitter, and random loss\n'
impairment_status=0
RSTREAM_DISTRIBUTOR_COMPARISON_RUNS="${run_count}" \
RSTREAM_DISTRIBUTOR_WARMUP_SECONDS=20 \
RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS=15 \
RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS=45 \
RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS=0 \
RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS=60 \
RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS=15 \
RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT=1 \
  "${comparison_runner}" "${output_directory}/impairment" || impairment_status=$?
if [[ ! -s "${output_directory}/impairment/summary.json" ]]; then
  printf 'impairment qualification did not produce a summary (status %d)\n' "${impairment_status}" >&2
  exit 1
fi

printf 'Qualifying producer-to-adapter capacity response and recovery\n'
source_capacity_status=0
RSTREAM_DISTRIBUTOR_SOURCE_RUNS="${run_count}" \
RSTREAM_DISTRIBUTOR_WARMUP_SECONDS=20 \
RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS=15 \
RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS=45 \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=4000 \
RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS=0 \
RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS=0 \
RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT=0 \
  "${source_runner}" "${output_directory}/source-capacity" || source_capacity_status=$?
if [[ ! -s "${output_directory}/source-capacity/summary.json" ]]; then
  printf 'producer-to-adapter capacity qualification did not produce a summary (status %d)\n' "${source_capacity_status}" >&2
  exit 1
fi

printf 'Qualifying producer-to-adapter repair under delay, jitter, and random loss\n'
source_impairment_status=0
RSTREAM_DISTRIBUTOR_SOURCE_RUNS="${run_count}" \
RSTREAM_DISTRIBUTOR_WARMUP_SECONDS=20 \
RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS=15 \
RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS=45 \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=0 \
RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS=60 \
RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS=15 \
RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT=1 \
  "${source_runner}" "${output_directory}/source-impairment" || source_impairment_status=$?
if [[ ! -s "${output_directory}/source-impairment/summary.json" ]]; then
  printf 'producer-to-adapter impairment qualification did not produce a summary (status %d)\n' "${source_impairment_status}" >&2
  exit 1
fi

jq -n \
  --argjson capacity_status "${capacity_status}" \
  --argjson impairment_status "${impairment_status}" \
  --argjson fanout_status "${fanout_status}" \
  --argjson source_capacity_status "${source_capacity_status}" \
  --argjson source_impairment_status "${source_impairment_status}" \
  --slurpfile capacity "${output_directory}/capacity/summary.json" \
  --slurpfile impairment "${output_directory}/impairment/summary.json" \
  --slurpfile fanout "${output_directory}/fanout/summary.json" \
  --slurpfile source_capacity "${output_directory}/source-capacity/summary.json" \
  --slurpfile source_impairment "${output_directory}/source-impairment/summary.json" \
  -f "${script_directory}/report.jq" >"${output_directory}/summary.json"
if [[ "$(jq -r '.passed' "${output_directory}/summary.json")" != true ]]; then
  jq '.gates' "${output_directory}/summary.json" >&2
  printf 'distribution qualification matrix failed\n' >&2
  exit 1
fi
printf 'Distribution qualification matrix passed: %s\n' "${output_directory}/summary.json"
jq '.productVerdict' "${output_directory}/summary.json"
