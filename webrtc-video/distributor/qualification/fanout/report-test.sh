#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-fanout-report-test.XXXXXX")"
cleanup() {
  find "${temporary_directory}" -type f -delete
  rmdir "${temporary_directory}"
}
trap cleanup EXIT

for run in 1 2 3; do
  jq -n --argjson run "${run}" '{
    mediaMTXVersion: "v1.20.0",
    sourceSessions: 1,
    sourceDeletes: 1,
    sourceTWCCPackets: (100 + $run),
    readerLimit: 8,
    saturationRejected: true,
    saturationRejectMilliseconds: (10 + $run),
    saturationViewerPayloadRatio: [1, 1, 1, 1, 1, 1, 1, 1],
    firstViewerSetupMilliseconds: (20 + $run),
    warmViewerSetupP95Milliseconds: (5 + $run),
    churnSetupP95Milliseconds: (3 + $run),
    process: {
      peakResidentBytes: (100000000 + $run),
      cpuCoreRatio: (0.2 + ($run / 100)),
      peakProcesses: 2
    },
    phases: [
      {readers: 1, inboundBitsPerSecond: (8000000 + $run), outboundBitsPerSecond: (8000000 + $run)},
      {readers: 4, inboundBitsPerSecond: (8000000 + $run), outboundBitsPerSecond: (32000000 + $run)},
      {readers: 8, inboundBitsPerSecond: (8000000 + $run), outboundBitsPerSecond: (64000000 + $run)}
    ],
    passed: true
  }' >"${temporary_directory}/${run}.json"
done

jq -s \
  --arg revision test-revision \
  --argjson working_tree_dirty false \
  -f "${script_directory}/report.jq" \
  "${temporary_directory}"/*.json >"${temporary_directory}/summary.json"

jq -e '
  .passed == true and
  .publishable == true and
  .runs == 3 and
  .readerLimits == [8] and
  .saturation.rejected == true and
  .saturation.rejectMilliseconds == {minimum: 11, maximum: 13} and
  .saturation.existingViewerPayloadRatio == {minimum: 1, maximum: 1} and
  [.phases[].readers] == [1, 4, 8] and
  ([.phases[] | .inboundBitsPerSecond.minimum, .inboundBitsPerSecond.maximum, .outboundBitsPerSecond.minimum, .outboundBitsPerSecond.maximum] | all(. != null)) and
  .phases[0].inboundBitsPerSecond == {minimum: 8000001, maximum: 8000003} and
  .phases[2].outboundBitsPerSecond == {minimum: 64000001, maximum: 64000003}
' "${temporary_directory}/summary.json" >/dev/null
