#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
qualification_directory="$(cd "${script_directory}/.." && pwd -P)"
repository_directory="$(git -C "${qualification_directory}" rev-parse --show-toplevel)"
runner="${qualification_directory}/end-to-end/run.sh"
context_name="${RSTREAM_CONTEXT:-}"
output_directory="${1:-}"
run_count="${RSTREAM_DISTRIBUTOR_NATIVE_RUNS:-3}"

if [[ -z "${context_name}" ]]; then
  printf 'RSTREAM_CONTEXT must name an explicit qualification context\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" ]]; then
  printf 'usage: RSTREAM_CONTEXT=<context> %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! [[ "${run_count}" =~ ^[0-9]+$ ]] || ((run_count < 3 || run_count > 10)); then
  printf 'RSTREAM_DISTRIBUTOR_NATIVE_RUNS must be from 3 through 10\n' >&2
  exit 1
fi
for command in git jq; do
  if ! command -v "${command}" >/dev/null; then
    printf 'required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}/runs"
output_directory="$(cd "${output_directory}" && pwd -P)"
revision="$(git -C "${repository_directory}" rev-parse HEAD)"
working_tree_dirty=false
if [[ -n "$(git -C "${repository_directory}" status --porcelain)" ]]; then
  working_tree_dirty=true
fi

for run in $(seq 1 "${run_count}"); do
  printf 'Running native MediaMTX qualification %d/%d\n' "${run}" "${run_count}"
  status=0
  RSTREAM_DISTRIBUTOR_MODE=mediamtx-native \
    "${runner}" "${output_directory}/runs/${run}" || status=$?
  if [[ -s "${output_directory}/runs/${run}/result.json" ]]; then
    jq -c --argjson run "${run}" --argjson status "${status}" \
      '{run: $run, status: $status, result: .}' \
      "${output_directory}/runs/${run}/result.json" >>"${output_directory}/records.jsonl"
  else
    jq -cn --argjson run "${run}" --argjson status "${status}" \
      '{run: $run, status: $status, result: null}' >>"${output_directory}/records.jsonl"
  fi
done

jq -s \
  --arg revision "${revision}" \
  --argjson working_tree_dirty "${working_tree_dirty}" \
  --argjson requested_runs "${run_count}" \
  -f "${script_directory}/report.jq" \
  "${output_directory}/records.jsonl" >"${output_directory}/summary.json"
if [[ "$(jq -r '.passed' "${output_directory}/summary.json")" != true ]]; then
  jq '.gates' "${output_directory}/summary.json" >&2
  printf 'native MediaMTX qualification failed\n' >&2
  exit 1
fi
printf 'Native MediaMTX qualification passed: %s\n' "${output_directory}/summary.json"
