def maximum(name): [.[] | .[name]] | max;
def maximum_freeze_ratio: 0.02;
def maximum_capacity_transition_freeze_seconds: 3;
def maximum_capacity_transition_disruption_seconds: 3;
def capacity_transition_grace_milliseconds: 4000;
def maximum_dropped_frame_ratio: 0.01;
def minimum_frame_rate_ratio: 0.8;
def frame_drop_ratio(decoded; dropped):
  if decoded == null or dropped == null or (decoded + dropped) == 0 then 0
  else dropped / (decoded + dropped)
  end;
def phase_delta(phase; name):
  [.[] | select(.phase == phase) | .[name]] |
  if length < 2 then null else last - first end;
def phase_nullable_delta(phase; name):
  [.[] | select(.phase == phase) | .[name] | select(type == "number")] |
  if length < 2 then null else last - first end;
def phase_counter_delta(phase; name):
  . as $samples |
  [range(0; $samples | length) | select($samples[.].phase == phase)] as $indices |
  if ($indices | length) == 0 then null else
    ($indices[0] - (if $indices[0] > 0 then 1 else 0 end)) as $before |
    $samples[$indices[-1]][name] - $samples[$before][name]
  end;
def phase_sampled_delta_after(phase; offset; name):
  . as $samples |
  [$samples[] | select(.phase == phase)] as $phase_samples |
  if ($phase_samples | length) == 0 then 0 else
    $phase_samples[0].elapsedMilliseconds as $started |
    [range(0; $samples | length) |
      select($samples[.].phase == phase and $samples[.].elapsedMilliseconds >= ($started + offset))
    ] as $indices |
    if ($indices | length) == 0 then 0 else
      ($indices[0] - (if $indices[0] > 0 then 1 else 0 end)) as $before |
      $samples[$indices[-1]][name] - $samples[$before][name]
    end
  end;
def native_boundary_required: ($ARGS.named.native_boundary_required // false);
def counter_names: ["framesDecoded", "framesDropped", "freezeCount", "totalFreezesDurationSeconds"];
def valid_native_counters:
  type == "object" and
  (.id | type == "string" and length > 0) and
  (.ssrc | type == "number" and . >= 0 and . <= 4294967295 and floor == .) and
  (.collectedAtMilliseconds | type == "number" and isfinite and . >= 0) and
  ([.framesDecoded, .framesDropped, .freezeCount] |
    all(type == "number" and . >= 0 and . <= 9007199254740991 and floor == .)) and
  (.totalFreezesDurationSeconds | type == "number" and isfinite and . >= 0);
def monotonic_native_counters:
  . as $values |
  all(range(1; $values | length); . as $i |
    $values[$i].collectedAtMilliseconds >= $values[$i - 1].collectedAtMilliseconds and
    all(counter_names[]; . as $name | $values[$i][$name] >= $values[$i - 1][$name]));
def phase_boundary(phase):
  [.[] | select(.phase == phase)] as $samples |
  ($samples[-1].transitionBoundary // {}) as $boundary |
  [$samples[].videoStats] as $regular |
  [$boundary.before, $boundary.after] as $bracket |
  ($regular + $bracket) as $all |
  ($boundary.status == "valid" and $boundary.schemaVersion == 1 and
    ($bracket | all(valid_native_counters)) and ($regular | length) >= 2 and
    ($regular | all(valid_native_counters))) as $snapshots_valid |
  (if $snapshots_valid then {
    snapshots: true,
    timing: ($boundary.phase == phase and
      ($boundary.phaseStartedAt | type == "string" and length > 0) and
      $boundary.graceMilliseconds == capacity_transition_grace_milliseconds and
      $boundary.bracketMilliseconds == 250 and
      ($boundary.timeOriginMilliseconds | type == "number" and isfinite and . > 0) and
      ($boundary.observedAtMilliseconds | type == "number" and isfinite and . >= 0) and
      ($boundary.cutoffMilliseconds | type == "number" and isfinite) and
      ($boundary.cutoffMilliseconds - $boundary.observedAtMilliseconds) == capacity_transition_grace_milliseconds and
      $boundary.before.collectedAtMilliseconds >= ($boundary.cutoffMilliseconds - 250) and
      $boundary.before.collectedAtMilliseconds < $boundary.cutoffMilliseconds and
      $boundary.after.collectedAtMilliseconds >= $boundary.cutoffMilliseconds and
      $boundary.after.collectedAtMilliseconds <= ($boundary.cutoffMilliseconds + 250) and
      $boundary.observedAtMilliseconds <= $samples[0].framePresentation.sampledAtMilliseconds and
      $boundary.observedAtMilliseconds >= ($regular[0].collectedAtMilliseconds - 250) and
      all($samples[];
        .phaseStartedAt == $boundary.phaseStartedAt and
        .framePresentation.timeOriginMilliseconds == $boundary.timeOriginMilliseconds and
        .videoStats.collectedAtMilliseconds <= (.framePresentation.sampledAtMilliseconds + 1) and
        .transitionBoundary.observedAtMilliseconds == $boundary.observedAtMilliseconds)),
    identity: (all($all[]; .id == $boundary.after.id and .ssrc == $boundary.after.ssrc)),
    consistentSampleCounters: (all($samples[]; . as $sample |
      all(counter_names[]; . as $name | $sample.videoStats[$name] == $sample[$name]))),
    constantBoundaryCounters: (all(counter_names[] | select(. != "framesDecoded");
      . as $name | $boundary.before[$name] == $boundary.after[$name])),
    monotonicCounters: (($regular | monotonic_native_counters) and
      ($all | sort_by(.collectedAtMilliseconds) | monotonic_native_counters)),
    postBoundaryCoverage: (
      ([$regular[] | select(.collectedAtMilliseconds > $boundary.after.collectedAtMilliseconds)] | length) >= 2 and
      ($regular[-1].collectedAtMilliseconds - $boundary.after.collectedAtMilliseconds) >= 1000)
  } else {snapshots: false} end) as $checks |
  ($checks | all(.[]; . == true)) as $valid |
  {
    measurementValid: $valid,
    checks: $checks,
    samples: ($samples | length),
    boundary: $boundary,
    steadyDeltas: (if $valid then
      reduce counter_names[] as $name ({}; .[$name] = ($regular[-1][$name] - $boundary.after[$name]))
      else null end),
    legacySampledDeltas: (. as $input | reduce counter_names[] as $name ({};
      .[$name] = ($input | phase_sampled_delta_after(phase; capacity_transition_grace_milliseconds; $name))))
  };
def phase_delta_after(phase; offset; name):
  if native_boundary_required then
    phase_boundary(phase) |
    if .samples == 0 then 0 else .steadyDeltas[name] end
  else phase_sampled_delta_after(phase; offset; name) end;
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
    .encodedKeyFrames,
    .flexFECMediaPackets,
    .flexFECRepairPackets,
    .lossAverage,
    .lossGuardLastObservedLoss,
    .lossGuardRecoveries,
    .lossGuardReductions,
    .lossGuardTargetKbps,
    .lossTargetKbps,
    .pacerPacingBitrateKbps,
    .pacerMaximumAdmittedDelayMilliseconds,
    .pacerMaximumFECDelayMilliseconds,
    .pacerMaximumPrimaryDelayMilliseconds,
    .pacerMaximumQueueDelayMilliseconds,
    .pacerMaximumRepairDelayMilliseconds,
    .pacerMaximumRetransmissionDelayMilliseconds,
    .pacerMaximumSustainedDelayMilliseconds,
    .pacerKeyFrameReserveBytes,
    .pacerMediaBytesDropped,
    .pacerMediaFramesDropped,
    .pacerQueueDrops,
    .pacerQueuePackets,
    .pacerRepairPacketsExpired,
    .pacerRepairPacketsTrimmed,
    .pacerRetransmissionPacketsCoalesced,
    .pacerRetransmissionPacketsExpired,
    .pacerRetransmissionPacketsSuppressed,
    .pacerRetransmissionPacketsTrimmed,
    .pacerFECPacketsExpired,
    .pacerFECPacketsTrimmed,
    .pacerSentFEC,
    .pacerSentPrimary,
    .pacerSentRepair,
    .pacerSentRetransmission,
    .pacerTargetBitrateKbps,
    .recoveryKeyFrameCoalesced,
    .recoveryKeyFrameFailures,
    .recoveryKeyFrameRequests,
    .rtcpKeyFrameRequests,
    .rtcpMalformedFeedback,
    .staleBitrateCallbacks,
    .twccFeedbackPackets,
    .twccMalformedFeedback,
    .twccPaddingStatuses,
    .twccReportedLost,
    .twccReportedStatuses,
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
($ARGS.named.recording // {enabled: false}) as $recording |
{
  revision: $revision,
  mode: $mode,
  workingTreeDirty: $working_tree_dirty,
  recording: $recording,
  profile: {
    controlPath: ($ARGS.named.control_path // "rstream"),
    edgeAuthentication: $edge_auth,
    edgeCredentialLifetimeSeconds: (if $edge_auth then $connect_token_ttl_seconds else null end),
    warmupSeconds: $warmup_seconds,
    phaseSeconds: $phase_seconds,
    recoverySeconds: $recovery_seconds,
    flexFEC: {
      mediaPackets: $flexfec_media_packets,
      repairPackets: $flexfec_repair_packets
    },
    playoutDelayHintSeconds: $playout_delay_hint_seconds,
    producerConfigSHA256: $producer_config_sha256,
    latencyProbe: $latency_probe,
    sourceQualityObserved: $quality_observer,
    expectedNetworkFormat: (if $expected_format == "" then null else $expected_format end),
    acceptance: {
      capacityTransitionGraceMilliseconds: capacity_transition_grace_milliseconds,
      maximumCapacityTransitionFreezeSeconds: maximum_capacity_transition_freeze_seconds,
      maximumCapacityTransitionDisruptionSeconds: maximum_capacity_transition_disruption_seconds,
      maximumContinuousImpairmentFreezeRatio: maximum_freeze_ratio,
      maximumDroppedFrameRatio: maximum_dropped_frame_ratio,
      minimumFrameRateRatio: minimum_frame_rate_ratio
    },
    steadyCounterMethod: (if native_boundary_required then "native-four-second-boundary" else "legacy-sampled-interval" end),
    viewerNetwork: {
      enabled: $viewer_network[0].enabled,
      capacityKbps: $viewer_network[0].capacityKbps,
      delayMilliseconds: $viewer_network[0].delayMilliseconds,
      jitterMilliseconds: $viewer_network[0].jitterMilliseconds,
      lossPercent: $viewer_network[0].lossPercent,
      queuePackets: $viewer_network[0].queuePackets
    },
    sourceNetwork: {
      enabled: $source_network[0].enabled,
      capacityKbps: $source_network[0].capacityKbps,
      delayMilliseconds: $source_network[0].delayMilliseconds,
      jitterMilliseconds: $source_network[0].jitterMilliseconds,
      lossPercent: $source_network[0].lossPercent,
      queuePackets: $source_network[0].queuePackets
    }
  },
  images: {producer: $producer_image, distributor: (if $distributor_image == "" then null else $distributor_image end), browser: $browser_image},
  latency: $latency[0],
  sourceFormats: $source_formats[0],
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
  runtimeHealth: $runtime_health[0],
  nativeSourceProfile: $native_source_profile[0],
  peerConnection: $browser[0].peerConnection,
  signaling: $signaling[0],
  setupMilliseconds: $connected.elapsedMilliseconds,
  setup: {
    presentation: $signaling[0].startup,
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
  transitionBoundaryDiagnostics: {
    scope: "Raw native snapshots; validated deltas and acceptance use are reported separately",
    viewerNetwork: ([.[] | select(.phase == "viewer-network") | .transitionBoundary] | last),
    sourceNetwork: ([.[] | select(.phase == "source-network") | .transitionBoundary] | last),
    recovery: ([.[] | select(.phase == "recovery") | .transitionBoundary] | last)
  },
  transitionBoundaryEvidence: {
    required: native_boundary_required,
    viewerNetwork: phase_boundary("viewer-network"),
    sourceNetwork: phase_boundary("source-network"),
    recovery: phase_boundary("recovery")
  },
  phases: {
    baseline: phase_summary("baseline"),
    sourceNetwork: phase_summary("source-network"),
    viewerNetwork: phase_summary("viewer-network"),
    recovery: phase_summary("recovery")
  },
  viewerNetwork: ($viewer_network[0] + {
    nackCountDelta: phase_counter_delta("viewer-network"; "nackCount"),
    packetsLostNetChange: phase_counter_delta("viewer-network"; "packetsLost"),
    framesDecodedDelta: phase_delta("viewer-network"; "framesDecoded"),
    framesDroppedDelta: phase_counter_delta("viewer-network"; "framesDropped"),
    freezeCountDelta: phase_counter_delta("viewer-network"; "freezeCount"),
    freezeDurationDeltaSeconds: phase_counter_delta("viewer-network"; "totalFreezesDurationSeconds"),
    baselineFreezeCountDelta: phase_counter_delta("baseline"; "freezeCount"),
    baselineFreezeDurationDeltaSeconds: phase_counter_delta("baseline"; "totalFreezesDurationSeconds"),
    recoveryFramesDecodedDelta: phase_delta("recovery"; "framesDecoded"),
    recoveryFreezeCountDelta: phase_counter_delta("recovery"; "freezeCount"),
    recoveryFreezeDurationDeltaSeconds: phase_counter_delta("recovery"; "totalFreezesDurationSeconds"),
    steadyStateFramesDecodedDelta: phase_delta_after("viewer-network"; capacity_transition_grace_milliseconds; "framesDecoded"),
    steadyStateFramesDroppedDelta: phase_delta_after("viewer-network"; capacity_transition_grace_milliseconds; "framesDropped"),
    steadyStateFreezeCountDelta: phase_delta_after("viewer-network"; capacity_transition_grace_milliseconds; "freezeCount"),
    steadyStateFreezeDurationDeltaSeconds: phase_delta_after("viewer-network"; capacity_transition_grace_milliseconds; "totalFreezesDurationSeconds"),
    steadyRecoveryFreezeCountDelta: phase_delta_after("recovery"; capacity_transition_grace_milliseconds; "freezeCount"),
    steadyRecoveryFreezeDurationDeltaSeconds: phase_delta_after("recovery"; capacity_transition_grace_milliseconds; "totalFreezesDurationSeconds")
  }),
  sourceNetwork: ($source_network[0] + {
    twccFeedbackPacketsDelta: phase_counter_delta("source-network"; "twccFeedbackPackets"),
    adaptiveBitrateUpdatesDelta: phase_counter_delta("source-network"; "adaptiveBitrateUpdates"),
    pacerSentRetransmissionDelta: phase_counter_delta("source-network"; "pacerSentRetransmission"),
    pacerSentFECDelta: phase_counter_delta("source-network"; "pacerSentFEC"),
    framesDecodedDelta: phase_delta("source-network"; "framesDecoded"),
    framesDroppedDelta: phase_counter_delta("source-network"; "framesDropped"),
    freezeCountDelta: phase_counter_delta("source-network"; "freezeCount"),
    freezeDurationDeltaSeconds: phase_counter_delta("source-network"; "totalFreezesDurationSeconds"),
    baselineFreezeCountDelta: phase_counter_delta("baseline"; "freezeCount"),
    baselineFreezeDurationDeltaSeconds: phase_counter_delta("baseline"; "totalFreezesDurationSeconds"),
    recoveryFramesDecodedDelta: phase_delta("recovery"; "framesDecoded"),
    recoveryFreezeCountDelta: phase_counter_delta("recovery"; "freezeCount"),
    recoveryFreezeDurationDeltaSeconds: phase_counter_delta("recovery"; "totalFreezesDurationSeconds"),
    steadyStateFramesDecodedDelta: phase_delta_after("source-network"; capacity_transition_grace_milliseconds; "framesDecoded"),
    steadyStateFramesDroppedDelta: phase_delta_after("source-network"; capacity_transition_grace_milliseconds; "framesDropped"),
    steadyStateFreezeCountDelta: phase_delta_after("source-network"; capacity_transition_grace_milliseconds; "freezeCount"),
    steadyStateFreezeDurationDeltaSeconds: phase_delta_after("source-network"; capacity_transition_grace_milliseconds; "totalFreezesDurationSeconds"),
    steadyRecoveryFreezeCountDelta: phase_delta_after("recovery"; capacity_transition_grace_milliseconds; "freezeCount"),
    steadyRecoveryFreezeDurationDeltaSeconds: phase_delta_after("recovery"; capacity_transition_grace_milliseconds; "totalFreezesDurationSeconds")
  }),
  gates: {
    recordingEvidence: ($recording.enabled == false or (
      $recording.enabled == true and $mode != "direct" and
      ($recording.segmentFiles | type) == "number" and $recording.segmentFiles > 0 and
      ($recording.bytes | type) == "number" and $recording.bytes > 0 and
      $recording.storageLimitBytes == 536870912 and $recording.bytes <= $recording.storageLimitBytes
    )),
    sourceFormats: ($expected_format == "" or (
      $source_formats[0].enabled == true and $source_formats[0].networkProfile == $expected_format and $source_formats[0].passed == true
    )),
    sourceQualityEvidence: (($quality_observer | not) or (
      length > 0 and all(.sourceQuality != null and .sourceQuality.failedUpdates == 0)
    )),
    latencyMeasurement: (($latency_probe | not) or (
      $latency[0].enabled == true and $latency[0].collectionComplete == true and
      $latency[0].measurementValid == true
    )),
    media: (maximum("framesDecoded") >= 150 and maximum("bytesReceived") > 0),
    playback: (maximum("freezeCount") == 0 and maximum("totalFreezesDurationSeconds") == 0),
    sourceFeedback: (maximum("twccFeedbackPackets") > 0 and ($mode == "mediamtx-native" or maximum("pacerSentFEC") > 0)),
    producerMetrics: (([.[] | select(producer_metrics_complete)] | length) == length),
    qualityEvidence: ([phase_summary("baseline"), phase_summary("source-network"), phase_summary("viewer-network"), phase_summary("recovery")] | map(select(. != null)) | all(.averageDecodedQP != null and .averageDecodedQP >= 0)),
    setupEvidence: ([$peer_created, $whep_post, $connected, $playback_ready, $first_decoded_frame] | all(. != null)),
    startupPresentationEvidence: (
      $signaling[0].startup.measurementValid == true and
      ($signaling[0].startup.requestToCallbackMilliseconds | type) == "number" and
      $signaling[0].startup.requestToCallbackMilliseconds >= 0 and
      ($signaling[0].startup.requestToExpectedDisplayMilliseconds | type) == "number" and
      $signaling[0].startup.requestToExpectedDisplayMilliseconds >= 0
    ),
    teardownEvidence: ($whep_delete != null and $whep_delete.durationMilliseconds >= 0),
    adapterIntegrity: (
      $mode != "mediamtx" or (
        $adapter[0].invalid_fec == 0 and
        $adapter[0].reorder_discarded == 0 and
        $adapter[0].discontinuities == 0 and
        $adapter[0].reorder_skipped == ($adapter[0].expired + $adapter[0].reorder_late) and
        (if $adapter[0].reorder_skipped > 0 then
          $adapter[0].key_frame_requests > 0 and
          $adapter[0].damaged_source_frames_dropped > 0 and
          $adapter[0].damaged_source_packets_dropped > 0
        else
          $adapter[0].damaged_source_frames_dropped == 0 and
          $adapter[0].damaged_source_packets_dropped == 0
        end)
      )
    ),
    runtimeMediaIntegrity: (
      $runtime_health[0].fatalErrors == 0 and
      $runtime_health[0].h264PacketizationErrors == 0 and
      $runtime_health[0].packetLossWarnings == 0
    ),
    performanceEnvironment: ($runtime_health[0].transportBufferWarnings == 0),
    nativeSourceLifecycle: (
      if $mode == "mediamtx-native" then
        $native_source_profile[0].required and
        $native_source_profile[0].activeSessions == 1 and
        $native_source_profile[0].createdSessions == 1 and
        $native_source_profile[0].negotiated == {twcc: 1, nack: 1, rtx: 1, flexfec: 0} and
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
| .viewerNetwork.frameDropRatio = frame_drop_ratio(.viewerNetwork.framesDecodedDelta; .viewerNetwork.framesDroppedDelta)
| .viewerNetwork.steadyStateFrameDropRatio = frame_drop_ratio(.viewerNetwork.steadyStateFramesDecodedDelta; .viewerNetwork.steadyStateFramesDroppedDelta)
| .viewerNetwork.capacityTransitionFramesDropped = ([0, ((.viewerNetwork.framesDroppedDelta // 0) - (.viewerNetwork.steadyStateFramesDroppedDelta // 0))] | max)
| .viewerNetwork.capacityTransitionDroppedFrameDurationSeconds = (
    if .phases.baseline.decodedFramesPerSecond > 0 then
      .viewerNetwork.capacityTransitionFramesDropped / .phases.baseline.decodedFramesPerSecond
    else 0 end
  )
| .viewerNetwork.capacityTransitionDisruptionSeconds = (
    (.viewerNetwork.freezeDurationDeltaSeconds // 0) +
    .viewerNetwork.capacityTransitionDroppedFrameDurationSeconds
  )
| .viewerNetwork.recoveryFreezeRatio = (
    if .viewerNetwork.enabled then
      .viewerNetwork.recoveryFreezeDurationDeltaSeconds / $recovery_seconds
    else 0 end
  )
| .sourceNetwork.freezeRatio = (
    if .sourceNetwork.enabled then
      .sourceNetwork.freezeDurationDeltaSeconds / $phase_seconds
    else 0 end
  )
| .sourceNetwork.frameDropRatio = frame_drop_ratio(.sourceNetwork.framesDecodedDelta; .sourceNetwork.framesDroppedDelta)
| .sourceNetwork.steadyStateFrameDropRatio = frame_drop_ratio(.sourceNetwork.steadyStateFramesDecodedDelta; .sourceNetwork.steadyStateFramesDroppedDelta)
| .sourceNetwork.capacityTransitionFramesDropped = ([0, ((.sourceNetwork.framesDroppedDelta // 0) - (.sourceNetwork.steadyStateFramesDroppedDelta // 0))] | max)
| .sourceNetwork.capacityTransitionDroppedFrameDurationSeconds = (
    if .phases.baseline.decodedFramesPerSecond > 0 then
      .sourceNetwork.capacityTransitionFramesDropped / .phases.baseline.decodedFramesPerSecond
    else 0 end
  )
| .sourceNetwork.capacityTransitionDisruptionSeconds = (
    (.sourceNetwork.freezeDurationDeltaSeconds // 0) +
    .sourceNetwork.capacityTransitionDroppedFrameDurationSeconds
  )
| .sourceNetwork.recoveryFreezeRatio = (
    if .sourceNetwork.enabled then
      .sourceNetwork.recoveryFreezeDurationDeltaSeconds / $recovery_seconds
    else 0 end
  )
| .networkImpairment = (
    if .sourceNetwork.enabled then
      (.sourceNetwork + {phase: "source-network"})
    elif .viewerNetwork.enabled then
      (.viewerNetwork + {phase: "viewer-network"})
    else
      (.viewerNetwork + {phase: null})
    end
  )
| .gates.transitionBoundaryEvidence = (
    if .transitionBoundaryEvidence.required then
      (if .viewerNetwork.enabled and .viewerNetwork.capacityKbps > 0 and
          .viewerNetwork.delayMilliseconds == 0 and .viewerNetwork.jitterMilliseconds == 0 and .viewerNetwork.lossPercent == 0 then
        .transitionBoundaryEvidence.viewerNetwork.measurementValid else true end) and
      (if .sourceNetwork.enabled and .sourceNetwork.capacityKbps > 0 and
          .sourceNetwork.delayMilliseconds == 0 and .sourceNetwork.jitterMilliseconds == 0 and .sourceNetwork.lossPercent == 0 then
        .transitionBoundaryEvidence.sourceNetwork.measurementValid else true end) and
      (if .viewerNetwork.enabled or .sourceNetwork.enabled then
        .transitionBoundaryEvidence.recovery.measurementValid else true end)
    else true end
  )
| .gates.playback = (
    if .networkImpairment.enabled then
      .networkImpairment.baselineFreezeCountDelta == 0 and
      .networkImpairment.baselineFreezeDurationDeltaSeconds == 0 and
      .networkImpairment.recoveryFreezeDurationDeltaSeconds <= maximum_capacity_transition_freeze_seconds and
      .networkImpairment.steadyRecoveryFreezeCountDelta == 0 and
      .networkImpairment.steadyRecoveryFreezeDurationDeltaSeconds == 0 and
      (if .networkImpairment.capacityKbps > 0 and
          .networkImpairment.delayMilliseconds == 0 and
          .networkImpairment.jitterMilliseconds == 0 and
          .networkImpairment.lossPercent == 0 then
        .networkImpairment.freezeDurationDeltaSeconds <= maximum_capacity_transition_freeze_seconds and
        .networkImpairment.steadyStateFreezeCountDelta == 0 and
        .networkImpairment.steadyStateFreezeDurationDeltaSeconds == 0
      else
        .networkImpairment.freezeRatio <= maximum_freeze_ratio
      end)
    else
      .phases.baseline.freezes == 0 and
      .phases.baseline.freezeDurationSeconds == 0
    end
  )
| .gates.sourceNetworkCausality = (
    if .sourceNetwork.enabled then
      .sourceNetwork.scope == "producer-to-adapter" and
      .sourceNetwork.destination.port == null and
      .sourceNetwork.qdisc.packets > 0 and
      .sourceNetwork.twccFeedbackPacketsDelta > 0 and
      (if .sourceNetwork.lossPercent > 0 then
        .sourceNetwork.qdisc.drops > 0 and
        .sourceNetwork.pacerSentRetransmissionDelta > 0 and
        (($adapter[0].repaired_rtx + $adapter[0].repaired_fec) > 0)
      else true end)
    else true end
  )
| .gates.sourceNetworkResponse = (
    if .sourceNetwork.enabled then
      (if $expected_format != "" then
        $source_formats[0].phases["source-network"].cadencePassed == true
      else .phases.sourceNetwork.decodedFramesPerSecond >=
        (.phases.baseline.decodedFramesPerSecond * minimum_frame_rate_ratio) end) and
      (if .sourceNetwork.capacityKbps > 0 and
          .sourceNetwork.delayMilliseconds == 0 and
          .sourceNetwork.jitterMilliseconds == 0 and
          .sourceNetwork.lossPercent == 0 then
        .sourceNetwork.capacityTransitionDisruptionSeconds <= maximum_capacity_transition_disruption_seconds and
        .sourceNetwork.steadyStateFrameDropRatio <= maximum_dropped_frame_ratio
      else
        .sourceNetwork.frameDropRatio <= maximum_dropped_frame_ratio
      end) and
      (if .sourceNetwork.capacityKbps > 0 then
        .sourceNetwork.adaptiveBitrateUpdatesDelta > 0 and
        .phases.sourceNetwork.encoderTargetKbps.medianLast10Seconds <=
          (.phases.baseline.encoderTargetKbps.medianLast10Seconds * 0.8) and
        .phases.sourceNetwork.encoderTargetKbps.medianLast10Seconds <=
          (.sourceNetwork.capacityKbps * 1.1)
      else true end)
    else true end
  )
| .gates.viewerNetworkRecovery = (
    if .viewerNetwork.enabled then
      .viewerNetwork.qdisc.packets > 0 and
      (if .viewerNetwork.qdisc.drops > 0 then .viewerNetwork.nackCountDelta > 0 else true end) and
      (if .viewerNetwork.lossPercent > 0 then .viewerNetwork.qdisc.drops > 0 else true end) and
      (if $expected_format != "" then
        $source_formats[0].phases["viewer-network"].cadencePassed == true
      else .phases.viewerNetwork.decodedFramesPerSecond >=
        (.phases.baseline.decodedFramesPerSecond * minimum_frame_rate_ratio) end) and
      (if .viewerNetwork.capacityKbps > 0 and
          .viewerNetwork.delayMilliseconds == 0 and
          .viewerNetwork.jitterMilliseconds == 0 and
          .viewerNetwork.lossPercent == 0 then
        .viewerNetwork.capacityTransitionDisruptionSeconds <= maximum_capacity_transition_disruption_seconds and
        .viewerNetwork.steadyStateFrameDropRatio <= maximum_dropped_frame_ratio and
        .viewerNetwork.steadyStateFreezeCountDelta == 0 and
        .viewerNetwork.steadyStateFreezeDurationDeltaSeconds == 0
      else
        .viewerNetwork.frameDropRatio <= maximum_dropped_frame_ratio and
        .viewerNetwork.freezeRatio <= maximum_freeze_ratio
      end) and
      (if $expected_format != "" then
        $source_formats[0].phases.recovery.cadencePassed == true
      else .phases.recovery.decodedFramesPerSecond >=
        (.phases.baseline.decodedFramesPerSecond * minimum_frame_rate_ratio) end) and
      .viewerNetwork.recoveryFreezeDurationDeltaSeconds <= maximum_capacity_transition_freeze_seconds and
      .viewerNetwork.steadyRecoveryFreezeCountDelta == 0 and
      .viewerNetwork.steadyRecoveryFreezeDurationDeltaSeconds == 0
    else true end
  )
| .gates.sourceTargetRecovery = (
    if .sourceNetwork.enabled or (.viewerNetwork.enabled and $mode == "direct") then
      .phases.recovery.encoderTargetKbps.medianLast10Seconds >=
        (.phases.baseline.encoderTargetKbps.medianLast10Seconds * 0.8)
    else true end
  )
| .functionalPassed = ([.gates | to_entries[] | select(.key != "performanceEnvironment") | .value] | all)
| .passed = (.functionalPassed and .gates.performanceEnvironment)
| .publishable = (.passed and (.workingTreeDirty | not))
