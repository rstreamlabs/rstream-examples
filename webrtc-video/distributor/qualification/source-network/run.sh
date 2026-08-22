#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
end_to_end_runner="${script_directory}/../end-to-end/run.sh"
context_name="${RSTREAM_CONTEXT:-}"
output_directory="${1:-}"
run_count="${RSTREAM_DISTRIBUTOR_SOURCE_RUNS:-3}"
warmup_seconds="${RSTREAM_DISTRIBUTOR_WARMUP_SECONDS:-20}"
phase_seconds="${RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS:-15}"
recovery_seconds="${RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS:-45}"
capacity_kbps="${RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS:-0}"
delay_milliseconds="${RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS:-0}"
jitter_milliseconds="${RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS:-0}"
loss_percent="${RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT:-0}"
queue_packets="${RSTREAM_DISTRIBUTOR_SOURCE_QUEUE_PACKETS:-256}"

if [[ -z "${context_name}" ]]; then
  printf 'RSTREAM_CONTEXT must name an explicit qualification context\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" ]]; then
  printf 'usage: RSTREAM_CONTEXT=<context> %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! [[ "${run_count}" =~ ^[0-9]+$ ]] || ((run_count < 3 || run_count > 10)); then
  printf 'RSTREAM_DISTRIBUTOR_SOURCE_RUNS must be from 3 through 10\n' >&2
  exit 1
fi
if ! jq -en \
  --arg capacity "${capacity_kbps}" \
  --arg delay "${delay_milliseconds}" \
  --arg jitter "${jitter_milliseconds}" \
  --arg loss "${loss_percent}" '
    ($capacity | tonumber) >= 0 and
    ($delay | tonumber) >= 0 and
    ($jitter | tonumber) >= 0 and
    ($loss | tonumber) >= 0 and
    (($capacity | tonumber) > 0 or ($delay | tonumber) > 0 or ($jitter | tonumber) > 0 or ($loss | tonumber) > 0)
  ' >/dev/null 2>&1; then
  printf 'at least one valid producer-to-adapter network condition is required\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}/runs"
output_directory="$(cd "${output_directory}" && pwd -P)"

for run in $(seq 1 "${run_count}"); do
  run_directory="${output_directory}/runs/${run}"
  printf 'Running producer-to-adapter qualification %d/%d\n' "${run}" "${run_count}"
  status=0
  RSTREAM_DISTRIBUTOR_MODE=mediamtx \
  RSTREAM_DISTRIBUTOR_WARMUP_SECONDS="${warmup_seconds}" \
  RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS="${phase_seconds}" \
  RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS="${recovery_seconds}" \
  RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT=0 \
  RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS="${capacity_kbps}" \
  RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS="${delay_milliseconds}" \
  RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS="${jitter_milliseconds}" \
  RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT="${loss_percent}" \
  RSTREAM_DISTRIBUTOR_SOURCE_QUEUE_PACKETS="${queue_packets}" \
    "${end_to_end_runner}" "${run_directory}" || status=$?
  if [[ ! -s "${run_directory}/result.json" ]]; then
    printf 'producer-to-adapter run %d did not produce evidence (status %d)\n' "${run}" "${status}" >&2
    exit 1
  fi
  jq --argjson execution_status "${status}" '. + {executionStatus: $execution_status}' \
    "${run_directory}/result.json" >"${run_directory}/source-result.json"
done

jq -s \
  --argjson run_count "${run_count}" \
  -f "${script_directory}/report.jq" \
  "${output_directory}"/runs/*/source-result.json >"${output_directory}/summary.json"
if [[ "$(jq -r '.passed' "${output_directory}/summary.json")" != true ]]; then
  jq '.gates' "${output_directory}/summary.json" >&2
  printf 'producer-to-adapter qualification failed\n' >&2
  exit 1
fi
printf 'Producer-to-adapter qualification passed: %s\n' "${output_directory}/summary.json"

