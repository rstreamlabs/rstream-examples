#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
end_to_end_runner="${script_directory}/../end-to-end/run.sh"
context_name="${RSTREAM_CONTEXT:-}"
output_directory="${1:-}"
run_count="${RSTREAM_DISTRIBUTOR_COMPARISON_RUNS:-3}"
warmup_seconds="${RSTREAM_DISTRIBUTOR_WARMUP_SECONDS:-20}"
phase_seconds="${RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS:-15}"
recovery_seconds="${RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS:-45}"
capacity_kbps="${RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS:-4000}"
delay_milliseconds="${RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS:-0}"
jitter_milliseconds="${RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS:-0}"
loss_percent="${RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT:-0}"
queue_packets="${RSTREAM_DISTRIBUTOR_VIEWER_QUEUE_PACKETS:-256}"

if [[ -z "${context_name}" ]]; then
  printf 'RSTREAM_CONTEXT must name an explicit qualification context\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" ]]; then
  printf 'usage: RSTREAM_CONTEXT=<context> %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! [[ "${run_count}" =~ ^[0-9]+$ ]] || ((run_count < 3 || run_count > 10)); then
  printf 'RSTREAM_DISTRIBUTOR_COMPARISON_RUNS must be from 3 through 10\n' >&2
  exit 1
fi
if ! [[ "${warmup_seconds}" =~ ^[0-9]+$ ]] || ((warmup_seconds < 10 || warmup_seconds > 300)); then
  printf 'RSTREAM_DISTRIBUTOR_WARMUP_SECONDS must be from 10 through 300\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}/runs"
output_directory="$(cd "${output_directory}" && pwd -P)"

for run in $(seq 1 "${run_count}"); do
  for mode in direct mediamtx; do
    run_directory="${output_directory}/runs/${run}/${mode}"
    printf 'Running %s comparison %d/%d\n' "${mode}" "${run}" "${run_count}"
    status=0
    RSTREAM_DISTRIBUTOR_MODE="${mode}" \
    RSTREAM_DISTRIBUTOR_WARMUP_SECONDS="${warmup_seconds}" \
    RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS="${phase_seconds}" \
    RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS="${recovery_seconds}" \
    RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS="${capacity_kbps}" \
    RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS="${delay_milliseconds}" \
    RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS="${jitter_milliseconds}" \
    RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT="${loss_percent}" \
    RSTREAM_DISTRIBUTOR_VIEWER_QUEUE_PACKETS="${queue_packets}" \
      "${end_to_end_runner}" "${run_directory}" || status=$?
    if [[ ! -s "${run_directory}/result.json" ]]; then
      printf '%s run %d did not produce evidence (status %d)\n' "${mode}" "${run}" "${status}" >&2
      exit 1
    fi
    jq --argjson execution_status "${status}" '. + {executionStatus: $execution_status}' \
      "${run_directory}/result.json" >"${run_directory}/comparison-result.json"
  done
done

jq -s \
  --argjson run_count "${run_count}" \
  -f "${script_directory}/report.jq" \
  "${output_directory}"/runs/*/*/comparison-result.json >"${output_directory}/summary.json"
if [[ "$(jq -r '.passed' "${output_directory}/summary.json")" != true ]]; then
  jq '.gates' "${output_directory}/summary.json" >&2
  printf 'comparison evidence is incomplete or the direct reference failed\n' >&2
  exit 1
fi
printf 'Comparison evidence collected: %s\n' "${output_directory}/summary.json"
jq '.verdict' "${output_directory}/summary.json"
