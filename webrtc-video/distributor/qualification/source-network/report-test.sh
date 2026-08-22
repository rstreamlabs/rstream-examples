#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-source-network-report.XXXXXX")"
trap 'rm -rf "${temporary_directory}"' EXIT INT TERM

write_fixture() {
  local capacity=$1
  local loss=$2
  local run=$3
  jq -n --argjson capacity "${capacity}" --argjson loss "${loss}" --argjson run "${run}" '{
    revision: "revision",
    mode: "mediamtx",
    workingTreeDirty: false,
    executionStatus: 0,
    passed: true,
    profile: {
      edgeAuthentication: true,
      sourceNetwork: {enabled: true, capacityKbps: $capacity, delayMilliseconds: (if $loss > 0 then 60 else 0 end), jitterMilliseconds: (if $loss > 0 then 15 else 0 end), lossPercent: $loss},
      viewerNetwork: {enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0}
    },
    sourceNetwork: {
      scope: "producer-to-adapter",
      destination: {port: null},
      qdisc: {packets: (1000 + $run), drops: (if $loss > 0 then 10 + $run else 0 end)},
      twccFeedbackPacketsDelta: (50 + $run),
      adaptiveBitrateUpdatesDelta: (if $capacity > 0 then 4 else 0 end),
      pacerSentRetransmissionDelta: (if $loss > 0 then 12 + $run else 0 end),
      pacerSentFECDelta: (if $loss > 0 then 4 + $run else 0 end),
      frameDropRatio: 0,
      freezeRatio: 0
    },
    adapter: {repaired_rtx: (if $loss > 0 then 8 + $run else 0 end), repaired_fec: (if $loss > 0 then 2 + $run else 0 end)},
    phases: {
      baseline: {encoderTargetKbps: {medianLast10Seconds: 8000}, decodedFramesPerSecond: 30, averageDecodedQP: 22},
      sourceNetwork: {encoderTargetKbps: {medianLast10Seconds: (if $capacity > 0 then 3800 else 7600 end)}, decodedFramesPerSecond: 30, averageDecodedQP: 25},
      recovery: {encoderTargetKbps: {medianLast10Seconds: 7600}, decodedFramesPerSecond: 30, averageDecodedQP: 22}
    },
    setup: {peerToFirstDecodedFrameMilliseconds: (500 + $run)},
    resources: {components: {
      browser: {samples: 5, residentBytes: {maximum: 400000000}},
      distributor: {samples: 5, cpuCoreRatio: {maximum: 0.2}, residentBytes: {maximum: 50000000}},
      producer: {samples: 5, cpuCoreRatio: {maximum: 0.6}, residentBytes: {maximum: 80000000}}
    }},
    gates: {
      sourceNetworkCausality: true,
      sourceNetworkResponse: true,
      sourceTargetRecovery: true,
      playback: true,
      runtimeMediaIntegrity: true,
      adapterIntegrity: true,
      resourceLifecycle: true,
      performanceEnvironment: true
    }
  }'
}

for run in 1 2 3; do
  write_fixture 4000 0 "${run}" >>"${temporary_directory}/capacity.jsonl"
done
jq -s --argjson run_count 3 -f "${script_directory}/report.jq" \
  "${temporary_directory}/capacity.jsonl" >"${temporary_directory}/capacity-summary.json"
jq -e '
  .passed == true and
  .publishable == true and
  .gates.producerToAdapterOnly == true and
  .gates.capacityResponse == true and
  .gates.repairResponse == true and
  .bitrate.conditionedEncoderTargetKbps == {minimum: 3800, maximum: 3800}
' "${temporary_directory}/capacity-summary.json" >/dev/null

for run in 1 2 3; do
  write_fixture 0 1 "${run}" >>"${temporary_directory}/loss.jsonl"
done
jq -s --argjson run_count 3 -f "${script_directory}/report.jq" \
  "${temporary_directory}/loss.jsonl" >"${temporary_directory}/loss-summary.json"
jq -e '
  .passed == true and
  .gates.repairResponse == true and
  .sourceNetwork.qdiscDrops == {minimum: 11, maximum: 13} and
  .sourceNetwork.repairedRTX == {minimum: 9, maximum: 11}
' "${temporary_directory}/loss-summary.json" >/dev/null

jq -s '.[1].sourceNetwork.scope = "distributor-to-browser"' \
  "${temporary_directory}/capacity.jsonl" >"${temporary_directory}/wrong-scope.json"
jq --argjson run_count 3 -f "${script_directory}/report.jq" \
  "${temporary_directory}/wrong-scope.json" | jq -e '
    .passed == false and .gates.producerToAdapterOnly == false
  ' >/dev/null

jq -s '.[2].adapter.repaired_rtx = 0 | .[2].adapter.repaired_fec = 0' \
  "${temporary_directory}/loss.jsonl" >"${temporary_directory}/unrepaired.json"
jq --argjson run_count 3 -f "${script_directory}/report.jq" \
  "${temporary_directory}/unrepaired.json" | jq -e '
    .passed == false and .gates.repairResponse == false
  ' >/dev/null

printf 'Producer-to-adapter report tests passed\n'
