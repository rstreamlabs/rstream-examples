#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-native-report-test.XXXXXX")"
cleanup() {
  find "${temporary_directory}" -type f -delete
  rmdir "${temporary_directory}"
}
trap cleanup EXIT

for run in 1 2 3; do
  jq -n --argjson run "${run}" '{
    revision: "test-revision",
    mode: "mediamtx-native",
    profile: {edgeAuthentication: true},
    images: {producer: "producer-image", distributor: "mediamtx-image", browser: "browser-image"},
    framesDecoded: (300 + $run),
    framesDropped: 0,
    freezeCount: 0,
    totalFreezesDurationSeconds: 0,
    bytesReceived: (10000000 + $run),
    producerTWCCFeedbackPackets: (200 + $run),
    producerPacerSentFEC: 0,
    nativeSourceProfile: {
      required: true,
      activeSessions: 1,
      createdSessions: 1,
      negotiated: {twcc: 1, nack: 1, rtx: 0, flexfec: 0},
      fixedSourcePacing: {adaptiveUpdates: 0, adaptiveFailures: 0, queueDrops: 0, mediaFrameDrops: 0},
      activeAfterTeardown: 0
    },
    peerConnection: {twccNegotiated: true, nackNegotiated: true, rtxNegotiated: false, flexFECNegotiated: false},
    setup: {peerToFirstDecodedFrameMilliseconds: (4000 + $run), whepPostDurationMilliseconds: (2000 + $run), postToConnectedMilliseconds: (50 + $run)},
    teardown: {whepDeleteDurationMilliseconds: (10 + $run)},
    resources: {components: {
      browser: {samples: 5, cpuCoreRatio: {maximum: 0.2}, residentBytes: {maximum: 400000000}, tasks: {maximum: 100}},
      distributor: {samples: 5, cpuCoreRatio: {maximum: 0.05}, residentBytes: {maximum: 32000000}, tasks: {maximum: 15}},
      producer: {samples: 5, cpuCoreRatio: {maximum: 0.6}, residentBytes: {maximum: 72000000}, tasks: {maximum: 35}}
    }},
    gates: {media: true, playback: true, sourceFeedback: true},
    passed: true
  }' | jq --argjson status 0 --argjson run "${run}" '{run: $run, status: $status, result: .}' >>"${temporary_directory}/records.jsonl"
done

jq -s \
  --arg revision test-revision \
  --argjson working_tree_dirty false \
  --argjson requested_runs 3 \
  -f "${script_directory}/report.jq" \
  "${temporary_directory}/records.jsonl" >"${temporary_directory}/summary.json"
jq -e '
  .passed == true and
  .publishable == true and
  .completedRuns == 3 and
  .media.framesDecoded == {minimum: 301, maximum: 303} and
  .setupMilliseconds.sourceToFirstFrame == {minimum: 4001, maximum: 4003} and
  .resources.mediaMTXPeakResidentBytes == {minimum: 32000000, maximum: 32000000}
' "${temporary_directory}/summary.json" >/dev/null
jq -s '.[1].result.freezeCount = 1 | .[1].result.gates.playback = false | .[1].result.passed = false' \
  "${temporary_directory}/records.jsonl" >"${temporary_directory}/failed-records.json"
jq \
  --arg revision test-revision \
  --argjson working_tree_dirty false \
  --argjson requested_runs 3 \
  -f "${script_directory}/report.jq" \
  "${temporary_directory}/failed-records.json" >"${temporary_directory}/failed-summary.json"
jq -e '.passed == false and .gates.mediaDeliveredWithoutLoss == false and .gates.allResultsPassed == false' \
  "${temporary_directory}/failed-summary.json" >/dev/null
