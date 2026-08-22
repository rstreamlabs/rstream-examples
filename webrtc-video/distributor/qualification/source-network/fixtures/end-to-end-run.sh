#!/usr/bin/env bash
set -Eeuo pipefail

output_directory=$1
run="$(basename "${output_directory}")"
status=0
passed=true
if [[ "${RSTREAM_TEST_FAIL_RUN:-}" == "${run}" ]]; then
  status=1
  passed=false
fi
mkdir -p "${output_directory}"
jq -n \
  --argjson capacity "${RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS}" \
  --argjson loss "${RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT}" \
  --argjson passed "${passed}" \
  --argjson run "${run}" '{
    revision: "revision",
    mode: "mediamtx",
    workingTreeDirty: false,
    passed: $passed,
    images: {producer: "producer-image", distributor: "distributor-image", browser: "browser-image"},
    profile: {
      edgeAuthentication: true,
      sourceNetwork: {enabled: true, capacityKbps: $capacity, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: $loss},
      viewerNetwork: {enabled: false}
    },
    sourceNetwork: {
      scope: "producer-to-adapter",
      destination: {port: null},
      qdisc: {packets: (1000 + $run), drops: (if $loss > 0 then 10 else 0 end)},
      twccFeedbackPacketsDelta: 50,
      adaptiveBitrateUpdatesDelta: (if $capacity > 0 then 4 else 0 end),
      pacerSentRetransmissionDelta: (if $loss > 0 then 10 else 0 end),
      pacerSentFECDelta: (if $loss > 0 then 3 else 0 end),
      frameDropRatio: 0,
      freezeRatio: 0
    },
    adapter: {repaired_rtx: (if $loss > 0 then 8 else 0 end), repaired_fec: (if $loss > 0 then 2 else 0 end)},
    phases: {
      baseline: {encoderTargetKbps: {medianLast10Seconds: 8000}, averageDecodedQP: 22},
      sourceNetwork: {encoderTargetKbps: {medianLast10Seconds: (if $capacity > 0 then 3800 else 7600 end)}, decodedFramesPerSecond: 30, averageDecodedQP: 25},
      recovery: {encoderTargetKbps: {medianLast10Seconds: 7600}, averageDecodedQP: 22}
    },
    setup: {peerToFirstDecodedFrameMilliseconds: 500},
    resources: {components: {
      browser: {samples: 5, residentBytes: {maximum: 400000000}},
      distributor: {samples: 5, cpuCoreRatio: {maximum: 0.2}, residentBytes: {maximum: 50000000}},
      producer: {samples: 5, cpuCoreRatio: {maximum: 0.6}, residentBytes: {maximum: 80000000}}
    }},
    gates: {
      sourceNetworkCausality: $passed,
      sourceNetworkResponse: $passed,
      sourceTargetRecovery: $passed,
      playback: $passed,
      runtimeMediaIntegrity: true,
      adapterIntegrity: true,
      resourceLifecycle: true,
      performanceEnvironment: true
    }
  }' >"${output_directory}/result.json"
exit "${status}"
