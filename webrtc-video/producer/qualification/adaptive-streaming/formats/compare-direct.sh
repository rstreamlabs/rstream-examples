#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
video_directory="$(cd "${script_directory}/../../../.." && pwd -P)"
runner="${RSTREAM_FORMAT_QUALIFICATION_RUNNER:-${video_directory}/distributor/qualification/end-to-end/run.sh}"
down_hold="${1:-}"
output_directory="${2:-}"
if [[ "${down_hold}" != 1s && "${down_hold}" != 3s && "${down_hold}" != paired ]]; then
  printf 'down-hold comparison accepts only 1s, 3s or paired\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" || -e "${output_directory}" ]]; then
  printf 'an unused output directory is required\n' >&2
  exit 1
fi
umask 077
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"
profile_directory="$(mktemp -d)"
trap 'rm -rf "${profile_directory}"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [[ "$(grep -Ec '^      downHold: 3s$' "${script_directory}/config.automatic.yaml")" != 1 ]]; then
  printf 'automatic profile changed; revalidate the comparison\n' >&2
  exit 1
fi
holds=("${down_hold}" "${down_hold}" "${down_hold}")
if [[ "${down_hold}" == paired ]]; then
  # Reuse the same runner and cached images, counterbalancing adjacent pairs.
  holds=(1s 3s 3s 1s 1s 3s)
fi
for index in "${!holds[@]}"; do
  run=$((index + 1))
  trial_hold="${holds[index]}"
  sed "s/^      downHold: 3s$/      downHold: ${trial_hold}/" \
    "${script_directory}/config.automatic.yaml" >"${profile_directory}/producer.yaml"
  chmod 0600 "${profile_directory}/producer.yaml"
  status=0
  RSTREAM_CONTEXT='' \
  RSTREAM_DISTRIBUTOR_CONTROL_PATH=local \
  RSTREAM_DISTRIBUTOR_EDGE_AUTH=false \
  RSTREAM_DISTRIBUTOR_MODE=direct \
  RSTREAM_DISTRIBUTOR_EXPECT_FORMAT=small \
  RSTREAM_DISTRIBUTOR_WARMUP_SECONDS=20 \
  RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS=45 \
  RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS=90 \
  RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS=1500 \
  RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_VIEWER_QUEUE_PACKETS=256 \
  RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=0 \
  RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT=0 \
  RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS=0 \
  RSTREAM_DISTRIBUTOR_FLEXFEC_MEDIA_PACKETS=5 \
  RSTREAM_DISTRIBUTOR_FLEXFEC_REPAIR_PACKETS=1 \
  RSTREAM_DISTRIBUTOR_PLAYOUT_DELAY_HINT_SECONDS=0 \
  RSTREAM_DISTRIBUTOR_RECORDING=false \
  RSTREAM_DISTRIBUTOR_LATENCY_PROBE=false \
  RSTREAM_DISTRIBUTOR_STARTUP_CYCLES=false \
  RSTREAM_DISTRIBUTOR_PRODUCER_CONFIG="${profile_directory}/producer.yaml" \
    "${runner}" "${output_directory}/run-${run}" >"${output_directory}/run-${run}.log" 2>&1 || status=$?
  if [[ ! -s "${output_directory}/run-${run}/result.json" ]]; then
    jq -cn --argjson run "${run}" --argjson status "${status}" --arg hold "${trial_hold}" \
      '{run: $run, downHold: $hold, status: $status, result: null}' >>"${output_directory}/records.jsonl"
    # Setup did not complete: retain it without retrying the same prerequisite.
    break
  fi
  jq -c --argjson run "${run}" --argjson status "${status}" --arg hold "${trial_hold}" \
    '{run: $run, downHold: $hold, status: $status, result: .}' \
    "${output_directory}/run-${run}/result.json" >>"${output_directory}/records.jsonl"
done
jq -s --arg down_hold "${down_hold}" --argjson requested_runs "${#holds[@]}" '
  (length == $requested_runs and all(.[]; .result != null) and
    ([.[].result.images.producer] | unique | length == 1) and
    ([.[].result.images.browser] | unique | length == 1) and
    all(.[]; (.result.images.producer | type == "string") and
      (.result.images.browser | type == "string"))) as $same_images |
{
  schemaVersion: 2,
  scope: "direct WebRTC on an isolated Docker bridge; no tunnel, TURN or edge authentication",
  downHold: $down_hold,
  requestedRuns: $requested_runs,
  sameImages: $same_images,
  passed: ($same_images and length == $requested_runs and all(.[];
    .status == 0 and .result.passed == true and
    .result.profile.controlPath == "local" and .result.profile.edgeAuthentication == false)),
  runs: .
}' "${output_directory}/records.jsonl" >"${output_directory}/summary.json"
jq -e '.passed' "${output_directory}/summary.json" >/dev/null
