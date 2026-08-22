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
jq -n '{enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 0, qdisc: null, filters: []}' >"${fixture_directory}/source-network.json"
jq -n '{invalid_fec: 0, reorder_late: 0, reorder_skipped: 0, reorder_discarded: 0, discontinuities: 0, key_frame_requests: 0, damaged_source_frames_dropped: 0, damaged_source_packets_dropped: 0, repaired_rtx: 0, repaired_fec: 1, expired: 0}' \
  >"${fixture_directory}/adapter.json"
jq -n '{fatalErrors: 0, h264PacketizationErrors: 0, packetLossWarnings: 0}' >"${fixture_directory}/runtime-health.json"
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
    encodedKeyFrames: 1,
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
    pacerSentRetransmission: 0,
    pacerTargetBitrateKbps: target,
    recoveryKeyFrameCoalesced: 0,
    recoveryKeyFrameFailures: 0,
    recoveryKeyFrameRequests: 1,
    rtcpKeyFrameRequests: 1,
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
  local source_network="${6:-${fixture_directory}/source-network.json}"
  local adapter="${7:-${fixture_directory}/adapter.json}"
  jq -s \
  --arg revision revision \
  --arg mode "${mode}" \
  --argjson edge_auth true \
  --argjson connect_token_ttl_seconds 300 \
  --argjson working_tree_dirty false \
  --arg producer_image producer \
  --arg distributor_image '' \
  --arg browser_image browser \
  --slurpfile adapter "${adapter}" \
  --slurpfile runtime_health "${fixture_directory}/runtime-health.json" \
  --slurpfile browser "${browser}" \
  --slurpfile signaling "${fixture_directory}/signaling.json" \
  --slurpfile resources "${fixture_directory}/resources.json" \
  --argjson warmup_seconds 20 \
  --argjson phase_seconds 1 \
  --argjson recovery_seconds 1 \
  --argjson flexfec_media_packets 5 \
  --argjson flexfec_repair_packets 1 \
  --argjson playout_delay_hint_seconds 0.2 \
  --slurpfile viewer_network "${network}" \
  --slurpfile source_network "${source_network}" \
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
    .profile.flexFEC == {mediaPackets: 5, repairPackets: 1} and
    .profile.playoutDelayHintSeconds == 0.2 and
    .profile.acceptance.capacityTransitionGraceMilliseconds == 4000 and
    .profile.acceptance.maximumCapacityTransitionFreezeSeconds == 3 and
    .profile.acceptance.maximumContinuousImpairmentFreezeRatio == 0.02 and
    .profile.acceptance.maximumDroppedFrameRatio == 0.01 and
    .profile.acceptance.minimumFrameRateRatio == 0.8 and
    .setupMilliseconds == 750 and
    .setup.whepPostDurationMilliseconds == 200 and
    .setup.postToConnectedMilliseconds == 400 and
    .setup.peerToFirstDecodedFrameMilliseconds == 750 and
    .teardown.whepDeleteDurationMilliseconds == 125 and
    .gates.qualityEvidence == true and
    .gates.runtimeMediaIntegrity == true and
    .phases.baseline.averageDecodedQP == 25 and
    .phases.recovery.encoderTargetKbps.sustainedMinimumLast10Seconds == 7000 and
    .phases.recovery.encoderTargetKbps.medianLast10Seconds == 7000 and
    .viewerNetwork.framesDecodedDelta == 30 and
    .viewerNetwork.packetsLostNetChange == 0 and
    .viewerNetwork.recoveryFramesDecodedDelta == 330
  ' >/dev/null

jq -n '{enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 0, qdisc: null, filters: []}' \
  >"${fixture_directory}/viewer-network-disabled.json"
jq -n '{peerConnection: {nackNegotiated: true, twccNegotiated: true, rtxNegotiated: false, flexFECNegotiated: false}}' \
  >"${fixture_directory}/distributed-browser.json"
jq -n '{
  enabled: true,
  scope: "producer-to-adapter",
  destination: {ip: "172.18.0.3", port: null},
  capacityKbps: 4000,
  delayMilliseconds: 0,
  jitterMilliseconds: 0,
  lossPercent: 0,
  queuePackets: 256,
  qdisc: {packets: 100, drops: 0},
  filters: []
}' >"${fixture_directory}/source-network-qualified.json"
jq -c '
  .phase |= if . == "viewer-network" then "source-network" else . end |
  .adaptiveBitrateUpdates = (.elapsedMilliseconds / 1000 | floor) |
  .twccFeedbackPackets = (.elapsedMilliseconds / 1000 | floor)
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/source-network-samples.jsonl"
render_result \
  "${fixture_directory}/source-network-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-network-qualified.json" | jq -e '
    .networkImpairment.phase == "source-network" and
    .sourceNetwork.scope == "producer-to-adapter" and
    .sourceNetwork.destination.port == null and
    .sourceNetwork.twccFeedbackPacketsDelta > 0 and
    .sourceNetwork.adaptiveBitrateUpdatesDelta > 0 and
    .gates.sourceNetworkCausality == true and
    .gates.sourceNetworkResponse == true and
    .gates.sourceTargetRecovery == true and
    .passed == true
  ' >/dev/null

jq -c '
  if .phase == "source-network" and .elapsedMilliseconds == 3000 then
    .framesDropped = 1
  else . end
' "${fixture_directory}/source-network-samples.jsonl" >"${fixture_directory}/excessive-frame-drop-samples.jsonl"
render_result \
  "${fixture_directory}/excessive-frame-drop-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-network-qualified.json" | jq -e '
    .sourceNetwork.frameDropRatio > .profile.acceptance.maximumDroppedFrameRatio and
    .gates.sourceNetworkResponse == false and
    .passed == false
  ' >/dev/null

jq '.reorder_skipped = 10 | .expired = 8 | .reorder_late = 2 | .key_frame_requests = 1 | .damaged_source_frames_dropped = 1 | .damaged_source_packets_dropped = 4' \
  "${fixture_directory}/adapter.json" >"${fixture_directory}/adapter-accounted-loss.json"
render_result \
  "${fixture_directory}/source-network-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-network-qualified.json" \
  "${fixture_directory}/adapter-accounted-loss.json" | jq -e '
    .gates.adapterIntegrity == true and
    .passed == true
  ' >/dev/null

jq '.reorder_skipped = 11' \
  "${fixture_directory}/adapter-accounted-loss.json" >"${fixture_directory}/adapter-unaccounted-loss.json"
render_result \
  "${fixture_directory}/source-network-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-network-qualified.json" \
  "${fixture_directory}/adapter-unaccounted-loss.json" | jq -e '
    .gates.adapterIntegrity == false and
    .passed == false
  ' >/dev/null

jq -n '{fatalErrors: 0, h264PacketizationErrors: 1, packetLossWarnings: 0}' >"${fixture_directory}/runtime-health.json"
render_result "${fixture_directory}/samples.jsonl" | jq -e '
  .runtimeHealth.h264PacketizationErrors == 1 and
  .gates.runtimeMediaIntegrity == false and
  .passed == false
' >/dev/null
jq -n '{fatalErrors: 0, h264PacketizationErrors: 0, packetLossWarnings: 0}' >"${fixture_directory}/runtime-health.json"

jq -n '{fatalErrors: 0, h264PacketizationErrors: 0, packetLossWarnings: 1}' >"${fixture_directory}/runtime-health.json"
render_result "${fixture_directory}/samples.jsonl" | jq -e '
  .runtimeHealth.packetLossWarnings == 1 and
  .gates.runtimeMediaIntegrity == false and
  .passed == false
' >/dev/null
jq -n '{fatalErrors: 0, h264PacketizationErrors: 0, packetLossWarnings: 0}' >"${fixture_directory}/runtime-health.json"

jq -n '{
  enabled: true,
  scope: "producer-to-adapter",
  destination: {ip: "172.18.0.3", port: null},
  capacityKbps: 0,
  delayMilliseconds: 0,
  jitterMilliseconds: 0,
  lossPercent: 1,
  queuePackets: 256,
  qdisc: {packets: 1000, drops: 10},
  filters: []
}' >"${fixture_directory}/source-loss-qualified.json"
jq -c '
  .phase |= if . == "viewer-network" then "source-network" else . end |
  .adaptiveBitrateUpdates = 0 |
  .twccFeedbackPackets = (.elapsedMilliseconds / 1000 | floor) |
  .pacerSentRetransmission = (.elapsedMilliseconds / 1000 | floor) |
  .encoderTargetKbps = 8000 |
  .twccTargetKbps = 8000
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/source-loss-samples.jsonl"
render_result \
  "${fixture_directory}/source-loss-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-loss-qualified.json" | jq -e '
    .sourceNetwork.qdisc.drops == 10 and
    .sourceNetwork.pacerSentRetransmissionDelta > 0 and
    .sourceNetwork.adaptiveBitrateUpdatesDelta == 0 and
    .phases.sourceNetwork.encoderTargetKbps.medianLast10Seconds == 8000 and
    .gates.sourceNetworkCausality == true and
    .gates.sourceNetworkResponse == true and
    .passed == true
  ' >/dev/null

jq '.repaired_rtx = 0 | .repaired_fec = 0' \
  "${fixture_directory}/adapter.json" >"${fixture_directory}/adapter-without-repairs.json"
render_result \
  "${fixture_directory}/source-loss-samples.jsonl" \
  mediamtx \
  "${fixture_directory}/distributed-browser.json" \
  "${fixture_directory}/native-source-profile.json" \
  "${fixture_directory}/viewer-network-disabled.json" \
  "${fixture_directory}/source-loss-qualified.json" \
  "${fixture_directory}/adapter-without-repairs.json" | jq -e '
    .sourceNetwork.qdisc.drops == 10 and
    .sourceNetwork.pacerSentRetransmissionDelta > 0 and
    .adapter.repaired_rtx == 0 and
    .adapter.repaired_fec == 0 and
    .gates.sourceNetworkCausality == false and
    .passed == false
  ' >/dev/null

jq -c '
  if .elapsedMilliseconds >= 3000 then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.8
  else . end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/bounded-transition-samples.jsonl"
render_result "${fixture_directory}/bounded-transition-samples.jsonl" | jq -e '
  .viewerNetwork.freezeDurationDeltaSeconds == 0.8 and
  .viewerNetwork.steadyStateFreezeDurationDeltaSeconds == 0 and
  .gates.playback == true and
  .gates.viewerNetworkRecovery == true and
  .passed == true
' >/dev/null

jq -c '
  if .phase == "recovery" then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.3
  else . end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/bounded-recovery-transition-samples.jsonl"
render_result "${fixture_directory}/bounded-recovery-transition-samples.jsonl" | jq -e '
  .viewerNetwork.recoveryFreezeDurationDeltaSeconds == 0.3 and
  .viewerNetwork.steadyRecoveryFreezeDurationDeltaSeconds == 0 and
  .gates.playback == true and
  .gates.viewerNetworkRecovery == true and
  .passed == true
' >/dev/null

jq -c '
  if .phase == "recovery" and .elapsedMilliseconds >= 15000 then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.3
  else . end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/late-recovery-samples.jsonl"
render_result "${fixture_directory}/late-recovery-samples.jsonl" | jq -e '
  .viewerNetwork.recoveryFreezeDurationDeltaSeconds == 0.3 and
  .viewerNetwork.steadyRecoveryFreezeDurationDeltaSeconds == 0.3 and
  .gates.playback == false and
  .gates.viewerNetworkRecovery == false and
  .passed == false
' >/dev/null

jq -c '
  (if .phase == "recovery" then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 0.2
  else . end),
  if .phase == "viewer-network" and .elapsedMilliseconds == 3000 then
    (. + {
      elapsedMilliseconds: 5000,
      framesDecoded: 120,
      qpSum: 3000,
      bytesReceived: 4000000
    }),
    (. + {
      elapsedMilliseconds: 6000,
      framesDecoded: 150,
      qpSum: 3750,
      bytesReceived: 5000000,
      freezeCount: 1,
      totalFreezesDurationSeconds: 0.2
    })
  else empty end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/late-transition-samples.jsonl"
render_result "${fixture_directory}/late-transition-samples.jsonl" | jq -e '
  .viewerNetwork.freezeDurationDeltaSeconds == 0.2 and
  .viewerNetwork.steadyStateFreezeDurationDeltaSeconds == 0.2 and
  .gates.playback == false and
  .gates.viewerNetworkRecovery == false and
  .passed == false
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
    .totalFreezesDurationSeconds = 3.03
  elif .phase == "recovery" then
    .freezeCount = 1 |
    .totalFreezesDurationSeconds = 3.03
  else . end
' "${fixture_directory}/samples.jsonl" >"${fixture_directory}/degraded-samples.jsonl"
render_result "${fixture_directory}/degraded-samples.jsonl" | jq -e '
  .phases.viewerNetwork.decodedFramesPerSecond == 23 and
  .viewerNetwork.freezeRatio == 3.03 and
  .gates.playback == false and
  .gates.viewerNetworkRecovery == false and
  .passed == false
' >/dev/null

printf 'End-to-end result tests passed\n'
