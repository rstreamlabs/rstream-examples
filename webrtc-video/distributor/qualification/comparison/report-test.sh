#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture="$(mktemp "${TMPDIR:-/tmp}/rstream-comparison-report.XXXXXX")"
loss_fixture="${fixture}.loss"
trap 'rm -f "${fixture}" "${loss_fixture}"' EXIT INT TERM

jq -n '
  def resources(mode): {
    components: ({
      producer: {samples: 3, cpuCoreRatio: {average: 0.4, p95: 0.5, maximum: 0.5}, residentBytes: {p95: 90, maximum: 100}},
      browser: {samples: 3, cpuCoreRatio: {average: 0.8, p95: 1.0, maximum: 1.0}, residentBytes: {p95: 180, maximum: 200}}
    } + if mode == "mediamtx" then {
      distributor: {samples: 3, cpuCoreRatio: {average: 0.2, p95: 0.25, maximum: 0.25}, residentBytes: {p95: 45, maximum: 50}, tasks: {maximum: 2}}
    } else {} end)
  };
  def result(mode; passed): {
    revision: "revision",
    mode: mode,
    workingTreeDirty: false,
    executionStatus: (if passed then 0 else 1 end),
    passed: passed,
    setupMilliseconds: (if mode == "direct" then 500 else 750 end),
    setup: {
      whepPostDurationMilliseconds: (if mode == "direct" then 100 else 150 end),
      postToConnectedMilliseconds: (if mode == "direct" then 300 else 450 end),
      peerToFirstDecodedFrameMilliseconds: (if mode == "direct" then 500 else 750 end)
    },
    teardown: {whepDeleteDurationMilliseconds: 125},
    profile: {phaseSeconds: 15, recoverySeconds: 45, viewerNetwork: {capacityKbps: 4000, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 256}},
    phases: {
      baseline: {averageDecodedQP: 24, encoderTargetKbps: {last: 8000}},
      viewerNetwork: {
        averageDecodedQP: (if mode == "direct" then 28 else 24 end),
        receivedBitrateKbps: (if mode == "direct" then 3000 else 2000 end),
        decodedFramesPerSecond: (if mode == "direct" then 30 else 1 end),
        nacks: 10,
        packetsLostNetChange: (if mode == "direct" then 2 else 1000 end),
        freezeDurationSeconds: (if mode == "direct" then 0.1 else 10 end),
        encoderTargetKbps: {last: (if mode == "direct" then 2000 else 8000 end)}
      },
      recovery: {averageDecodedQP: 24, encoderTargetKbps: {last: 8000, medianLast10Seconds: 7000, sustainedMinimumLast10Seconds: 7000}}
    },
    resources: resources(mode)
  };
  [range(0; 3) as $run | result("direct"; true), result("mediamtx"; false)]
' >"${fixture}"

jq \
  --argjson run_count 3 \
  -f "${script_directory}/report.jq" \
  "${fixture}" | jq -e '
    .passed == true and
    .publishable == true and
    .direct.runs == 3 and
    .mediamtx.runs == 3 and
    .direct.setup.whepPostDurationMilliseconds == {minimum: 100, maximum: 100} and
    .mediamtx.teardown.whepDeleteDurationMilliseconds == {minimum: 125, maximum: 125} and
    .direct.visualQuality.viewerNetworkAverageDecodedQP == {minimum: 28, maximum: 28} and
    .direct.sourceAdaptation.adaptedRuns == 3 and
    .mediamtx.sourceAdaptation.adaptedRuns == 0 and
    .verdict.directProfileQualified == true and
    .verdict.directAdaptive == true and
    .verdict.mediaMTXProfileQualified == false and
    .verdict.mediaMTXSingleRenditionAdaptive == false and
    .verdict.mediaMTXSourceRespondsToViewerTWCC == false and
    .verdict.mediaMTXRequiresRenditionStrategy == true
  ' >/dev/null

jq 'map(
  .profile.viewerNetwork.capacityKbps = 0 |
  if .mode == "mediamtx" then .passed = true | .executionStatus = 0 else . end
)' "${fixture}" >"${loss_fixture}"
jq \
  --argjson run_count 3 \
  -f "${script_directory}/report.jq" \
  "${loss_fixture}" | jq -e '
    .passed == true and
    .gates.directSourceAdapted == true and
    .gates.directSourceRecovered == true and
    .verdict.directProfileQualified == true and
    .verdict.directAdaptive == null and
    .verdict.mediaMTXProfileQualified == true and
    .verdict.mediaMTXSingleRenditionAdaptive == null and
    .verdict.mediaMTXSourceRespondsToViewerTWCC == null and
    .verdict.mediaMTXRequiresRenditionStrategy == false
  ' >/dev/null

printf 'Comparison report tests passed\n'
