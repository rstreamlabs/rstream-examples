#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
distributor_directory="$(cd "${script_directory}/../.." && pwd -P)"
repository_directory="$(git -C "${distributor_directory}" rev-parse --show-toplevel)"
output_directory="${1:-}"
run_count="${RSTREAM_DISTRIBUTOR_FANOUT_RUNS:-3}"
mediamtx_binary="${RSTREAM_MEDIAMTX_BINARY:-}"

if [[ -z "${output_directory}" ]]; then
  printf 'usage: %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! [[ "${run_count}" =~ ^[0-9]+$ ]] || ((run_count < 3 || run_count > 20)); then
  printf 'RSTREAM_DISTRIBUTOR_FANOUT_RUNS must be from 3 through 20\n' >&2
  exit 1
fi
for command in git go jq; do
  if ! command -v "${command}" >/dev/null; then
    printf 'required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done
if [[ -z "${mediamtx_binary}" ]]; then
  mediamtx_binary="$(command -v mediamtx || true)"
fi
if [[ -z "${mediamtx_binary}" ]] || [[ ! -x "${mediamtx_binary}" ]]; then
  printf 'MediaMTX executable not found; install mediamtx or set RSTREAM_MEDIAMTX_BINARY to an executable path\n' >&2
  exit 1
fi
mediamtx_binary="$(cd "$(dirname "${mediamtx_binary}")" && pwd -P)/$(basename "${mediamtx_binary}")"
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
  printf 'Running fan-out qualification %d/%d\n' "${run}" "${run_count}"
  RSTREAM_DISTRIBUTOR_QUALIFICATION_OUTPUT="${output_directory}/runs/${run}.json" \
  RSTREAM_DISTRIBUTOR_QUALIFICATION_REVISION="${revision}" \
  RSTREAM_MEDIAMTX_BINARY="${mediamtx_binary}" \
    go -C "${distributor_directory}" test \
      -count=1 \
      -timeout=3m \
      -tags='integration qualification' \
      -run '^TestMediaMTXFanOutQualification$' \
      ./internal/bridge
done

jq -s \
  --arg revision "${revision}" \
  --argjson working_tree_dirty "${working_tree_dirty}" \
  -f "${script_directory}/report.jq" \
  "${output_directory}"/runs/*.json >"${output_directory}/summary.json"

if ! jq -e '
  .passed == true and
  (.phases | length) == 3 and
  ([.phases[] | .inboundBitsPerSecond.minimum, .inboundBitsPerSecond.maximum, .outboundBitsPerSecond.minimum, .outboundBitsPerSecond.maximum] | all(. != null))
' "${output_directory}/summary.json" >/dev/null; then
  jq '.gates' "${output_directory}/summary.json" >&2
  printf 'fan-out qualification failed\n' >&2
  exit 1
fi
printf 'Fan-out qualification passed: %s\n' "${output_directory}/summary.json"
