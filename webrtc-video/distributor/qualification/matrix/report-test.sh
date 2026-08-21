#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-matrix-report.XXXXXX")"
trap 'rm -rf "${temporary_directory}"' EXIT INT TERM

jq -n '
  def mode(passed; adaptive): {
    passedRuns: (if passed then 3 else 0 end),
    setup: {peerToFirstDecodedFrameMilliseconds: {minimum: 500, maximum: 750}},
    teardown: {whepDeleteDurationMilliseconds: {minimum: 100, maximum: 150}},
    visualQuality: {viewerNetworkAverageDecodedQP: {minimum: 24, maximum: 28}}
  };
  {
    revision: "revision",
    runsPerMode: 3,
    profile: {edgeAuthentication: true, viewerNetwork: {capacityKbps: 4000, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0}},
    direct: mode(true; true),
    mediamtx: mode(false; false),
    gates: {directSourceAdapted: true, directSourceRecovered: true, resourceReportsComplete: true},
    verdict: {
      directProfileQualified: true,
      directAdaptive: true,
      mediaMTXProfileQualified: false,
      mediaMTXSingleRenditionAdaptive: false,
      mediaMTXSourceRespondsToViewerTWCC: false,
      mediaMTXRequiresRenditionStrategy: true
    },
    passed: true,
    publishable: true
  }
' >"${temporary_directory}/capacity.json"

jq '
  .profile.viewerNetwork = {capacityKbps: 0, delayMilliseconds: 60, jitterMilliseconds: 15, lossPercent: 1} |
  .mediamtx.passedRuns = 3 |
  .verdict = {
    directProfileQualified: true,
    directAdaptive: null,
    mediaMTXProfileQualified: true,
    mediaMTXSingleRenditionAdaptive: null,
    mediaMTXSourceRespondsToViewerTWCC: null,
    mediaMTXRequiresRenditionStrategy: false
  }
' "${temporary_directory}/capacity.json" >"${temporary_directory}/impairment.json"

jq -n '{
  revision: "revision",
  runs: 3,
  phases: [
    {readers: 1},
    {readers: 4},
    {readers: 8}
  ],
  process: {peakResidentBytes: {maximum: 50000000}, cpuCoreRatio: {maximum: 0.2}},
  passed: true,
  publishable: true
}' >"${temporary_directory}/fanout.json"

jq -n \
  --argjson capacity_status 0 \
  --argjson impairment_status 0 \
  --argjson fanout_status 0 \
  --slurpfile capacity "${temporary_directory}/capacity.json" \
  --slurpfile impairment "${temporary_directory}/impairment.json" \
  --slurpfile fanout "${temporary_directory}/fanout.json" \
  -f "${script_directory}/report.jq" | jq -e '
    .passed == true and
    .publishable == true and
    .gates.directCapacityQualified == true and
    .gates.mediaMTXImpairmentQualified == true and
    .gates.adaptiveBoundaryDemonstrated == true and
    .productVerdict.direct == "go" and
    .productVerdict.mediaMTXHeterogeneousAdaptive == "no-go"
  ' >/dev/null

jq '.verdict.mediaMTXProfileQualified = false | .passed = false | .publishable = false' \
  "${temporary_directory}/impairment.json" >"${temporary_directory}/impairment-failed.json"
jq -n \
  --argjson capacity_status 0 \
  --argjson impairment_status 1 \
  --argjson fanout_status 0 \
  --slurpfile capacity "${temporary_directory}/capacity.json" \
  --slurpfile impairment "${temporary_directory}/impairment-failed.json" \
  --slurpfile fanout "${temporary_directory}/fanout.json" \
  -f "${script_directory}/report.jq" | jq -e '
    .passed == false and
    .gates.mediaMTXImpairmentQualified == false
  ' >/dev/null

jq '.gates.directSourceRecovered = false | .passed = false | .publishable = false' \
  "${temporary_directory}/impairment.json" >"${temporary_directory}/impairment-slow-recovery.json"
jq -n \
  --argjson capacity_status 0 \
  --argjson impairment_status 1 \
  --argjson fanout_status 0 \
  --slurpfile capacity "${temporary_directory}/capacity.json" \
  --slurpfile impairment "${temporary_directory}/impairment-slow-recovery.json" \
  --slurpfile fanout "${temporary_directory}/fanout.json" \
  -f "${script_directory}/report.jq" | jq -e '
    .passed == false and
    .gates.directImpairmentQualified == false and
    .productVerdict.direct == "no-go"
  ' >/dev/null

jq '.profile.edgeAuthentication = false' \
  "${temporary_directory}/capacity.json" >"${temporary_directory}/capacity-unauthenticated.json"
jq -n \
  --argjson capacity_status 0 \
  --argjson impairment_status 0 \
  --argjson fanout_status 0 \
  --slurpfile capacity "${temporary_directory}/capacity-unauthenticated.json" \
  --slurpfile impairment "${temporary_directory}/impairment.json" \
  --slurpfile fanout "${temporary_directory}/fanout.json" \
  -f "${script_directory}/report.jq" | jq -e '
    .passed == false and
    .publishable == false and
    .gates.edgeAuthenticationEnabled == false
  ' >/dev/null

jq -n \
  --argjson capacity_status 1 \
  --argjson impairment_status 0 \
  --argjson fanout_status 0 \
  --slurpfile capacity "${temporary_directory}/capacity.json" \
  --slurpfile impairment "${temporary_directory}/impairment.json" \
  --slurpfile fanout "${temporary_directory}/fanout.json" \
  -f "${script_directory}/report.jq" | jq -e '
    .passed == false and
    .gates.runnerStatusMatchesReports == false
  ' >/dev/null

printf 'Distribution matrix report tests passed\n'
