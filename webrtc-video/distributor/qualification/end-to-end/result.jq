def maximum(name): [.[] | .[name]] | max;
def maximum_freeze_ratio: 0.02;
def minimum_frame_rate_ratio: 0.8;
def phase_delta(phase; name):
  [.[] | select(.phase == phase) | .[name]] |
  if length < 2 then null else last - first end;
def phase_nullable_delta(phase; name):
  [.[] | select(.phase == phase) | .[name] | select(type == "number")] |
  if length < 2 then null else last - first end;
def producer_metrics_complete:
  .producerMetricsSource == "openmetrics" and
  ([
    .adaptiveBitrateFailures,
    .adaptiveBitrateUpdates,
    .delayControllerDecreaseSessions,
    .delayControllerHoldSessions,
    .delayControllerIncreaseSessions,
    .delayControllerNormalSessions,
    .delayControllerOveruseSessions,
    .delayControllerUnderuseSessions,
    .delayTargetKbps,
    .encoderTargetKbps,
    .lossAverage,
    .lossGuardRecoveries,
    .lossGuardReductions,
    .lossGuardTargetKbps,
    .lossTargetKbps,
    .pacerPacingBitrateKbps,
    .pacerTargetBitrateKbps,
    .twccTargetKbps
  ] | all(type == "number")) and
  (.lossGuardActive | type == "boolean");
def tail_minimum(samples; name; window):
  samples as $samples |
  ($samples[-1].elapsedMilliseconds - window) as $cutoff |
  [$samples[] | select(.elapsedMilliseconds >= $cutoff) | .[name]] | min;
def median(values):
  (values | sort) as $values |
  ($values | length) as $count |
  if $count == 0 then null
  elif ($count % 2) == 1 then $values[($count / 2 | floor)]
  else (($values[$count / 2 - 1] + $values[$count / 2]) / 2)
  end;
def tail_median(samples; name; window):
  samples as $samples |
  ($samples[-1].elapsedMilliseconds - window) as $cutoff |
  median([$samples[] | select(.elapsedMilliseconds >= $cutoff) | .[name]]);
def phase_summary(phase):
  [.[] | select(.phase == phase)] as $samples |
  if ($samples | length) < 2 then null else
    ($samples[-1].elapsedMilliseconds - $samples[0].elapsedMilliseconds) as $duration |
    (phase_delta(phase; "framesDecoded")) as $decoded_frames |
    (phase_nullable_delta(phase; "qpSum")) as $qp_sum |
    {
      samples: ($samples | length),
      durationMilliseconds: $duration,
      receivedBitrateKbps: (phase_delta(phase; "bytesReceived") * 8 / $duration),
      decodedFramesPerSecond: ($decoded_frames * 1000 / $duration),
      averageDecodedQP: (if $qp_sum != null and $decoded_frames > 0 then $qp_sum / $decoded_frames else null end),
      framesPerSecond: {minimum: ([$samples[].framesPerSecond] | min), maximum: ([$samples[].framesPerSecond] | max)},
      encoderTargetKbps: {first: $samples[0].encoderTargetKbps, last: $samples[-1].encoderTargetKbps, minimum: ([$samples[].encoderTargetKbps] | min), maximum: ([$samples[].encoderTargetKbps] | max), medianLast10Seconds: tail_median($samples; "encoderTargetKbps"; 10000), sustainedMinimumLast10Seconds: tail_minimum($samples; "encoderTargetKbps"; 10000)},
      twccTargetKbps: {first: $samples[0].twccTargetKbps, last: $samples[-1].twccTargetKbps, minimum: ([$samples[].twccTargetKbps] | min), maximum: ([$samples[].twccTargetKbps] | max), medianLast10Seconds: tail_median($samples; "twccTargetKbps"; 10000), sustainedMinimumLast10Seconds: tail_minimum($samples; "twccTargetKbps"; 10000)},
      jitterMilliseconds: {average: (([$samples[].jitterSeconds] | add) * 1000 / ($samples | length)), maximum: (([$samples[].jitterSeconds] | max) * 1000)},
      roundTripTimeMilliseconds: {average: (([$samples[].currentRoundTripTimeSeconds] | add) * 1000 / ($samples | length)), maximum: (([$samples[].currentRoundTripTimeSeconds] | max) * 1000)},
      nacks: phase_delta(phase; "nackCount"),
      packetsLostNetChange: phase_delta(phase; "packetsLost"),
      framesDropped: phase_delta(phase; "framesDropped"),
      freezes: phase_delta(phase; "freezeCount"),
      freezeDurationSeconds: phase_delta(phase; "totalFreezesDurationSeconds")
    }
  end;
def event(kind): [$signaling[0].events[]? | select(.kind == kind)] | first;
def state_event(kind; state): [$signaling[0].events[]? | select(.kind == kind and .state == state)] | first;
def whep_event(method): [$signaling[0].events[]? | select(.kind == "whep-request" and .method == method)] | first;
(event("peer-created")) as $peer_created |
(whep_event("POST")) as $whep_post |
(state_event("connectionstatechange"; "connected")) as $connected |
(event("playback-ready")) as $playback_ready |
(event("first-decoded-frame")) as $first_decoded_frame |
(whep_event("DELETE")) as $whep_delete |
{
  revision: $revision,
  mode: $mode,
  workingTreeDirty: $working_tree_dirty,
  profile: {
    edgeAuthentication: $edge_auth,
    edgeCredentialLifetimeSeconds: (if $edge_auth then $connect_token_ttl_seconds else null end),
    warmupSeconds: $warmup_seconds,
    phaseSeconds: $phase_seconds,
    recoverySeconds: $recovery_seconds,
    viewerNetwork: {
      capacityKbps: $viewer_network[0].capacityKbps,
      delayMilliseconds: $viewer_network[0].delayMilliseconds,
      jitterMilliseconds: $viewer_network[0].jitterMilliseconds,
      lossPercent: $viewer_network[0].lossPercent,
      queuePackets: $viewer_network[0].queuePackets
    }
  },
  images: {producer: $producer_image, distributor: (if $distributor_image == "" then null else $distributor_image end), browser: $browser_image},
  samples: length,
  framesDecoded: maximum("framesDecoded"),
  framesDropped: maximum("framesDropped"),
  freezeCount: maximum("freezeCount"),
  totalFreezesDurationSeconds: maximum("totalFreezesDurationSeconds"),
  bytesReceived: maximum("bytesReceived"),
  producerTWCCFeedbackPackets: maximum("twccFeedbackPackets"),
  producerPacerSentFEC: maximum("pacerSentFEC"),
  producerMetricsSamples: ([.[] | select(.producerMetricsSource == "openmetrics")] | length),
  producerMetricsCompleteSamples: ([.[] | select(producer_metrics_complete)] | length),
  adapter: $adapter[0],
  nativeSourceProfile: $native_source_profile[0],
  peerConnection: $browser[0].peerConnection,
  signaling: $signaling[0],
  setupMilliseconds: $connected.elapsedMilliseconds,
  setup: {
    peerCreatedMilliseconds: $peer_created.elapsedMilliseconds,
    whepPostDurationMilliseconds: $whep_post.durationMilliseconds,
    whepPostCompletedMilliseconds: $whep_post.elapsedMilliseconds,
    connectedMilliseconds: $connected.elapsedMilliseconds,
    playbackReadyMilliseconds: $playback_ready.elapsedMilliseconds,
    firstDecodedFrameMilliseconds: $first_decoded_frame.elapsedMilliseconds,
    postToConnectedMilliseconds: ($connected.elapsedMilliseconds - $whep_post.elapsedMilliseconds),
    peerToFirstDecodedFrameMilliseconds: ($first_decoded_frame.elapsedMilliseconds - $peer_created.elapsedMilliseconds)
  },
  teardown: {
    whepDeleteDurationMilliseconds: $whep_delete.durationMilliseconds,
    whepDeleteCompletedMilliseconds: $whep_delete.elapsedMilliseconds
  },
  resources: $resources[0],
  phases: {
    baseline: phase_summary("baseline"),
    viewerNetwork: phase_summary("viewer-network"),
    recovery: phase_summary("recovery")
  },
  viewerNetwork: ($viewer_network[0] + {
    nackCountDelta: phase_delta("viewer-network"; "nackCount"),
    packetsLostNetChange: phase_delta("viewer-network"; "packetsLost"),
    framesDecodedDelta: phase_delta("viewer-network"; "framesDecoded"),
    framesDroppedDelta: phase_delta("viewer-network"; "framesDropped"),
    freezeCountDelta: phase_delta("viewer-network"; "freezeCount"),
    freezeDurationDeltaSeconds: phase_delta("viewer-network"; "totalFreezesDurationSeconds"),
    baselineFreezeCountDelta: phase_delta("baseline"; "freezeCount"),
    baselineFreezeDurationDeltaSeconds: phase_delta("baseline"; "totalFreezesDurationSeconds"),
    recoveryFramesDecodedDelta: phase_delta("recovery"; "framesDecoded"),
    recoveryFreezeCountDelta: phase_delta("recovery"; "freezeCount"),
    recoveryFreezeDurationDeltaSeconds: phase_delta("recovery"; "totalFreezesDurationSeconds")
  }),
  gates: {
    media: (maximum("framesDecoded") >= 150 and maximum("bytesReceived") > 0),
    playback: (maximum("freezeCount") == 0 and maximum("totalFreezesDurationSeconds") == 0),
    sourceFeedback: (maximum("twccFeedbackPackets") > 0 and ($mode == "mediamtx-native" or maximum("pacerSentFEC") > 0)),
    producerMetrics: (([.[] | select(producer_metrics_complete)] | length) == length),
    qualityEvidence: ([phase_summary("baseline"), phase_summary("viewer-network"), phase_summary("recovery")] | map(select(. != null)) | all(.averageDecodedQP != null and .averageDecodedQP >= 0)),
    setupEvidence: ([$peer_created, $whep_post, $connected, $playback_ready, $first_decoded_frame] | all(. != null)),
    teardownEvidence: ($whep_delete != null and $whep_delete.durationMilliseconds >= 0),
    adapterIntegrity: ($mode != "mediamtx" or ($adapter[0].invalid_fec == 0 and $adapter[0].reorder_late == 0 and $adapter[0].reorder_discarded == 0 and $adapter[0].expired == 0)),
    nativeSourceLifecycle: (
      if $mode == "mediamtx-native" then
        $native_source_profile[0].required and
        $native_source_profile[0].activeSessions == 1 and
        $native_source_profile[0].createdSessions == 1 and
        $native_source_profile[0].negotiated == {twcc: 1, nack: 1, rtx: 0, flexfec: 0} and
        $native_source_profile[0].fixedSourcePacing == {adaptiveUpdates: 0, adaptiveFailures: 0, queueDrops: 0, mediaFrameDrops: 0} and
        $native_source_profile[0].activeAfterTeardown == 0
      else
        ($native_source_profile[0].required | not)
      end
    ),
    viewerNegotiation: (
      $browser[0].peerConnection.nackNegotiated and
      $browser[0].peerConnection.twccNegotiated and
      (if $mode == "direct" then
        $browser[0].peerConnection.rtxNegotiated and $browser[0].peerConnection.flexFECNegotiated
      else
        ($browser[0].peerConnection.rtxNegotiated | not) and ($browser[0].peerConnection.flexFECNegotiated | not)
      end)
    ),
    resourceLifecycle: ($signaling[0].iceRestartOffers == 0 and $signaling[0].whepSessionCreates == 1 and $signaling[0].whepSessionDeletes == 1 and $signaling[0].whepFailedRequests == 0)
  }
}
| .viewerNetwork.freezeRatio = (
    if .viewerNetwork.enabled then
      .viewerNetwork.freezeDurationDeltaSeconds / $phase_seconds
    else 0 end
  )
| .viewerNetwork.recoveryFreezeRatio = (
    if .viewerNetwork.enabled then
      .viewerNetwork.recoveryFreezeDurationDeltaSeconds / $recovery_seconds
    else 0 end
  )
| .gates.playback = (
    if .viewerNetwork.enabled then
      .viewerNetwork.baselineFreezeCountDelta == 0 and
      .viewerNetwork.baselineFreezeDurationDeltaSeconds == 0 and
      .viewerNetwork.freezeRatio <= maximum_freeze_ratio and
      .viewerNetwork.recoveryFreezeRatio <= maximum_freeze_ratio
    else
      .phases.baseline.freezes == 0 and
      .phases.baseline.freezeDurationSeconds == 0
    end
  )
| .gates.viewerNetworkRecovery = (
    if .viewerNetwork.enabled then
      .viewerNetwork.qdisc.packets > 0 and
      (if .viewerNetwork.qdisc.drops > 0 then .viewerNetwork.nackCountDelta > 0 else true end) and
      (if .viewerNetwork.lossPercent > 0 then .viewerNetwork.qdisc.drops > 0 else true end) and
      .phases.viewerNetwork.decodedFramesPerSecond >=
        (.phases.baseline.decodedFramesPerSecond * minimum_frame_rate_ratio) and
      .viewerNetwork.framesDroppedDelta == 0 and
      .viewerNetwork.freezeRatio <= maximum_freeze_ratio and
      .phases.recovery.decodedFramesPerSecond >=
        (.phases.baseline.decodedFramesPerSecond * minimum_frame_rate_ratio) and
      .viewerNetwork.recoveryFreezeRatio <= maximum_freeze_ratio
    else true end
  )
| .gates.sourceTargetRecovery = (
    if .viewerNetwork.enabled and $mode == "direct" then
      .phases.recovery.encoderTargetKbps.medianLast10Seconds >=
        (.phases.baseline.encoderTargetKbps.medianLast10Seconds * 0.8)
    else true end
  )
| .passed = ([.gates[]] | all)
| .publishable = (.passed and (.workingTreeDirty | not))
