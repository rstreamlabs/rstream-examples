#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-end-to-end-result.XXXXXX")"
trap 'rm -rf "${fixture_directory}"' EXIT INT TERM

jq -n '{peerConnection: {nackNegotiated: true, twccNegotiated: true, rtxNegotiated: true, flexFECNegotiated: true}}' >"${fixture_directory}/browser.json"
jq -n '{
  iceRestartOffers: 0,
  whepSessionCreates: 1,
  whepSessionDeletes: 1,
  whepFailedRequests: 0,
  events: [
    {elapsedMilliseconds: 100, kind: "peer-created"},
    {durationMilliseconds: 200, elapsedMilliseconds: 350, kind: "whep-request", method: "POST", status: 201},
    {elapsedMilliseconds: 750, kind: "connectionstatechange", state: "connected"},
    {elapsedMilliseconds: 800, kind: "playback-ready"},
    {elapsedMilliseconds: 850, kind: "first-decoded-frame"},
    {durationMilliseconds: 125, elapsedMilliseconds: 16000, kind: "whep-request", method: "DELETE", status: 200}
  ]
}' >"${fixture_directory}/signaling.json"
jq -n '{components: {producer: {samples: 2}, browser: {samples: 2}}}' >"${fixture_directory}/resources.json"
jq -n '{enabled: true, capacityKbps: 4000, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 256, qdisc: {packets: 100, drops: 0}}' >"${fixture_directory}/network.json"
jq -n '{}' >"${fixture_directory}/adapter.json"
jq -n '{required: false}' >"${fixture_directory}/native-source-profile.json"
jq -nc '
  def sample(phase; elapsed; frames; bytes; target): {
    phase: phase,
    elapsedMilliseconds: elapsed,
    framesDecoded: frames,
    qpSum: (frames * 25),
    bytesReceived: bytes,
    framesPerSecond: 30,
    encoderTargetKbps: target,
    twccTargetKbps: target,
    jitterSeconds: 0.01,
    currentRoundTripTimeSeconds: 0.02,
    nackCount: 0,
    packetsLost: 0,
    framesDropped: 0,
    freezeCount: 0,
    totalFreezesDurationSeconds: 0,
    adaptiveBitrateFailures: 0,
    adaptiveBitrateUpdates: 1,
    delayControllerDecreaseSessions: 0,
    delayControllerHoldSessions: 0,
    delayControllerIncreaseSessions: 1,
    delayControllerNormalSessions: 1,
    delayControllerOveruseSessions: 0,
    delayControllerUnderuseSessions: 0,
    delayTargetKbps: target,
    lossAverage: 0,
    lossGuardActive: false,
    lossGuardRecoveries: 0,
    lossGuardReductions: 0,
    lossGuardTargetKbps: 0,
    lossTargetKbps: target,
    pacerPacingBitrateKbps: target,
    pacerTargetBitrateKbps: target,
    twccFeedbackPackets: 1,
    pacerSentFEC: 1,
    producerMetricsSource: "openmetrics"
  };
  sample("baseline"; 0; 0; 0; 8000),
  sample("baseline"; 1000; 30; 1000000; 8000),
  sample("viewer-network"; 2000; 60; 2000000; 2000),
  sample("viewer-network"; 3000; 90; 3000000; 2000),
  sample("recovery"; 4000; 120; 4000000; 7000),
  sample("recovery"; 15000; 450; 15000000; 7000)
' >"${fixture_directory}/samples.jsonl"

render_result() {
  local mode="${2:-direct}"
  local browser="${3:-${fixture_directory}/browser.json}"
  local native_source_profile="${4:-${fixture_directory}/native-source-profile.json}"
  local network="${5:-${fixture_directory}/network.json}"
  jq -s \
  --arg revision revision \
  --arg mode "${mode}" \
  --argjson edge_auth true \
  --argjson connect_token_ttl_seconds 300 \
  --argjson working_tree_dirty false \
  --arg producer_image producer \
  --arg distributor_image '' \
  --arg browser_image browser \
  --slurpfile adapter "${fixture_directory}/adapter.json" \
  --slurpfile browser "${browser}" \
  --slurpfile signaling "${fixture_directory}/signaling.json" \
  --slurpfile resources "${fixture_directory}/resources.json" \
  --argjson warmup_seconds 20 \
  --argjson phase_seconds 1 \
  --argjson recovery_seconds 1 \
  --slurpfile viewer_network "${network}" \
  --slurpfile native_source_profile "${native_source_profile}" \
  -f "${script_directory}/result.jq" \
  "$1"
}

render_result "${fixture_directory}/samples.jsonl" | jq -e '
    .passed == true and
    .profile.edgeAuthentication == true and
    .profile.edgeCredentialLifetimeSeconds == 300 and
    .profile.warmupSeconds == 20 and
    .profile.recoverySeconds == 1 and
    .setupMilliseconds == 750 and
    .setup.whepPostDurationMilliseconds == 200 and
    .setup.postToConnectedMilliseconds == 400 and
    .setup.peerToFirstDecodedFrameMilliseconds == 750 and
    .teardown.whepDeleteDurationMilliseconds == 125 and
    .gates.qualityEvidence == true and
    .phases.baseline.averageDecodedQP == 25 and
    .phases.recovery.encoderTargetKbps.sustainedMinimumLast10Seconds == 7000 and
    .phases.recovery.encoderTargetKbps.medianLast10Seconds == 7000 and
    .viewerNetwork.framesDecodedDelta == 30 and
    .viewerNetwork.packetsLostNetChange == 0 and
    .viewerNetwork.recoveryFramesDecodedDelta == 330
  ' >/dev/null

jq -n '{peerConnection: {nackNegotiated: true, twccNegotiated: true, rtxNegotiated: false, flexFECNegotiated: false}}' \
  >"${fixture_directory}/native-browser.json"
jq -n '{enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 0, qdisc: null}' \
  >"${fixture_directory}/native-network.json"
jq -n '{
  required: true,
  activeSessions: 1,
  createdSessions: 1,
  negotiated: {twcc: 1, nack: 1, rtx: 0, flexfec: 0},
  fixedSourcePacing: {adaptiveUpdates: 0, adaptiveFailures: 0, queueDrops: 0, mediaFrameDrops: 0},
  activeAfterTeardown: 0
}' >"${fixture_directory}/native-source-profile-qualified.json"
jq -c '.pacerSentFEC = 0 | .framesDropped = 7 | .freezeCount = 1 | .totalFreezesDurationSeconds = 0.238' \
  "${fixture_directory}/samples.jsonl" >"${fixture_directory}/native-samples.jsonl"
render_result \
  "${fixture_directory}/native-samples.jsonl" \
  mediamtx-native \
  "${fixture_directory}/native-browser.json" \
  "${fixture_directory}/native-source-profile-qualified.json" \
  "${fixture_directory}/native-network.json" | jq -e '
    .passed == true and
    .framesDropped == 7 and
    .freezeCount == 1 and
    .totalFreezesDurationSeconds == 0.238 and
    .phases.baseline.framesDropped == 0 and
    .phases.baseline.freezes == 0 and
    .phases.baseline.freezeDurationSeconds == 0 and
    .gates.playback == true and
    .gates.sourceFeedback == true and
    .gates.adapterIntegrity == true and
    .gates.nativeSourceLifecycle == true and
    .nativeSourceProfile.activeAfterTeardown == 0
  ' >/dev/null

jq -c 'del(.delayTargetKbps)' \
  "${fixture_directory}/samples.jsonl" \
  >"${fixture_directory}/incomplete-samples.jsonl"
render_result "${fixture_directory}/incomplete-samples.jsonl" | jq -e '
  .gates.producerMetrics == false and
  .producerMetricsSamples == .samples and
  .producerMetricsCompleteSamples == 0 and
  .passed == false
' >/dev/null

jq -c '
  if .phase == "viewer-network" and .elapsedMilliseconds == 3000 then
    .framesDecoded = 83 |
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.03
  elif .phase == "recovery" then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.03
  else . end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/degraded-samples.jsonl"
render_result "${fixture_directory}/degraded-samples.jsonl" | jq -e '
  .phases.viewerNetwork.decodedFramesPerSecond == 23 and
  .viewerNetwork.freezeRatio == 0.03 and
  .gates.playback == false and
  .gates.viewerNetworkRecovery == false and
  .passed == false
' >/dev/null

printf 'End-to-end result tests passed\n'
