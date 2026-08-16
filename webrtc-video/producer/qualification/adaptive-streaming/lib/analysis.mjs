import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import process from "node:process";
import {
  renderNetworkConditionsSVG,
  renderPlaybackQualitySVG,
  renderTransportEvidenceSVG,
} from "./evidence-svg.mjs";

const maximumQualificationSampleGapMilliseconds = 2500;
const minimumSustainedRecoveryMilliseconds = 10_000;

export function enrichSamples(samples) {
  let previous = null;
  return samples.map((sample) => {
    let receivedBitrateKbps = 0;
    if (
      previous &&
      sample.bytesReceived >= previous.bytesReceived &&
      sample.elapsedMilliseconds > previous.elapsedMilliseconds
    ) {
      const bytes = sample.bytesReceived - previous.bytesReceived;
      const milliseconds =
        sample.elapsedMilliseconds - previous.elapsedMilliseconds;
      receivedBitrateKbps = (bytes * 8) / milliseconds;
    }
    previous = sample;
    return { ...sample, receivedBitrateKbps };
  });
}

export function analyze(
  samples,
  manifest,
  trafficControl = null,
  encoderQuality = null,
  receiverUDPEvidence = null,
  producerUDPEvidence = null,
  setupEvidence = null,
  hostCPUEvidence = null,
  phaseTimeline = null,
  receiverHostCPUEvidence = null,
  networkConditionTimeline = null,
) {
  const enriched = enrichSamples(samples);
  const phaseOrder = manifest.phases.map((phase) => phase.name);
  const steadyPhaseOrder = phaseOrder.filter((name) => name !== "mobility");
  const qualificationPhaseOrder = phaseOrder.filter(
    (name) => name !== "warmup",
  );
  const summaries = Object.fromEntries(
    manifest.phases.map((phase) => [
      phase.name,
      summarizePhase(
        enriched.filter((sample) => sample.phase === phase.name),
        phase,
        encoderQuality?.[phase.name] || null,
      ),
    ]),
  );
  const baseline = summaries.baseline;
  const conditioning = summaries.conditioning;
  const constrained = summaries.constrained;
  const impaired = summaries.impaired;
  const recovery = summaries.recovery;
  const assertions = [];
  const trafficControlSummary = summarizeTrafficControl(trafficControl);
  const receiverUDP = summarizeReceiverUDP(receiverUDPEvidence, phaseOrder);
  const producerUDP = summarizeReceiverUDP(producerUDPEvidence, phaseOrder);
  const setup = summarizeSetup(setupEvidence);
  const hostCPU = summarizeHostCPU(hostCPUEvidence, phaseTimeline, phaseOrder);
  const receiverHostCPU = summarizeHostCPU(
    receiverHostCPUEvidence,
    phaseTimeline,
    phaseOrder,
  );
  const networkConditions = alignNetworkConditions(
    networkConditionTimeline,
    enriched,
    manifest,
  );
  const candidatePairSwitches = countCandidatePairSwitches(enriched);
  const networkMobility = summarizeNetworkMobility(enriched);
  const icePolicy = manifest.networkPath?.icePolicy || "relay";
  let healthyLinkTargetKbps = null;
  let healthyLinkTargetRatio = null;
  const constrainedMediaCapacityKbps = mediaCapacityKbps(
    constrained?.capacityKbps || 0,
    manifest.protection,
  );
  const congestionResponseRequired =
    (baseline?.medianEncoderTargetKbps || 0) > constrainedMediaCapacityKbps;
  if (manifest.networkConditionTimeline?.required === true) {
    assert(
      assertions,
      networkConditions.available && networkConditions.chronological,
      "network-condition-timeline",
      "every configured traffic-control transition is timestamped in chronological order on the metrics collector clock",
    );
  }
  assert(
    assertions,
    phaseOrder.every((name) => summaries[name]?.samples >= 15),
    "phase-sample-coverage",
    "every measured phase has at least 15 samples",
  );
  if (Number.isInteger(manifest.runtime?.logicalCPUs)) {
    assert(
      assertions,
      hostCPU.available &&
        phaseOrder.every(
          (name) =>
            hostCPU.phases[name]?.samples >= 15 &&
            hostCPU.phases[name].samplingGapSamples >= 15 &&
            hostCPU.phases[name].samplingGapMaximumMilliseconds <= 350 &&
            hostCPU.phases[name].stealP95Ratio <= 0.05,
        ),
      "producer-host-scheduler",
      "producer host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms",
    );
  }
  if (Number.isInteger(manifest.browserRuntime?.logicalCPUs)) {
    assert(
      assertions,
      receiverHostCPU.available &&
        phaseOrder.every(
          (name) =>
            receiverHostCPU.phases[name]?.samples >= 15 &&
            receiverHostCPU.phases[name].samplingGapSamples >= 15 &&
            receiverHostCPU.phases[name].samplingGapMaximumMilliseconds <=
              350 &&
            receiverHostCPU.phases[name].stealP95Ratio <= 0.05,
        ),
      "receiver-host-scheduler",
      "receiver host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms",
    );
  }
  if (Number.isFinite(manifest.video?.playoutDelayHintSeconds)) {
    assert(
      assertions,
      phaseOrder.every(
        (name) =>
          Number.isFinite(
            summaries[name]?.averageJitterBufferTargetMilliseconds,
          ) && summaries[name].averageJitterBufferTargetMilliseconds <= 250,
      ),
      "playout-target-latency-budget",
      "receiver jitter-buffer target evidence covers every phase and remains at or below 250 ms",
    );
    assert(
      assertions,
      phaseOrder.every(
        (name) =>
          Number.isFinite(
            summaries[name]?.averageJitterBufferDelayMilliseconds,
          ) && summaries[name].averageJitterBufferDelayMilliseconds <= 300,
      ),
      "playout-effective-latency-budget",
      "receiver effective buffered delay evidence covers every phase and its phase average remains at or below 300 ms",
    );
  }
  assert(
    assertions,
    enriched.every((sample) => {
      if (
        sample.phase === "mobility" &&
        sample.peerConnectionState !== "connected"
      ) {
        return true;
      }
      const usesRelay =
        sample.localCandidateType === "relay" ||
        sample.remoteCandidateType === "relay";
      return icePolicy === "relay" ? usesRelay : !usesRelay;
    }),
    "ice-path",
    icePolicy === "relay"
      ? "every sample uses a TURN relay candidate"
      : "every sample uses the direct Docker bridge path without TURN",
  );
  assert(
    assertions,
    steadyPhaseOrder.every((name) => summaries[name]?.connectedRatio >= 0.98),
    "session-continuity",
    "peer connection and playback remain healthy for at least 98% of samples",
  );
  if (manifest.networkMobility) {
    assert(
      assertions,
      manifest.networkMobility.addressChanged === true,
      "mobility-source-address",
      "the producer moves to a distinct source address during the controlled interface switch",
    );
    assert(
      assertions,
      networkMobility.candidatePairSwitches >= 1 &&
        networkMobility.trickledRemoteCandidates >= 1,
      "trickle-ice-mobility",
      "the producer trickles at least one fresh candidate and WebRTC selects a new candidate pair after the interface switch",
    );
    assert(
      assertions,
      manifest.networkMobility.signalingTransport === "quic" &&
        networkMobility.peerConnectionsCreated === 1 &&
        networkMobility.webSocketsCreated === 1 &&
        networkMobility.webSocketCloses === 0,
      "quic-signaling-mobility",
      "the original WebRTC peer and signaling WebSocket survive the producer network change over the rstream QUIC upstream",
    );
    assert(
      assertions,
      networkMobility.maximumUnavailableMilliseconds <= 15_000,
      "mobility-recovery",
      "video playback remains continuous or recovers within 15 seconds of the producer interface switch",
    );
  }
  assert(
    assertions,
    baseline?.medianReceivedBitrateKbps >= 1000,
    "baseline-throughput",
    "baseline median receive throughput is at least 1 Mbps",
  );
  if (conditioning) {
    assert(
      assertions,
      conditioning.endingEncoderTargetKbps >=
        (baseline?.medianEncoderTargetKbps || Infinity) * 0.8,
      "capacity-experiment-settled",
      "the encoder returns to at least 80% of its baseline target after traffic-control activation and before the first capacity step",
    );
  }
  if (
    manifest.networkPath?.kind !== "relay" &&
    Number.isFinite(manifest.video?.adaptive?.maximumBitrateKbps)
  ) {
    const maximumBitrateKbps = manifest.video.adaptive.maximumBitrateKbps;
    const changeThresholdPct = manifest.video.adaptive.changeThresholdPct || 0;
    healthyLinkTargetKbps =
      (maximumBitrateKbps * (100 - changeThresholdPct)) / 100;
    const baselineSamples = enriched.filter(
      (sample) =>
        sample.phase === "baseline" &&
        Number.isFinite(sample.encoderTargetKbps) &&
        sample.encoderTargetKbps > 0,
    );
    healthyLinkTargetRatio =
      baselineSamples.length > 0
        ? baselineSamples.filter(
            (sample) => sample.encoderTargetKbps >= healthyLinkTargetKbps,
          ).length / baselineSamples.length
        : 0;
    assert(
      assertions,
      healthyLinkTargetRatio >= 0.5,
      "healthy-link-quality-ceiling",
      `the encoder spends at least half of the healthy baseline within ${changeThresholdPct}% of its ${maximumBitrateKbps} kbps adaptive ceiling`,
    );
  }
  const reductionThreshold = (baseline?.medianEncoderTargetKbps || 0) * 0.8;
  assert(
    assertions,
    !congestionResponseRequired ||
      (constrained?.medianEncoderTargetKbps || Infinity) <= reductionThreshold,
    "congestion-response",
    congestionResponseRequired
      ? "constrained median encoder target falls by at least 20% from baseline"
      : "the baseline encoder target already fits inside the constrained media budget, so no forced reduction is required",
  );
  const responseDelay = timeToEncoderTarget(
    enriched,
    "constrained",
    reductionThreshold,
    "at-most",
  );
  assert(
    assertions,
    !congestionResponseRequired ||
      (responseDelay !== null && responseDelay <= 30_000),
    "response-time",
    congestionResponseRequired
      ? "encoder target reacts to the constrained link within 30 seconds"
      : "no response deadline applies because the baseline target fits the constrained media budget",
  );
  assert(
    assertions,
    targetDoesNotIncreaseAfterObservedLoss(
      enriched,
      "impaired",
      (manifest.video?.adaptive?.maxIncreaseLossPct ?? 1) / 100,
    ),
    "continued-pressure",
    "the encoder does not increase its target after measured loss exceeds the configured recovery threshold",
  );
  assert(
    assertions,
    (constrained?.capacityUtilization || 0) >= 0.55,
    "constrained-link-efficiency",
    "median video payload uses at least 55% of constrained link capacity",
  );
  assert(
    assertions,
    (impaired?.medianReceivedBitrateKbps || 0) >=
      (impaired?.medianEncoderTargetKbps || Infinity) * 0.85,
    "impaired-target-efficiency",
    "median received video remains at least 85% of the encoder target while loss is injected",
  );
  assert(
    assertions,
    steadyPhaseOrder.every(
      (name) => summaries[name]?.decoderActiveRatio >= 0.95,
    ),
    "decoder-activity",
    "decoded-frame progress is visible in at least 95% of sample intervals",
  );
  assert(
    assertions,
    baseline?.freezeRatio <= 0.02 && recovery?.freezeRatio <= 0.02,
    "healthy-link-freezes",
    "baseline and recovery spend at most 2% of measured time frozen",
  );
  assert(
    assertions,
    constrained?.freezeRatio <= 0.1 && impaired?.freezeRatio <= 0.1,
    "impaired-link-freezes",
    "each shaped phase spends at most 10% of measured time frozen",
  );
  assert(
    assertions,
    baseline?.maximumRTTMilliseconds <= 350 &&
      recovery?.maximumRTTMilliseconds <= 350 &&
      constrained?.maximumRTTMilliseconds <= 600 &&
      impaired?.maximumRTTMilliseconds <= 600,
    "interactive-latency-budget",
    "maximum RTT stays below 350 ms on unshaped links and 600 ms under the 120 ms one-way impairment",
  );
  assert(
    assertions,
    baseline?.decodedFramesPerSecond >= 25 &&
      constrained?.decodedFramesPerSecond >= 20 &&
      impaired?.decodedFramesPerSecond >= 20 &&
      recovery?.decodedFramesPerSecond >= 25,
    "decoded-frame-rate",
    "decoded output stays above 25 fps on healthy links and 20 fps while shaped",
  );
  assert(
    assertions,
    phaseOrder.every(
      (name) =>
        Number.isFinite(summaries[name]?.averageQP) &&
        summaries[name].averageQP >= 0 &&
        summaries[name].averageQP <= 51 &&
        summaries[name].encodedFrames >= 15,
    ),
    "encoder-quality-telemetry",
    "the pinned x264 encoder reports valid per-frame H.264 quantization data in every phase",
  );
  assert(
    assertions,
    !Object.values(encoderQuality || {}).some((phase) =>
      Object.hasOwn(phase, "frameIntervals"),
    ) ||
      qualificationPhaseOrder.every(
        (name) =>
          summaries[name]?.encoderFrameIntervals >= 15 &&
          summaries[name].encoderFrameGapP99Milliseconds <= 50 &&
          summaries[name].encoderMaximumFrameGapMilliseconds <= 200 &&
          summaries[name].encoderLateFrameRatio <= 0.01 &&
          summaries[name].encoderBurstFrameRatio <= 0.01,
      ),
    "encoder-cadence",
    "the encoded source keeps p99 frame gaps at or below 50 ms, every gap at or below 200 ms, and late or catch-up intervals at or below 1% from baseline through recovery",
  );
  assert(
    assertions,
    impaired?.averageQP <= 42,
    "impaired-compression-quality",
    "impaired-link sender average H.264 QP stays at or below 42",
  );
  if (manifest.video?.width && manifest.video?.height) {
    assert(
      assertions,
      phaseOrder.every(
        (name) =>
          summaries[name]?.medianFrameWidth === manifest.video.width &&
          summaries[name]?.medianFrameHeight === manifest.video.height,
      ),
      "decoded-resolution",
      `decoded video remains ${manifest.video.width}x${manifest.video.height} in every phase`,
    );
  }
  const recoveryThreshold = (baseline?.medianEncoderTargetKbps || 0) * 0.8;
  const recoveryCapacityRestoredAfterMilliseconds =
    (manifest.phases.find((phase) => phase.name === "recovery")?.shaping
      ?.schedule?.[0]?.durationSeconds || 0) * 1000;
  const recoveryDelay = timeToEncoderTarget(
    enriched,
    "recovery",
    recoveryThreshold,
    "at-least",
    recoveryCapacityRestoredAfterMilliseconds,
  );
  const sustainedRecoveryMilliseconds = longestEncoderTargetDuration(
    enriched,
    "recovery",
    recoveryThreshold,
    recoveryCapacityRestoredAfterMilliseconds,
  );
  assert(
    assertions,
    !congestionResponseRequired ||
      (recoveryDelay !== null && recoveryDelay <= 35_000),
    "recovery-time",
    congestionResponseRequired
      ? "encoder target returns to at least 80% of its baseline within 35 seconds of the capacity-restoration step"
      : "no recovery ramp is required because the capacity phase did not require a 20% reduction",
  );
  assert(
    assertions,
    !congestionResponseRequired ||
      sustainedRecoveryMilliseconds >= minimumSustainedRecoveryMilliseconds,
    "sustained-recovery",
    congestionResponseRequired
      ? "the encoder sustains at least 80% of its baseline target for 10 seconds after capacity is restored"
      : "no sustained recovery target is required when the capacity phase did not reduce the encoder by 20%",
  );
  assert(
    assertions,
    !congestionResponseRequired ||
      (recovery?.medianReceivedBitrateKbps || 0) >=
        (baseline?.medianReceivedBitrateKbps || Infinity) * 0.6,
    "throughput-recovery",
    congestionResponseRequired
      ? "recovery median receive throughput returns to at least 60% of baseline"
      : "no throughput increase is required when the capacity phase did not reduce the encoder by 20%",
  );
  assert(
    assertions,
    counterIncrease(enriched, "nackCount", ["constrained", "impaired"]) > 0,
    "loss-feedback",
    "network impairment produces NACK feedback",
  );
  if (manifest.webrtc?.rtxNegotiated) {
    assert(
      assertions,
      counterIncrease(enriched, "pacerSentRTX", ["impaired"]) > 0,
      "rtx-sender-pacing",
      "the sender records paced RTX packets while loss is injected",
    );
    if (!manifest.protection?.flexFEC) {
      assert(
        assertions,
        counterIncrease(enriched, "retransmittedPacketsReceived", [
          "impaired",
        ]) > 0,
        "rtx-repair",
        "the receiver observes RTX repair packets while loss is injected without proactive FEC",
      );
    }
    assert(
      assertions,
      (impaired?.nackToPacketRatio || Infinity) <= 0.1,
      "repair-amplification",
      "NACK feedback remains below 10% of received packets during 2% injected loss",
    );
  }
  if (manifest.protection?.flexFEC) {
    assert(
      assertions,
      manifest.webrtc?.flexFECNegotiated,
      "flexfec-negotiation",
      "the browser and producer negotiate the FlexFEC-03 protection stream",
    );
    assert(
      assertions,
      counterIncrease(enriched, "fecPacketsReceived", ["impaired"]) > 0,
      "flexfec-repair",
      "the receiver observes FlexFEC packets while loss is injected",
    );
    assert(
      assertions,
      counterIncrease(enriched, "pacerSentFEC", ["impaired"]) > 0,
      "flexfec-sender-pacing",
      "the sender records paced FlexFEC packets while loss is injected",
    );
    assert(
      assertions,
      enriched.every(
        (sample) =>
          sample.flexFECMediaPackets ===
            manifest.protection.flexFECMediaPackets &&
          sample.flexFECRepairPackets ===
            manifest.protection.flexFECRepairPackets,
      ),
      "flexfec-configuration",
      "runtime telemetry matches the FlexFEC protection recorded by the manifest",
    );
    assert(
      assertions,
      enriched.every((sample) => {
        const expected = protectedPacingEnvelopeKbps(
          sample.pacerTargetBitrateKbps,
        );
        return (
          Number.isFinite(expected) &&
          expected > 0 &&
          Number.isFinite(sample.pacerPacingBitrateKbps) &&
          Math.abs(sample.pacerPacingBitrateKbps - expected) <=
            Math.max(1, expected * 0.001)
        );
      }),
      "flexfec-burst-headroom",
      "the protected wire rate retains the sender's real-time burst headroom",
    );
  }
  assert(
    assertions,
    counterIncrease(enriched, "framesDecoded", phaseOrder) > 0,
    "decoded-video",
    "the browser keeps decoding frames throughout the scenario",
  );
  assert(
    assertions,
    Math.max(0, ...enriched.map((sample) => sample.pacerQueueDrops || 0)) ===
      0 &&
      Math.max(
        0,
        ...enriched.map(
          (sample) => sample.pacerMaximumQueueDelayMilliseconds || 0,
        ),
      ) <= 375 &&
      Math.max(
        0,
        ...enriched.map(
          (sample) => sample.pacerMaximumAdmittedDelayMilliseconds || 0,
        ),
      ) <= 225,
    "bounded-pacer-capacity",
    "the sender never overflows its packet queue, keeps actual packet residence within 375 ms, and admits no new media beyond 225 ms",
  );
  const recoveryTelemetryPresent = enriched.some((sample) =>
    Object.hasOwn(sample, "recoveryKeyFrameRequests"),
  );
  const maximumRecoveryKeyFrameRequests = Math.max(
    0,
    ...enriched.map((sample) => sample.recoveryKeyFrameRequests || 0),
  );
  const maximumRecoveryKeyFrameFailures = Math.max(
    0,
    ...enriched.map((sample) => sample.recoveryKeyFrameFailures || 0),
  );
  const maximumRTCPKeyFrameRequests = Math.max(
    0,
    ...enriched.map((sample) => sample.rtcpKeyFrameRequests || 0),
  );
  const maximumRTCPMalformedFeedback = Math.max(
    0,
    ...enriched.map((sample) => sample.rtcpMalformedFeedback || 0),
  );
  const maximumBrowserPLIRequests = Math.max(
    0,
    ...enriched.map((sample) => sample.pliCount || 0),
  );
  const maximumPacerMediaFrameDrops = Math.max(
    0,
    ...enriched.map((sample) => sample.pacerMediaFramesDropped || 0),
  );
  assert(
    assertions,
    !recoveryTelemetryPresent ||
      (maximumRecoveryKeyFrameFailures === 0 &&
        (maximumPacerMediaFrameDrops === 0 ||
          maximumRecoveryKeyFrameRequests > 0)),
    "pacer-recovery-keyframes",
    "every complete-frame admission drop requests a recovery key frame and encounters no encoder rejection",
  );
  assert(
    assertions,
    !recoveryTelemetryPresent ||
      maximumBrowserPLIRequests === 0 ||
      maximumRTCPKeyFrameRequests > 0,
    "rtcp-keyframe-feedback",
    "receiver PLI feedback reaches the producer's encoder instead of being discarded",
  );
  assert(
    assertions,
    !recoveryTelemetryPresent || maximumRTCPMalformedFeedback === 0,
    "rtcp-feedback-integrity",
    "the producer parses every compound RTCP feedback datagram",
  );
  const adaptiveUpdateTelemetryPresent = enriched.some((sample) =>
    Object.hasOwn(sample, "adaptiveBitrateFailures"),
  );
  assert(
    assertions,
    !adaptiveUpdateTelemetryPresent ||
      Math.max(
        0,
        ...enriched.map((sample) => sample.adaptiveBitrateFailures || 0),
      ) === 0,
    "adaptive-reconfiguration-integrity",
    "the encoder accepts every rate-limited adaptive bitrate reconfiguration",
  );
  const twccTelemetryPresent = enriched.some((sample) =>
    Object.hasOwn(sample, "twccFeedbackPackets"),
  );
  assert(
    assertions,
    !twccTelemetryPresent ||
      (counterIncrease(enriched, "twccFeedbackPackets", phaseOrder) > 0 &&
        Math.max(
          0,
          ...enriched.map((sample) => sample.twccMalformedFeedback || 0),
        ) === 0),
    "twcc-feedback-integrity",
    "TWCC feedback is present and every reported status is parsed without malformed packets",
  );
  const lossGuardTelemetryPresent = enriched.some((sample) =>
    Object.hasOwn(sample, "lossGuardReductions"),
  );
  const sustainedHighLoss = Object.values(summaries).some(
    (phase) => (phase?.twccReportedLossRatio || 0) > 0.1,
  );
  assert(
    assertions,
    !lossGuardTelemetryPresent ||
      !sustainedHighLoss ||
      counterIncrease(enriched, "lossGuardReductions", phaseOrder) > 0,
    "loss-guard-response",
    "persistent TWCC loss above 10% immediately reduces the sender target without waiting for a delay-estimator callback",
  );
  if (manifest.networkImpairment) {
    assert(
      assertions,
      trafficControlSummary.constrainedPackets >= 100 &&
        trafficControlSummary.impairedPackets >= 100,
      "selective-media-shaping",
      "the selective traffic-control branch handles the measured media flow",
    );
    assert(
      assertions,
      trafficControlSummary.constrainedConfiguredLossRatio === 0,
      "capacity-profile-configuration",
      "the capacity phase has no random-loss injector configured",
    );
    assert(
      assertions,
      Math.abs(trafficControlSummary.impairedConfiguredLossRatio - 0.02) <
        0.0001,
      "loss-profile-configuration",
      "the impaired phase configures exactly 2% random packet loss",
    );
    assert(
      assertions,
      trafficControlSummary.constrainedTransitionDropRatio <= 0.15 &&
        trafficControlSummary.constrainedSteadyDropRatio <= 0.05 &&
        trafficControlSummary.impairedDropRatio <= 0.05,
      "traffic-control-drop-budget",
      "capacity-step transients stay below 15%, and steady capacity plus random-loss phases stay below 5% drops",
    );
    assert(
      assertions,
      trafficControlSummary.constrainedEndQueueUtilization <= 0.75 &&
        trafficControlSummary.impairedEndQueueUtilization <= 0.75,
      "traffic-control-queue-headroom",
      "the shaped queue ends each steady interval below 75% occupancy, preventing a larger limit from hiding sustained bufferbloat",
    );
    assert(
      assertions,
      !trafficControlSummary.recoveryDrainEvidenceAvailable ||
        (trafficControlSummary.recoveryDrainConfiguredLossRatio === 0 &&
          trafficControlSummary.recoveryDrainPackets >= 100 &&
          trafficControlSummary.recoveryDrainDrops === 0 &&
          trafficControlSummary.recoveryDrainEndQueuePackets <= 16),
      "traffic-control-recovery-drain",
      "the healthy recovery profile carries media, adds no drops, and leaves at most one short RTP burst queued before traffic-control teardown",
    );
    assert(
      assertions,
      !twccTelemetryPresent ||
        (constrained.twccReportedLossRatio <=
          trafficControlSummary.constrainedDropRatio + 0.08 &&
          impaired.twccReportedLossRatio <=
            trafficControlSummary.impairedDropRatio + 0.08),
      "twcc-loss-fidelity",
      "browser TWCC loss stays within eight percentage points of shaped-link drops, detecting transport-sequence accounting regressions",
    );
  }
  if (receiverUDP.available) {
    assert(
      assertions,
      phaseOrder.every((name) => receiverUDP.phases[name]?.samples >= 10),
      "receiver-udp-observability",
      "receiver-kernel UDP counters cover every measured phase",
    );
    assert(
      assertions,
      receiverUDP.receiveBufferDropsIncrease === 0,
      "receiver-kernel-capacity",
      "the receiver kernel drops no UDP datagram because its socket buffer is full",
    );
  }
  if (producerUDP.available) {
    const producerUnshapedSendDrops = ["warmup", "baseline", "recovery"].reduce(
      (total, name) =>
        total + (producerUDP.phases[name]?.sendBufferDropsIncrease || 0),
      0,
    );
    const producerConstrainedSendDrops =
      producerUDP.phases.constrained?.sendBufferDropsIncrease || 0;
    const producerImpairedSendDrops =
      producerUDP.phases.impaired?.sendBufferDropsIncrease || 0;
    const constrainedBoundaryTolerance =
      trafficControlSummary.constrainedDrops > 0 ? 2 : 0;
    const impairedBoundaryTolerance =
      trafficControlSummary.impairedDrops > 0 ? 2 : 0;
    assert(
      assertions,
      phaseOrder.every((name) => producerUDP.phases[name]?.samples >= 10),
      "producer-udp-observability",
      "producer-kernel UDP counters cover every measured phase",
    );
    assert(
      assertions,
      producerUDP.receiveBufferDropsIncrease === 0 &&
        producerUnshapedSendDrops === 0 &&
        producerConstrainedSendDrops <=
          trafficControlSummary.constrainedDrops +
            constrainedBoundaryTolerance &&
        producerImpairedSendDrops <=
          trafficControlSummary.impairedDrops + impairedBoundaryTolerance,
      "producer-kernel-capacity",
      "the producer kernel has no UDP receive overflow or send rejection outside the independently measured qdisc envelope, allowing at most two datagrams at an asynchronous phase boundary",
    );
  }
  return {
    assertions,
    passed: assertions.every((assertion) => assertion.passed),
    candidatePairSwitches,
    congestionResponseRequired,
    constrainedMediaCapacityKbps,
    healthyLinkTargetKbps,
    healthyLinkTargetRatio,
    responseDelayMilliseconds: responseDelay,
    recoveryDelayMilliseconds: recoveryDelay,
    sustainedRecoveryMilliseconds,
    staleBitrateCallbacks: Math.max(
      0,
      ...enriched.map((sample) => sample.staleBitrateCallbacks || 0),
    ),
    phases: summaries,
    producerUDP,
    receiverUDP,
    receiverHostCPU,
    samples: enriched,
    setup,
    hostCPU,
    networkMobility,
    networkConditions,
    trafficControl: trafficControlSummary,
  };
}

export function alignNetworkConditions(events, samples, manifest) {
  const firstSample = samples.find(
    (sample) =>
      Number.isFinite(sample.elapsedMilliseconds) &&
      Number.isFinite(Date.parse(sample.capturedAt)),
  );
  const expected = networkConditionDefinitions(manifest);
  if (!firstSample || !Array.isArray(events)) {
    return {
      available: false,
      changes: [],
      chronological: false,
      expectedEvents: expected.map((entry) => entry.name),
    };
  }
  const firstCapturedAt = Date.parse(firstSample.capturedAt);
  const definitions = new Map(expected.map((entry) => [entry.name, entry]));
  const changes = events
    .map((event) => {
      const definition = definitions.get(event.name);
      const observedAt = Date.parse(event.observedAt);
      if (!definition || !Number.isFinite(observedAt)) return null;
      return {
        ...definition,
        elapsedMilliseconds:
          firstSample.elapsedMilliseconds + observedAt - firstCapturedAt,
        observedAt: event.observedAt,
      };
    })
    .filter(Boolean);
  const chronological = changes.every(
    (change, index) =>
      index === 0 ||
      change.elapsedMilliseconds > changes[index - 1].elapsedMilliseconds,
  );
  const observedNames = new Set(changes.map((change) => change.name));
  return {
    available:
      chronological &&
      expected.length > 0 &&
      expected.every((entry) => observedNames.has(entry.name)),
    changes,
    chronological,
    expectedEvents: expected.map((entry) => entry.name),
  };
}

function networkConditionDefinitions(manifest) {
  const phases = new Map(
    (manifest.phases || []).map((phase) => [phase.name, phase]),
  );
  const definitions = [];
  const add = (name, shaping) => {
    if (!shaping) return;
    definitions.push({
      capacityKbps: numberOrNull(shaping.capacityKbps),
      delayMs: durationMilliseconds(shaping.delay),
      jitterMs: durationMilliseconds(shaping.jitter),
      lossPercent: parsePercent(shaping.loss),
      name,
    });
  };
  add("conditioning-started", phases.get("conditioning")?.shaping);
  const constrained = phases.get("constrained")?.shaping;
  if (Array.isArray(constrained?.schedule)) {
    const names = [
      "constrained-started",
      "constrained-step-2-started",
      "constrained-step-3-started",
      "constrained-steady-started",
    ];
    constrained.schedule.forEach((step, index) =>
      add(names[index], { ...constrained, ...step }),
    );
  } else {
    add("constrained-started", constrained);
  }
  add("impaired-started", phases.get("impaired")?.shaping);
  const recovery = phases.get("recovery")?.shaping;
  if (Array.isArray(recovery?.schedule)) {
    const names = ["recovery-started", "recovery-capacity-started"];
    recovery.schedule.forEach((step, index) =>
      add(names[index], { ...recovery, ...step }),
    );
  } else {
    add("recovery-started", recovery);
  }
  return definitions;
}

export function renderSVG(analysis, manifest) {
  const samples = analysis.samples;
  const width = 960;
  const height = 580;
  const margin = { top: 110, right: 28, bottom: 110, left: 78 };
  const plotWidth = width - margin.left - margin.right;
  const plotHeight = height - margin.top - margin.bottom;
  const maximumTime = Math.max(
    1,
    ...samples.map((sample) => sample.elapsedMilliseconds),
  );
  const phaseStarts = new Map();
  for (const sample of samples) {
    if (!phaseStarts.has(sample.phase)) {
      phaseStarts.set(sample.phase, sample.elapsedMilliseconds);
    }
  }
  const phases = new Map(manifest.phases.map((phase) => [phase.name, phase]));
  const capacityFor = (sample) => {
    if (analysis.networkConditions?.available) {
      const active = analysis.networkConditions.changes
        .filter(
          (change) => change.elapsedMilliseconds <= sample.elapsedMilliseconds,
        )
        .at(-1);
      return active?.capacityKbps ?? null;
    }
    const phase = phases.get(sample.phase);
    if (!phase?.shaping) return null;
    const schedule = phase.shaping.schedule;
    if (!Array.isArray(schedule) || schedule.length === 0) {
      return phase.shaping.capacityKbps;
    }
    const elapsedSeconds =
      (sample.elapsedMilliseconds - phaseStarts.get(sample.phase)) / 1000;
    let boundary = 0;
    for (const step of schedule) {
      boundary += step.durationSeconds;
      if (elapsedSeconds < boundary) return step.capacityKbps;
    }
    return schedule.at(-1).capacityKbps;
  };
  const capacities = samples.map(capacityFor);
  const maximumBitrate = Math.max(
    1000,
    ...samples
      .flatMap((sample) => [
        sample.encoderTargetKbps,
        sample.twccTargetKbps,
        sample.receivedBitrateKbps,
      ])
      .filter(Number.isFinite),
    ...capacities.filter(Number.isFinite),
    ...(analysis.networkConditions?.changes || [])
      .map((change) => change.capacityKbps)
      .filter(Number.isFinite),
  );
  const x = (milliseconds) =>
    margin.left + (milliseconds / maximumTime) * plotWidth;
  const y = (kilobitsPerSecond) =>
    margin.top +
    plotHeight -
    (Math.min(kilobitsPerSecond, maximumBitrate) / maximumBitrate) * plotHeight;
  const phaseBlocks = manifest.phases
    .map((phase, index) => {
      const phaseSamples = samples.filter(
        (sample) => sample.phase === phase.name,
      );
      if (phaseSamples.length === 0) {
        return "";
      }
      const start = phaseSamples[0].elapsedMilliseconds;
      const end = phaseSamples.at(-1).elapsedMilliseconds;
      const fill = index % 2 === 0 ? "#f3f4f6" : "#e5e7eb";
      const path = phase.shaping ? "controlled link" : "unshaped";
      const label = phase.name === "conditioning" ? "settling" : phase.name;
      return `<rect x="${round(x(start))}" y="${margin.top}" width="${round(Math.max(1, x(end) - x(start)))}" height="${plotHeight}" fill="${fill}"/><text x="${round(x(start) + 7)}" y="${margin.top - 20}" font-size="15" font-weight="600" fill="#374151">${escapeXML(label)}</text><text x="${round(x(start) + 7)}" y="${margin.top - 3}" font-size="13" fill="#6b7280">${path}</text>`;
    })
    .join("");
  const grid = Array.from({ length: 5 }, (_, index) => {
    const value = (maximumBitrate * index) / 4;
    const ordinate = y(value);
    return `<line x1="${margin.left}" y1="${round(ordinate)}" x2="${width - margin.right}" y2="${round(ordinate)}" stroke="#d1d5db"/><text x="${margin.left - 12}" y="${round(ordinate + 5)}" text-anchor="end" font-size="14" fill="#6b7280">${Math.round(value / 100) / 10} Mb/s</text>`;
  }).join("");
  const series = [
    ["Encoder", "#2563eb", "encoderTargetKbps", ""],
    ["TWCC", "#d97706", "twccTargetKbps", ""],
    ["Received", "#059669", "receivedBitrateKbps", ""],
  ];
  const lines = series
    .map(([label, color, field, dash], index) => {
      const points = samples
        .filter((sample) => Number.isFinite(sample[field]) && sample[field] > 0)
        .map(
          (sample) =>
            `${round(x(sample.elapsedMilliseconds))},${round(y(sample[field]))}`,
        )
        .join(" ");
      const legendX = margin.left + index * 150;
      return `<polyline fill="none" stroke="${color}" stroke-width="2.5" stroke-dasharray="${dash}" points="${points}"/><line x1="${legendX}" y1="${height - 28}" x2="${legendX + 28}" y2="${height - 28}" stroke="${color}" stroke-width="3"/><text x="${legendX + 36}" y="${height - 22}" font-size="16" fill="#111827">${label}</text>`;
    })
    .join("");
  const capacityPointList = [];
  let previousCapacity = null;
  const capacityChanges = analysis.networkConditions?.available
    ? analysis.networkConditions.changes
    : samples.map((sample, index) => ({
        capacityKbps: capacities[index],
        elapsedMilliseconds: sample.elapsedMilliseconds,
      }));
  for (const change of capacityChanges) {
    const capacity = change.capacityKbps;
    if (!Number.isFinite(capacity)) continue;
    const abscissa = round(x(change.elapsedMilliseconds));
    if (Number.isFinite(previousCapacity) && capacity !== previousCapacity) {
      capacityPointList.push(`${abscissa},${round(y(previousCapacity))}`);
    }
    if (capacity !== previousCapacity) {
      capacityPointList.push(`${abscissa},${round(y(capacity))}`);
    }
    previousCapacity = capacity;
  }
  if (Number.isFinite(previousCapacity)) {
    capacityPointList.push(
      `${round(x(maximumTime))},${round(y(previousCapacity))}`,
    );
  }
  const capacityPoints = capacityPointList.join(" ");
  const capacityLegendX = margin.left + 3 * 150;
  const capacityLine = `<polyline fill="none" stroke="#be185d" stroke-width="3" stroke-dasharray="8 6" points="${capacityPoints}"/><line x1="${capacityLegendX}" y1="${height - 28}" x2="${capacityLegendX + 28}" y2="${height - 28}" stroke="#be185d" stroke-width="3" stroke-dasharray="8 6"/><text x="${capacityLegendX + 36}" y="${height - 22}" font-size="16" fill="#111827">Configured capacity</text>`;
  const constrainedSchedule =
    manifest.phases.find((phase) => phase.name === "constrained")?.shaping
      ?.schedule || [];
  const isolatedCapacityStep =
    constrainedSchedule.length > 0 &&
    new Set(constrainedSchedule.map((step) => step.delay)).size === 1 &&
    new Set(constrainedSchedule.map((step) => step.jitter)).size === 1 &&
    constrainedSchedule.every(
      (step) => step.loss === undefined || step.loss === "0%",
    );
  const subtitle = isolatedCapacityStep
    ? "Capacity transitions are isolated at " +
      constrainedSchedule[0].delay +
      " delay, " +
      constrainedSchedule[0].jitter +
      " jitter, and 0% injected loss"
    : "Capacity, delay, jitter, and loss follow the recorded phase manifest";
  const resultColor = analysis.passed ? "#047857" : "#b91c1c";
  const resultLabel = analysis.passed ? "PASS" : "FAIL";
  const timeTicks = Array.from({ length: 6 }, (_, index) => {
    const elapsed = (maximumTime * index) / 5;
    const abscissa = round(x(elapsed));
    return `<line x1="${abscissa}" y1="${margin.top + plotHeight}" x2="${abscissa}" y2="${margin.top + plotHeight + 7}" stroke="#111827"/><text x="${abscissa}" y="${margin.top + plotHeight + 25}" text-anchor="middle" font-size="13" fill="#4b5563">${Math.round(elapsed / 1000)} s</text>`;
  }).join("");
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">
  <title id="title">Adaptive sender response to controlled link changes</title>
  <desc id="description">Measured encoder, TWCC, and receive rates are plotted against the independently configured traffic-control schedule.</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="${margin.left}" y="34" font-size="24" font-weight="600" fill="#111827">Adaptive sender response to controlled link changes</text>
  <text x="${margin.left}" y="62" font-size="14" fill="#4b5563">${escapeXML(subtitle)}</text>
  <text x="${width - margin.right}" y="28" text-anchor="end" font-size="16" font-weight="700" fill="${resultColor}">${resultLabel}</text>
  ${phaseBlocks}
  ${grid}
  ${lines}
  ${capacityLine}
  <line x1="${margin.left}" y1="${margin.top + plotHeight}" x2="${width - margin.right}" y2="${margin.top + plotHeight}" stroke="#111827"/>
  ${timeTicks}
  <text x="${width / 2}" y="${height - 62}" text-anchor="middle" font-size="14" fill="#6b7280">Elapsed time · capacity line exists only while traffic control is active</text>
</svg>
`;
}

export function renderMarkdown(analysis, manifest) {
  const status = analysis.passed ? "PASS" : "FAIL";
  const phaseRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.samples} | ${formatNumber(phase.connectedRatio * 100, 1)}% | ${formatNumber(phase.medianReceivedBitrateKbps, 0)} | ${phase.capacityKbps ? `${formatNumber(phase.capacityUtilization * 100, 1)}%` : "n/a"} | ${formatNumber(phase.medianTWCCKbps, 0)} | ${formatNumber(phase.medianEncoderTargetKbps, 0)} | ${formatNumber(phase.decodedFramesPerSecond, 1)} | ${formatNumber(phase.averageQP, 1)} | ${formatNumber(phase.averageDecodeMilliseconds, 2)} | ${formatNumber(phase.freezeRatio * 100, 1)}% | ${phase.nackIncrease} | ${phase.retransmittedPacketsIncrease} | ${phase.fecPacketsIncrease} | ${formatNumber(phase.maximumRTTMilliseconds, 0)} |`,
    )
    .join("\n");
  const assertions = analysis.assertions
    .map(
      (assertion) =>
        `- ${assertion.passed ? "PASS" : "FAIL"} — ${assertion.name}: ${assertion.description}`,
    )
    .join("\n");
  const baseline = analysis.phases.baseline;
  const conditioning = analysis.phases.conditioning;
  const constrained = analysis.phases.constrained;
  const impaired = analysis.phases.impaired;
  const recovery = analysis.phases.recovery;
  const rateReduction =
    baseline?.medianEncoderTargetKbps > 0
      ? 1 -
        constrained.medianEncoderTargetKbps / baseline.medianEncoderTargetKbps
      : null;
  const receiveRecoveryRatio =
    baseline?.medianReceivedBitrateKbps > 0
      ? recovery.medianReceivedBitrateKbps / baseline.medianReceivedBitrateKbps
      : null;
  const capacityIsolation =
    conditioning?.endingEncoderTargetKbps > 0 &&
    baseline?.medianEncoderTargetKbps > 0
      ? conditioning.endingEncoderTargetKbps / baseline.medianEncoderTargetKbps
      : null;
  const phaseValues = Object.values(analysis.phases);
  const maximumQueueResidence = Math.max(
    0,
    ...phaseValues.map(
      (phase) => phase.maximumPacerQueueDelayMilliseconds || 0,
    ),
  );
  const maximumAdmittedBacklog = Math.max(
    0,
    ...phaseValues.map(
      (phase) => phase.maximumPacerAdmittedDelayMilliseconds || 0,
    ),
  );
  const queueDrops = phaseValues.reduce(
    (total, phase) => total + (phase.pacerQueueDropsIncrease || 0),
    0,
  );
  const repairObserved = manifest.webrtc?.rtxNegotiated
    ? manifest.protection?.flexFEC
      ? `NACK ${impaired.nackIncrease}; sender RTX ${impaired.pacerSentRTXIncrease}; receiver RTX ${impaired.retransmittedPacketsIncrease}; FlexFEC ${impaired.fecPacketsIncrease}`
      : `NACK ${impaired.nackIncrease}; sender RTX ${impaired.pacerSentRTXIncrease}; receiver RTX ${impaired.retransmittedPacketsIncrease}`
    : `NACK ${impaired.nackIncrease}`;
  const repairRequired = manifest.webrtc?.rtxNegotiated
    ? manifest.protection?.flexFEC
      ? "NACK, sender RTX, and FlexFEC greater than zero"
      : "NACK, sender RTX, and receiver RTX greater than zero"
    : "NACK greater than zero";
  const decisionRows = [
    ...(Number.isFinite(analysis.healthyLinkTargetRatio)
      ? [
          [
            "Healthy-link quality",
            `${formatNumber(analysis.healthyLinkTargetRatio * 100, 1)}% of baseline at or above ${formatNumber(analysis.healthyLinkTargetKbps, 0)} kbps`,
            "at least 50% of baseline",
          ],
        ]
      : []),
    [
      "Shaper activation",
      capacityIsolation === null
        ? "not measured"
        : `${formatNumber(capacityIsolation * 100, 1)}% of baseline before the first capacity step`,
      conditioning ? "at least 80%" : "not applicable",
    ],
    [
      "Rate response",
      analysis.congestionResponseRequired
        ? `${formatNumber(rateReduction * 100, 1)}% reduction in ${formatDuration(analysis.responseDelayMilliseconds)}`
        : `not required; baseline already at ${formatNumber(baseline.medianEncoderTargetKbps, 0)} kbps`,
      analysis.congestionResponseRequired
        ? "at least 20% within 30 s"
        : "no increase under additional pressure",
    ],
    [
      "Rate recovery",
      analysis.congestionResponseRequired
        ? `${formatDuration(analysis.recoveryDelayMilliseconds)} to threshold; sustained for ${formatDuration(analysis.sustainedRecoveryMilliseconds)}; received throughput ${formatNumber(receiveRecoveryRatio * 100, 1)}% of baseline`
        : "not required; capacity phase did not reduce the target",
      analysis.congestionResponseRequired
        ? "target reaches 80% within 35 s, sustains it for 10 s, and median throughput reaches at least 60% of baseline"
        : "not applicable",
    ],
    [
      "Playback under impairment",
      `${formatNumber(impaired.decodedFramesPerSecond, 1)} fps; ${formatNumber(impaired.freezeRatio * 100, 1)}% frozen`,
      "at least 20 fps; at most 10% frozen",
    ],
    [
      "Latency under impairment",
      `${formatNumber(impaired.maximumRTTMilliseconds, 0)} ms max RTT; ${formatNumber(impaired.averageJitterBufferDelayMilliseconds, 1)} ms effective playout buffer`,
      "at most 600 ms RTT; at most 300 ms phase-average buffer",
    ],
    [
      "Visual quality under impairment",
      `QP ${formatNumber(impaired.averageQP, 1)}; ${impaired.medianFrameWidth}x${impaired.medianFrameHeight}`,
      `QP at most 42; ${manifest.video?.width || "recorded"}x${manifest.video?.height || "resolution"}`,
    ],
    [
      "Loss fidelity",
      `qdisc ${formatNumber(analysis.trafficControl?.impairedDropRatio * 100, 2)}%; TWCC ${formatNumber(impaired.twccReportedLossRatio * 100, 2)}%`,
      "2% injected; TWCC within 8 percentage points",
    ],
    ["Packet repair", repairObserved, repairRequired],
    [
      "Sender queue",
      `${formatNumber(maximumQueueResidence, 1)} ms residence; ${formatNumber(maximumAdmittedBacklog, 1)} ms admitted backlog; ${queueDrops} overflow drops`,
      "at most 375 ms; at most 225 ms; zero overflow",
    ],
  ]
    .map(
      ([gate, observed, required]) => `| ${gate} | ${observed} | ${required} |`,
    )
    .join("\n");
  const controllerRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${formatNumber(phase.medianLossTargetKbps, 0)} | ${formatNumber(phase.medianDelayTargetKbps, 0)} | ${formatNumber(phase.medianLossGuardTargetKbps, 0)} | ${formatNumber(phase.maximumLossGuardObservedLoss * 100, 2)}% | ${phase.lossGuardReductionsIncrease} / ${phase.lossGuardRecoveriesIncrease} | ${formatNumber(phase.medianAverageLoss * 100, 2)}% | ${formatNumber(phase.twccReportedLossRatio * 100, 2)}% | ${phase.adaptiveBitrateUpdatesIncrease} | ${phase.adaptiveBitrateFailuresIncrease} | ${phase.twccFeedbackPacketsIncrease} | ${phase.twccPaddingStatusesIncrease} | ${phase.twccMalformedFeedbackIncrease} |`,
    )
    .join("\n");
  const pacingRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${formatNumber(phase.maximumPacerQueueDelayMilliseconds, 1)} | ${formatNumber(phase.maximumPacerPrimaryDelayMilliseconds, 1)} | ${formatNumber(phase.maximumPacerRepairDelayMilliseconds, 1)} | ${formatNumber(phase.maximumPacerAdmittedDelayMilliseconds, 1)} | ${formatNumber(phase.maximumPacerSustainedDelayMilliseconds, 1)} | ${phase.maximumPacerQueuePackets} | ${phase.maximumPacerKeyFrameReserveBytes} | ${phase.pacerMediaFrameDropsIncrease} | ${phase.pacerRepairPacketsExpiredIncrease} | ${phase.pacerRepairPacketsTrimmedIncrease} | ${phase.recoveryKeyFrameRequestsIncrease} | ${phase.recoveryKeyFrameCoalescedIncrease} | ${phase.rtcpKeyFrameRequestsIncrease} | ${phase.rtcpMalformedFeedbackIncrease} | ${phase.recoveryKeyFrameFailuresIncrease} | ${phase.pacerQueueDropsIncrease} |`,
    )
    .join("\n");
  const repairRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${formatNumber(phase.maximumPacerFECDelayMilliseconds, 1)} | ${phase.pacerSentFECIncrease} | ${phase.pacerFECPacketsExpiredIncrease} | ${phase.pacerFECPacketsTrimmedIncrease} | ${formatNumber(phase.maximumPacerRTXDelayMilliseconds, 1)} | ${phase.pacerSentRTXIncrease} | ${phase.pacerRTXPacketsExpiredIncrease} | ${phase.pacerRTXPacketsTrimmedIncrease} |`,
    )
    .join("\n");
  const encoderCadenceRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.encoderFrameIntervals} | ${formatNumber(phase.encoderFrameGapP99Milliseconds, 1)} | ${formatNumber(phase.encoderMaximumFrameGapMilliseconds, 1)} | ${formatNumber(phase.encoderLateFrameRatio * 100, 2)}% | ${formatNumber(phase.encoderBurstFrameRatio * 100, 2)}% |`,
    )
    .join("\n");
  const hostCPURows = Object.entries(analysis.hostCPU?.phases || {})
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.samples} | ${formatNumber(phase.activeMedianRatio * 100, 1)}% | ${formatNumber(phase.stealP95Ratio * 100, 1)}% | ${formatNumber(phase.stealMaximumRatio * 100, 1)}% | ${formatNumber(phase.samplingGapP99Milliseconds, 0)} ms | ${formatNumber(phase.samplingGapMaximumMilliseconds, 0)} ms |`,
    )
    .join("\n");
  const hostCPUSection = analysis.hostCPU?.available
    ? `## Producer host scheduling

Linux aggregate CPU counters are sampled from the producer's host namespace.
They expose time withheld by the hypervisor separately from work performed by
the application. A run with sustained steal time cannot establish a transport
performance result because the source itself was not scheduled predictably.
The 250 ms sampling heartbeat also exposes shorter pauses that aggregate CPU
counters cannot attribute.
The producer runtime reports ${manifest.runtime?.logicalCPUs || "an unknown number of"} logical CPUs.

Source: \`${analysis.hostCPU.source}\`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
${hostCPURows}

`
    : "";
  const receiverHostCPURows = Object.entries(
    analysis.receiverHostCPU?.phases || {},
  )
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.samples} | ${formatNumber(phase.activeMedianRatio * 100, 1)}% | ${formatNumber(phase.stealP95Ratio * 100, 1)}% | ${formatNumber(phase.stealMaximumRatio * 100, 1)}% | ${formatNumber(phase.samplingGapP99Milliseconds, 0)} ms | ${formatNumber(phase.samplingGapMaximumMilliseconds, 0)} ms |`,
    )
    .join("\n");
  const receiverHostCPUSection = analysis.receiverHostCPU?.available
    ? `## Receiver host scheduling

Linux aggregate CPU counters are sampled from the receiver's host namespace.
They distinguish media-path latency from time when the hypervisor prevented the
browser host from running. The 250 ms sampling heartbeat also exposes shorter
runtime pauses. The receiver runtime reports ${manifest.browserRuntime?.logicalCPUs || "an unknown number of"} logical CPUs.

Source: \`${analysis.receiverHostCPU.source}\`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
${receiverHostCPURows}

`
    : "";
  const playoutRows = Object.entries(analysis.phases)
    .map(
      ([name, phase]) =>
        `| ${name} | ${formatNumber(phase.averageJitterBufferDelayMilliseconds, 1)} | ${formatNumber(phase.averageJitterBufferTargetMilliseconds, 1)} |`,
    )
    .join("\n");
  const playoutSection = Number.isFinite(
    manifest.video?.playoutDelayHintSeconds,
  )
    ? `## Receiver playout latency

The receiver uses a bounded jitter buffer to absorb packet timing variation and
leave time for repair. Both columns come from cumulative WebRTC receiver
counters. The configured minimum hint is ${formatNumber(manifest.video.playoutDelayHintSeconds * 1000, 0)} ms. Qualification caps the requested target at 250 ms and each phase's average effective buffered delay at 300 ms. The synchronized transport figure retains per-sample values so shorter excursions remain visible.

| Phase | Average buffered delay ms/frame | Average target delay ms/frame |
| --- | ---: | ---: |
${playoutRows}

`
    : "";
  const impairmentDescription =
    manifest.networkImpairment?.scope === "producer-turn-transport"
      ? "The impairment applies to the selected producer-to-TURN transport. Media, TURN permissions, and TURN channel traffic share that physical branch; rstream publication and HTTP signaling remain outside it."
      : "The direct impairment applies to every outbound UDP packet on the isolated producer-to-browser address. It follows legitimate ICE candidate-port switches while excluding host and unrelated container traffic.";
  const receiverRows = Object.entries(analysis.receiverUDP?.phases || {})
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.samples} | ${phase.datagramsReceivedIncrease} | ${phase.datagramsSentIncrease} | ${phase.inputErrorsIncrease} | ${phase.noPortDropsIncrease} | ${phase.receiveBufferDropsIncrease} | ${phase.sendBufferDropsIncrease} |`,
    )
    .join("\n");
  const receiverSection = analysis.receiverUDP?.available
    ? `## Receiver-kernel UDP diagnostics

These independent kernel counters distinguish upstream packet loss from a local
browser socket that could not drain its receive buffer. The qualification
browser is sampled inside its isolated Linux network namespace, so the counters
exclude unrelated host and container traffic.

Source: \`${analysis.receiverUDP.source}\`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${receiverRows}

`
    : "";
  const producerRows = Object.entries(analysis.producerUDP?.phases || {})
    .map(
      ([name, phase]) =>
        `| ${name} | ${phase.samples} | ${phase.datagramsReceivedIncrease} | ${phase.datagramsSentIncrease} | ${phase.inputErrorsIncrease} | ${phase.noPortDropsIncrease} | ${phase.receiveBufferDropsIncrease} | ${phase.sendBufferDropsIncrease} |`,
    )
    .join("\n");
  const producerSection = analysis.producerUDP?.available
    ? `## Producer-kernel UDP diagnostics

These counters come from the producer container's isolated Linux network
namespace. Together with the receiver table they show whether a missing RTP
sequence was dropped by a local socket or disappeared between two healthy
kernel boundaries. Linux may account a \`netem\` rejection in both the qdisc
drop counter and UDP \`SndbufErrors\`; therefore send-buffer errors are accepted
only in shaped phases and only up to each interval's independently measured
qdisc drops. Any receive overflow, unshaped-phase send rejection, or excess over
those bounds fails the run.

Source: \`${analysis.producerUDP.source}\`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${producerRows}

`
    : "";
  const setupRows = (analysis.setup?.milestones || [])
    .map(
      (milestone) =>
        `| ${milestone.name} | ${milestone.sincePreviousMilliseconds === null ? "n/a" : formatDuration(milestone.sincePreviousMilliseconds)} |`,
    )
    .join("\n");
  const setupSection = analysis.setup?.available
    ? `## Qualification setup timeline

Build time is reported separately from service establishment. The
\`connection-started\` to \`media-connected\` interval covers producer startup,
rstream publication, browser startup, signaling, ICE, and first selected media
path; it does not include Docker image builds.

| Milestone | Time since previous milestone |
| --- | ---: |
${setupRows}

Measured service establishment: ${formatDuration(analysis.setup.connectionMilliseconds)}.

`
    : "";
  const mobilitySection = manifest.networkMobility
    ? `## Producer network mobility

The qualification moves the running producer between two isolated network
interfaces with distinct source addresses. The same browser page, WebRTC peer,
and signaling WebSocket must remain in place while Trickle ICE publishes the
new path. This separates transport mobility from a hidden page reload or a new
viewer session.

Candidate-pair switches: ${analysis.networkMobility.candidatePairSwitches}. Fresh remote candidates: ${analysis.networkMobility.trickledRemoteCandidates}. ICE restart offers: ${analysis.networkMobility.iceRestartOffers}. Longest playback interruption: ${formatDuration(analysis.networkMobility.maximumUnavailableMilliseconds)}.

The event timeline is recorded in \`signaling-events.json\`.

`
    : "";
  return `# Adaptive streaming qualification — ${status}

Generated at ${manifest.generatedAt} from repository revision \`${manifest.git.revision}\`${manifest.git.dirty ? " with uncommitted changes" : ""}.

![Adaptive bitrate response](./adaptive-bitrate.svg)

${Number.isFinite(manifest.video?.adaptive?.maximumBitrateKbps) ? `The media controller starts at ${manifest.video.adaptive.initialBitrateKbps} kbps and operates from ${manifest.video.adaptive.minimumBitrateKbps} through ${manifest.video.adaptive.maximumBitrateKbps} kbps. Its ${manifest.video.adaptive.changeThresholdPct}% hysteresis keeps a healthy-link target stable once it is close to the configured ceiling.` : ""}

${setupSection}${mobilitySection}## Phase summary

| Phase | Samples | Connected | Received kbps (median) | Link use | TWCC kbps (median) | Encoder kbps (median) | Decoded fps | Avg QP | Decode ms/frame | Frozen | NACK | RTX packets | FEC packets | Max RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${phaseRows}

Congestion response: ${analysis.congestionResponseRequired ? formatDuration(analysis.responseDelayMilliseconds) : "not required (baseline target fits the constrained media budget)"}. Recovery response: ${analysis.congestionResponseRequired ? formatDuration(analysis.recoveryDelayMilliseconds) : "not required"}.

Selected ICE candidate-pair switches: ${analysis.candidatePairSwitches}. ${manifest.networkPath?.kind === "relay" ? "Both peers remain on the required TURN relay path." : "The direct-path address selector remains active across port changes."}

Superseded out-of-order estimator callbacks: ${analysis.staleBitrateCallbacks}. The producer always applies the estimator's current target rather than the potentially stale callback payload.

## Qualification decision

| Gate | Observed | Required |
| --- | ---: | ---: |
${decisionRows}

## Congestion-controller diagnostics

| Phase | Loss target kbps | Delay target kbps | Guard target kbps | Peak report loss | Guard reduce / recover | Controller loss | Browser TWCC loss | Encoder updates | Update failures | Feedback packets | Padding statuses | Malformed feedback |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${controllerRows}

## Real-time sender queue

The sender drops complete encoded access units before RTP packetization when
its sustained-rate backlog would exceed 225 ms. It schedules a recovery key
frame once the bounded queue has room and resumes on that frame, avoiding
partial-frame corruption, multi-second GOP waits, and bufferbloat. Requests caused by one
congestion event or by repeated receiver PLI/FIR feedback are coalesced to one
encoder request per 250 ms, preventing recovery key frames from becoming their
own burst-amplification loop. A rejected recovery request or packetized RTP
rejection remains a hard failure. A sudden estimator decrease is applied to the
encoder and new media immediately. A frame that was already accepted and
packetized drains at its admission rate: slowing or deleting part of that RTP
frame would create sequence holes and multi-second latency. This bounded
transition is exposed by the actual-residence and scheduled-backlog columns.
Queued FEC and RTX are purged on a rate decrease rather than consuming the new
budget for stale repair. A recovery
key-frame request is deferred until the queue has room for the most recently
observed key-frame size plus 25% headroom, avoiding a request that would produce
another key frame only to discard it. Admission accounts for every queued
primary and FEC service interval, plus the single RTX packet that scheduling may
place before each queued frame. Repair packets older than 225 ms are expired
rather than consuming bandwidth after their media window. Expiration is
reported separately from queue overflow. High-water values are cumulative
process counters, so a later phase retains an earlier peak unless it establishes
a new one; the packet count is sampled within each phase.

| Phase | Any packet residence ms | Primary residence ms | Repair residence ms | Admitted backlog ms | Scheduled backlog ms | Maximum sampled packets | Key-frame reserve bytes | Complete frames dropped | Expired repair packets | Rate-trimmed repair packets | Encoder requests | Coalesced requests | Receiver PLI/FIR | Malformed RTCP | Request failures | RTP queue overflows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${pacingRows}

### Repair timeliness

FEC is paced immediately after each protected ${manifest.protection?.flexFECMediaPackets || 0}-packet media group so it can
arrive before playout. RTX remains at media-frame boundaries because it repairs
an already reported loss and must not delay completion of the current frame.
The split counters below make a late proactive repair distinguishable from an
expired retransmission.

| Phase | Max FEC residence ms | FEC sent | FEC expired | FEC rate-trimmed | Max RTX residence ms | RTX sent | RTX expired | RTX rate-trimmed |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
${repairRows}

${hostCPUSection}${receiverHostCPUSection}## Encoder cadence and observer effect

The performance result is meaningful only if its source produces frames on
time. Per-frame x264 evidence is written to container-local tmpfs and copied
only after the pipeline stops, not streamed through Docker's logging path; this
prevents detailed diagnostics or host I/O from blocking the encoder being
measured. At 30 fps, a late interval exceeds
50 ms and a catch-up burst is shorter than 16.7 ms. Qualification requires a
p99 gap no higher than 50 ms, no individual gap above 200 ms, and no more
than 1% late or catch-up intervals in any measured phase. The p99 and maximum
columns keep isolated scheduler jitter visible rather than misclassifying it as
network loss or hiding it behind an aggregate frame rate.

| Phase | Measured intervals | p99 frame gap ms | Maximum frame gap ms | Late intervals | Catch-up bursts |
| --- | ---: | ---: | ---: | ---: | ---: |
${encoderCadenceRows}

## Network-emulation fidelity

${impairmentDescription}

| Interval | Configured random loss | Shaped packets | Total qdisc drops | Total drop ratio | Ending queue |
| --- | ---: | ---: | ---: | ---: | ---: |
| capacity transitions | ${formatNumber(analysis.trafficControl.constrainedConfiguredLossRatio * 100, 2)}% | ${analysis.trafficControl.constrainedTransitionPackets} | ${analysis.trafficControl.constrainedTransitionDrops} | ${formatNumber(analysis.trafficControl.constrainedTransitionDropRatio * 100, 2)}% | n/a |
| constrained steady state | ${formatNumber(analysis.trafficControl.constrainedConfiguredLossRatio * 100, 2)}% | ${analysis.trafficControl.constrainedSteadyPackets} | ${analysis.trafficControl.constrainedSteadyDrops} | ${formatNumber(analysis.trafficControl.constrainedSteadyDropRatio * 100, 2)}% | ${analysis.trafficControl.constrainedEndQueuePackets}/${analysis.trafficControl.constrainedQueueLimitPackets} (${formatNumber(analysis.trafficControl.constrainedEndQueueUtilization * 100, 1)}%) |
| impaired (incremental) | ${formatNumber(analysis.trafficControl.impairedConfiguredLossRatio * 100, 2)}% | ${analysis.trafficControl.impairedPackets} | ${analysis.trafficControl.impairedDrops} | ${formatNumber(analysis.trafficControl.impairedDropRatio * 100, 2)}% | ${analysis.trafficControl.impairedEndQueuePackets}/${analysis.trafficControl.impairedQueueLimitPackets} (${formatNumber(analysis.trafficControl.impairedEndQueueUtilization * 100, 1)}%) |
${analysis.trafficControl.recoveryDrainEvidenceAvailable ? `| recovery drain | ${formatNumber(analysis.trafficControl.recoveryDrainConfiguredLossRatio * 100, 2)}% | ${analysis.trafficControl.recoveryDrainPackets} | ${analysis.trafficControl.recoveryDrainDrops} | ${formatNumber(analysis.trafficControl.recoveryDrainDropRatio * 100, 2)}% | ${analysis.trafficControl.recoveryDrainEndQueuePackets}/${analysis.trafficControl.recoveryDrainQueueLimitPackets} (${formatNumber(analysis.trafficControl.recoveryDrainEndQueueUtilization * 100, 1)}%) |` : ""}

Total qdisc drops include both configured random loss and queue overflow while the congestion controller reacts. Capacity-transition counters are separated from the final steady interval so a bounded reaction transient cannot hide sustained overload, and steady behavior cannot hide a destructive transition.

${playoutSection}${receiverSection}${producerSection}## Acceptance criteria

${assertions}

The checked-in summary is evidence for one pinned run, not a universal performance guarantee. Re-run \`./run.sh\` on the target architecture before using the result as an acceptance decision.
`;
}

export async function writeArtifacts(outputDirectory, analysis, manifest) {
  const summary = { ...analysis };
  delete summary.samples;
  await writeFile(
    `${outputDirectory}/summary.json`,
    `${JSON.stringify(summary, null, 2)}\n`,
  );
  await writeFile(
    `${outputDirectory}/summary.md`,
    renderMarkdown(analysis, manifest),
  );
  await writeFile(
    `${outputDirectory}/adaptive-bitrate.svg`,
    renderSVG(analysis, manifest),
  );
  await writeFile(
    `${outputDirectory}/network-conditions.svg`,
    renderNetworkConditionsSVG(analysis, manifest),
  );
  await writeFile(
    `${outputDirectory}/playback-quality.svg`,
    renderPlaybackQualitySVG(analysis, manifest),
  );
  await writeFile(
    `${outputDirectory}/transport-evidence.svg`,
    renderTransportEvidenceSVG(analysis, manifest),
  );
  const columns = [
    "capturedAt",
    "elapsedMilliseconds",
    "phase",
    "receivedBitrateKbps",
    "twccTargetKbps",
    "encoderTargetKbps",
    "lossTargetKbps",
    "delayTargetKbps",
    "lossAverage",
    "lossGuardTargetKbps",
    "lossGuardLastObservedLoss",
    "lossGuardReductions",
    "lossGuardRecoveries",
    "flexFECMediaPackets",
    "flexFECRepairPackets",
    "delayMeasurementMilliseconds",
    "delayEstimateMilliseconds",
    "delayThresholdMilliseconds",
    "delayControllerUsage",
    "delayControllerState",
    "framesPerSecond",
    "framesDecoded",
    "framesDropped",
    "freezeCount",
    "totalFreezesDurationSeconds",
    "frameWidth",
    "frameHeight",
    "totalDecodeTimeSeconds",
    "packetsLost",
    "packetsReceived",
    "nackCount",
    "retransmittedPacketsReceived",
    "retransmittedBytesReceived",
    "fecPacketsReceived",
    "fecPacketsDiscarded",
    "pacerTargetBitrateKbps",
    "pacerPacingBitrateKbps",
    "pacerQueuePackets",
    "pacerQueueDrops",
    "pacerQueueDelayMilliseconds",
    "pacerMaximumQueueDelayMilliseconds",
    "pacerMaximumPrimaryDelayMilliseconds",
    "pacerMaximumRepairDelayMilliseconds",
    "pacerMaximumRTXDelayMilliseconds",
    "pacerMaximumFECDelayMilliseconds",
    "pacerMaximumSustainedDelayMilliseconds",
    "pacerMaximumAdmittedDelayMilliseconds",
    "pacerKeyFrameReserveBytes",
    "pacerMediaFramesDropped",
    "pacerMediaBytesDropped",
    "pacerRepairPacketsExpired",
    "pacerRepairPacketsTrimmed",
    "pacerRTXPacketsExpired",
    "pacerFECPacketsExpired",
    "pacerRTXPacketsTrimmed",
    "pacerFECPacketsTrimmed",
    "pacerSentPrimary",
    "pacerSentRepair",
    "pacerSentRTX",
    "pacerSentFEC",
    "adaptiveBitrateUpdates",
    "adaptiveBitrateFailures",
    "recoveryKeyFrameRequests",
    "recoveryKeyFrameCoalesced",
    "recoveryKeyFrameFailures",
    "rtcpKeyFrameRequests",
    "rtcpMalformedFeedback",
    "staleBitrateCallbacks",
    "twccFeedbackPackets",
    "twccMalformedFeedback",
    "twccPaddingStatuses",
    "twccReportedLost",
    "twccReportedStatuses",
    "pliCount",
    "currentRoundTripTimeSeconds",
    "jitterSeconds",
    "jitterBufferDelaySeconds",
    "jitterBufferEmittedCount",
    "jitterBufferTargetDelaySeconds",
    "peerConnectionState",
    "peerConnectionID",
    "peerConnectionsCreated",
    "iceConnectionState",
    "iceRestartOffers",
    "offersSent",
    "localCandidatesSent",
    "remoteCandidatesReceived",
    "webSocketID",
    "webSocketOpenCount",
    "webSocketCloseCount",
    "webSocketsCreated",
    "webSocketState",
    "localCandidateType",
    "localCandidateAddress",
    "localCandidatePort",
    "localCandidateProtocol",
    "remoteCandidateType",
    "remoteCandidateAddress",
    "remoteCandidatePort",
    "remoteCandidateProtocol",
    "playoutDelayHintSeconds",
    "playback",
    "videoCurrentTimeSeconds",
    "videoReadyState",
  ];
  const rows = [
    columns.join(","),
    ...analysis.samples.map((sample) =>
      columns.map((column) => csvValue(sample[column])).join(","),
    ),
  ];
  await writeFile(`${outputDirectory}/metrics.csv`, `${rows.join("\n")}\n`);
}

function countCandidatePairSwitches(samples) {
  let previous = null;
  let switches = 0;
  for (const sample of samples) {
    const localPort = Number(sample.localCandidatePort);
    const remotePort = Number(sample.remoteCandidatePort);
    if (
      !Number.isInteger(localPort) ||
      localPort <= 0 ||
      !Number.isInteger(remotePort) ||
      remotePort <= 0
    ) {
      continue;
    }
    const current = [
      sample.localCandidateProtocol || "",
      sample.localCandidateAddress || "",
      localPort,
      sample.remoteCandidateProtocol || "",
      sample.remoteCandidateAddress || "",
      remotePort,
    ].join("|");
    if (previous !== null && current !== previous) {
      switches += 1;
    }
    previous = current;
  }
  return switches;
}

export function summarizeNetworkMobility(samples) {
  const mobility = samples.filter((sample) => sample.phase === "mobility");
  if (mobility.length === 0) {
    return {
      available: false,
      candidatePairSwitches: 0,
      iceRestartOffers: 0,
      maximumUnavailableMilliseconds: 0,
      peerConnectionsCreated: 0,
      trickledRemoteCandidates: 0,
      webSocketCloses: 0,
      webSocketsCreated: 0,
    };
  }
  const previous = [...samples]
    .reverse()
    .find(
      (sample) =>
        sample.phase !== "mobility" &&
        sample.elapsedMilliseconds < mobility[0].elapsedMilliseconds,
    );
  const pathSamples = previous ? [previous, ...mobility] : mobility;
  let unavailableSince = null;
  let maximumUnavailableMilliseconds = 0;
  for (const sample of mobility) {
    const available =
      sample.peerConnectionState === "connected" &&
      sample.playback === "Playing";
    if (!available && unavailableSince === null) {
      unavailableSince = sample.elapsedMilliseconds;
    } else if (available && unavailableSince !== null) {
      maximumUnavailableMilliseconds = Math.max(
        maximumUnavailableMilliseconds,
        sample.elapsedMilliseconds - unavailableSince,
      );
      unavailableSince = null;
    }
  }
  if (unavailableSince !== null) {
    maximumUnavailableMilliseconds = Math.max(
      maximumUnavailableMilliseconds,
      mobility.at(-1).elapsedMilliseconds - unavailableSince,
    );
  }
  const first = previous || mobility[0];
  const last = mobility.at(-1);
  return {
    available: true,
    candidatePairSwitches: countCandidatePairSwitches(pathSamples),
    iceRestartOffers: Math.max(
      0,
      (last.iceRestartOffers || 0) - (first.iceRestartOffers || 0),
    ),
    maximumUnavailableMilliseconds,
    peerConnectionsCreated: Math.max(
      0,
      ...mobility.map((sample) => sample.peerConnectionsCreated || 0),
    ),
    trickledRemoteCandidates: Math.max(
      0,
      (last.remoteCandidatesReceived || 0) -
        (first.remoteCandidatesReceived || 0),
    ),
    webSocketCloses: Math.max(
      0,
      (last.webSocketCloseCount || 0) - (first.webSocketCloseCount || 0),
    ),
    webSocketsCreated: Math.max(
      0,
      ...mobility.map((sample) => sample.webSocketsCreated || 0),
    ),
  };
}

export function summarizeReceiverUDP(samples, phaseOrder) {
  if (!Array.isArray(samples) || samples.length === 0) {
    return {
      available: false,
      phases: {},
      receiveBufferDropsIncrease: 0,
      sendBufferDropsIncrease: 0,
      source: null,
    };
  }
  const fields = [
    "datagramsReceived",
    "datagramsSent",
    "inputErrors",
    "noPortDrops",
    "receiveBufferDrops",
    "sendBufferDrops",
  ];
  const phases = Object.fromEntries(
    phaseOrder.map((phase) => [
      phase,
      {
        datagramsReceivedIncrease: 0,
        datagramsSentIncrease: 0,
        inputErrorsIncrease: 0,
        noPortDropsIncrease: 0,
        receiveBufferDropsIncrease: 0,
        sendBufferDropsIncrease: 0,
        samples: 0,
      },
    ]),
  );
  let previous = null;
  for (const sample of samples) {
    const phase = phases[sample.phase];
    if (phase) {
      phase.samples += 1;
      if (previous) {
        for (const field of fields) {
          const start = Number(previous[field]);
          const end = Number(sample[field]);
          if (Number.isFinite(start) && Number.isFinite(end)) {
            phase[`${field}Increase`] += counterDelta(start, end);
          }
        }
      }
    }
    previous = sample;
  }
  return {
    available: true,
    phases,
    receiveBufferDropsIncrease: Object.values(phases).reduce(
      (total, phase) => total + phase.receiveBufferDropsIncrease,
      0,
    ),
    sendBufferDropsIncrease: Object.values(phases).reduce(
      (total, phase) => total + phase.sendBufferDropsIncrease,
      0,
    ),
    source: samples[0].source || "unknown",
  };
}

export function summarizeSetup(milestones) {
  if (!Array.isArray(milestones) || milestones.length === 0) {
    return { available: false, connectionMilliseconds: null, milestones: [] };
  }
  let previous = null;
  const normalized = milestones.map((milestone) => {
    const observedAtMilliseconds = Date.parse(milestone.observedAt);
    const sincePreviousMilliseconds =
      previous === null || !Number.isFinite(observedAtMilliseconds)
        ? null
        : Math.max(0, observedAtMilliseconds - previous);
    if (Number.isFinite(observedAtMilliseconds)) {
      previous = observedAtMilliseconds;
    }
    return {
      name: milestone.name,
      observedAt: milestone.observedAt,
      sincePreviousMilliseconds,
    };
  });
  const byName = new Map(
    normalized.map((milestone) => [
      milestone.name,
      Date.parse(milestone.observedAt),
    ]),
  );
  const connectionStarted = byName.get("connection-started");
  const mediaConnected = byName.get("media-connected");
  return {
    available: true,
    connectionMilliseconds:
      Number.isFinite(connectionStarted) && Number.isFinite(mediaConnected)
        ? Math.max(0, mediaConnected - connectionStarted)
        : null,
    milestones: normalized,
  };
}

export function summarizeHostCPU(samples, phaseTimeline, phaseOrder) {
  const emptyPhases = Object.fromEntries(
    phaseOrder.map((name) => [
      name,
      {
        activeMedianRatio: 0,
        samplingGapMaximumMilliseconds: 0,
        samplingGapP99Milliseconds: 0,
        samplingGapSamples: 0,
        samples: 0,
        stealMaximumRatio: 0,
        stealP95Ratio: 0,
      },
    ]),
  );
  if (
    !Array.isArray(samples) ||
    samples.length < 2 ||
    !Array.isArray(phaseTimeline)
  ) {
    return { available: false, phases: emptyPhases, source: null };
  }
  const timeline = phaseTimeline
    .map((phase) => ({
      name: phase.name,
      startedAtMilliseconds: Date.parse(phase.startedAt),
    }))
    .filter(
      (phase) =>
        typeof phase.name === "string" &&
        Number.isFinite(phase.startedAtMilliseconds),
    )
    .sort(
      (left, right) => left.startedAtMilliseconds - right.startedAtMilliseconds,
    );
  const phaseAt = (timestamp) => {
    let active = null;
    for (const phase of timeline) {
      if (phase.startedAtMilliseconds > timestamp) break;
      active = phase.name;
    }
    return phaseOrder.includes(active) ? active : null;
  };
  const byPhase = Object.fromEntries(
    phaseOrder.map((name) => [name, { active: [], gaps: [], steal: [] }]),
  );
  const counterFields = [
    "userTicks",
    "niceTicks",
    "systemTicks",
    "idleTicks",
    "ioWaitTicks",
    "irqTicks",
    "softIRQTicks",
    "stealTicks",
  ];
  let previous = null;
  for (const sample of samples) {
    const capturedAtMilliseconds = Date.parse(sample.capturedAt);
    const valid =
      Number.isFinite(capturedAtMilliseconds) &&
      counterFields.every(
        (field) => Number.isFinite(sample[field]) && sample[field] >= 0,
      );
    if (!valid) {
      previous = null;
      continue;
    }
    const phase = phaseAt(capturedAtMilliseconds);
    const previousPhase = previous
      ? phaseAt(previous.capturedAtMilliseconds)
      : null;
    if (previous && phase && phase === previousPhase) {
      if (
        Number.isFinite(sample.gapMilliseconds) &&
        sample.gapMilliseconds > 0
      ) {
        byPhase[phase].gaps.push(sample.gapMilliseconds);
      }
      const deltas = Object.fromEntries(
        counterFields.map((field) => [field, sample[field] - previous[field]]),
      );
      if (Object.values(deltas).every((value) => value >= 0)) {
        const total = Object.values(deltas).reduce(
          (sum, value) => sum + value,
          0,
        );
        if (total > 0) {
          const stealRatio = deltas.stealTicks / total;
          const activeRatio =
            (total -
              deltas.idleTicks -
              deltas.ioWaitTicks -
              deltas.stealTicks) /
            total;
          byPhase[phase].active.push(Math.max(0, Math.min(1, activeRatio)));
          byPhase[phase].steal.push(Math.max(0, Math.min(1, stealRatio)));
        }
      }
    }
    previous = { ...sample, capturedAtMilliseconds };
  }
  const phases = Object.fromEntries(
    phaseOrder.map((name) => {
      const active = byPhase[name].active.sort((left, right) => left - right);
      const gaps = byPhase[name].gaps.sort((left, right) => left - right);
      const steal = byPhase[name].steal.sort((left, right) => left - right);
      return [
        name,
        {
          activeMedianRatio: median(active),
          samplingGapMaximumMilliseconds: gaps.at(-1) || 0,
          samplingGapP99Milliseconds: percentile(gaps, 0.99),
          samplingGapSamples: gaps.length,
          samples: steal.length,
          stealMaximumRatio: steal.at(-1) || 0,
          stealP95Ratio: percentile(steal, 0.95),
        },
      ];
    }),
  );
  return {
    available: Object.values(phases).some((phase) => phase.samples > 0),
    phases,
    source: "linux-proc-stat",
  };
}

function summarizePhase(samples, phase, encoderQuality) {
  if (samples.length === 0) {
    return null;
  }
  const settled = samples.slice(Math.min(8, Math.floor(samples.length / 3)));
  const medianReceivedBitrateKbps = median(
    settled.map((sample) => sample.receivedBitrateKbps).filter(positive),
  );
  const capacityKbps = phase.shaping?.capacityKbps || 0;
  const first = samples[0];
  const last = samples.at(-1);
  const durationSeconds = Math.max(
    0,
    (last.elapsedMilliseconds - first.elapsedMilliseconds) / 1000,
  );
  const decodedFrames = Math.max(0, last.framesDecoded - first.framesDecoded);
  const qpSumIncrease = nullableCounterIncrease(samples, "qpSum");
  const totalDecodeTimeIncrease = nullableCounterIncrease(
    samples,
    "totalDecodeTimeSeconds",
  );
  const jitterBufferDelayIncrease = nullableCounterIncrease(
    samples,
    "jitterBufferDelaySeconds",
  );
  const jitterBufferEmittedIncrease = nullableCounterIncrease(
    samples,
    "jitterBufferEmittedCount",
  );
  const jitterBufferTargetDelayIncrease = nullableCounterIncrease(
    samples,
    "jitterBufferTargetDelaySeconds",
  );
  const decodedIntervals = samples
    .slice(1)
    .filter(
      (sample, index) => sample.framesDecoded > samples[index].framesDecoded,
    ).length;
  const freezeDurationSeconds = Math.max(
    0,
    (last.totalFreezesDurationSeconds || 0) -
      (first.totalFreezesDurationSeconds || 0),
  );
  const nackIncrease = counterIncrease(samples, "nackCount", [phase.name]);
  const packetsReceivedIncrease = counterIncrease(samples, "packetsReceived", [
    phase.name,
  ]);
  const twccReportedLostIncrease = counterIncrease(
    samples,
    "twccReportedLost",
    [phase.name],
  );
  const twccReportedStatusesIncrease = counterIncrease(
    samples,
    "twccReportedStatuses",
    [phase.name],
  );
  return {
    averageDecodeMilliseconds:
      decodedFrames > 0 && totalDecodeTimeIncrease !== null
        ? (totalDecodeTimeIncrease * 1000) / decodedFrames
        : null,
    averageJitterBufferDelayMilliseconds:
      jitterBufferEmittedIncrease > 0 && jitterBufferDelayIncrease !== null
        ? (jitterBufferDelayIncrease * 1000) / jitterBufferEmittedIncrease
        : null,
    averageJitterBufferTargetMilliseconds:
      jitterBufferEmittedIncrease > 0 &&
      jitterBufferTargetDelayIncrease !== null
        ? (jitterBufferTargetDelayIncrease * 1000) / jitterBufferEmittedIncrease
        : null,
    averageQP:
      encoderQuality?.averageQP ??
      (decodedFrames > 0 && qpSumIncrease !== null
        ? qpSumIncrease / decodedFrames
        : null),
    encoderBurstFrameRatio: encoderQuality?.burstFrameRatio || 0,
    encoderFrameGapP99Milliseconds:
      encoderQuality?.frameGapP99Milliseconds || 0,
    encoderFrameIntervals: encoderQuality?.frameIntervals || 0,
    encoderLateFrameRatio: encoderQuality?.lateFrameRatio || 0,
    encoderMaximumFrameGapMilliseconds:
      encoderQuality?.maximumFrameGapMilliseconds || 0,
    capacityKbps,
    capacityUtilization:
      capacityKbps > 0 ? medianReceivedBitrateKbps / capacityKbps : 0,
    connectedRatio:
      samples.filter(
        (sample) =>
          sample.peerConnectionState === "connected" &&
          sample.playback === "Playing",
      ).length / samples.length,
    decodedFramesPerSecond:
      durationSeconds > 0 ? decodedFrames / durationSeconds : 0,
    decoderActiveRatio:
      samples.length > 1 ? decodedIntervals / (samples.length - 1) : 0,
    freezeDurationSeconds,
    freezeRatio:
      durationSeconds > 0 ? freezeDurationSeconds / durationSeconds : 0,
    fecPacketsIncrease: counterIncrease(samples, "fecPacketsReceived", [
      phase.name,
    ]),
    maximumRTTMilliseconds:
      1000 *
      Math.max(
        0,
        ...samples.map((sample) => sample.currentRoundTripTimeSeconds || 0),
      ),
    medianEncoderTargetKbps: median(
      settled.map((sample) => sample.encoderTargetKbps).filter(positive),
    ),
    endingEncoderTargetKbps: last.encoderTargetKbps,
    maximumEncoderTargetKbps: Math.max(
      0,
      ...samples.map((sample) => sample.encoderTargetKbps || 0),
    ),
    medianFrameHeight: median(
      settled.map((sample) => sample.frameHeight).filter(positive),
    ),
    medianFrameWidth: median(
      settled.map((sample) => sample.frameWidth).filter(positive),
    ),
    medianAverageLoss: median(
      settled
        .map((sample) => sample.lossAverage)
        .filter((value) => Number.isFinite(value) && value >= 0),
    ),
    medianLossGuardTargetKbps: median(
      settled.map((sample) => sample.lossGuardTargetKbps).filter(positive),
    ),
    maximumLossGuardObservedLoss: Math.max(
      0,
      ...samples.map((sample) => sample.lossGuardLastObservedLoss || 0),
    ),
    lossGuardReductionsIncrease: counterIncrease(
      samples,
      "lossGuardReductions",
      [phase.name],
    ),
    lossGuardRecoveriesIncrease: counterIncrease(
      samples,
      "lossGuardRecoveries",
      [phase.name],
    ),
    medianDelayTargetKbps: median(
      settled.map((sample) => sample.delayTargetKbps).filter(positive),
    ),
    medianLossTargetKbps: median(
      settled.map((sample) => sample.lossTargetKbps).filter(positive),
    ),
    medianReceivedBitrateKbps,
    medianTWCCKbps: median(
      settled.map((sample) => sample.twccTargetKbps).filter(positive),
    ),
    startingEncoderTargetKbps: first.encoderTargetKbps || 0,
    nackIncrease,
    nackToPacketRatio:
      packetsReceivedIncrease > 0 ? nackIncrease / packetsReceivedIncrease : 0,
    packetsLostIncrease: counterIncrease(samples, "packetsLost", [phase.name]),
    packetsReceivedIncrease,
    maximumPacerQueuePackets: Math.max(
      0,
      ...samples.map((sample) => sample.pacerQueuePackets || 0),
    ),
    maximumPacerQueueDelayMilliseconds: Math.max(
      0,
      ...samples.map(
        (sample) => sample.pacerMaximumQueueDelayMilliseconds || 0,
      ),
    ),
    maximumPacerPrimaryDelayMilliseconds: Math.max(
      0,
      ...samples.map(
        (sample) => sample.pacerMaximumPrimaryDelayMilliseconds || 0,
      ),
    ),
    maximumPacerRepairDelayMilliseconds: Math.max(
      0,
      ...samples.map(
        (sample) => sample.pacerMaximumRepairDelayMilliseconds || 0,
      ),
    ),
    maximumPacerRTXDelayMilliseconds: Math.max(
      0,
      ...samples.map((sample) => sample.pacerMaximumRTXDelayMilliseconds || 0),
    ),
    maximumPacerFECDelayMilliseconds: Math.max(
      0,
      ...samples.map((sample) => sample.pacerMaximumFECDelayMilliseconds || 0),
    ),
    maximumPacerSustainedDelayMilliseconds: Math.max(
      0,
      ...samples.map(
        (sample) => sample.pacerMaximumSustainedDelayMilliseconds || 0,
      ),
    ),
    maximumPacerAdmittedDelayMilliseconds: Math.max(
      0,
      ...samples.map(
        (sample) => sample.pacerMaximumAdmittedDelayMilliseconds || 0,
      ),
    ),
    maximumPacerKeyFrameReserveBytes: Math.max(
      0,
      ...samples.map((sample) => sample.pacerKeyFrameReserveBytes || 0),
    ),
    pacerMediaFrameDropsIncrease: counterIncrease(
      samples,
      "pacerMediaFramesDropped",
      [phase.name],
    ),
    pacerMediaByteDropsIncrease: counterIncrease(
      samples,
      "pacerMediaBytesDropped",
      [phase.name],
    ),
    pacerQueueDropsIncrease: counterIncrease(samples, "pacerQueueDrops", [
      phase.name,
    ]),
    pacerRepairPacketsExpiredIncrease: counterIncrease(
      samples,
      "pacerRepairPacketsExpired",
      [phase.name],
    ),
    pacerRepairPacketsTrimmedIncrease: counterIncrease(
      samples,
      "pacerRepairPacketsTrimmed",
      [phase.name],
    ),
    pacerRTXPacketsExpiredIncrease: counterIncrease(
      samples,
      "pacerRTXPacketsExpired",
      [phase.name],
    ),
    pacerFECPacketsExpiredIncrease: counterIncrease(
      samples,
      "pacerFECPacketsExpired",
      [phase.name],
    ),
    pacerRTXPacketsTrimmedIncrease: counterIncrease(
      samples,
      "pacerRTXPacketsTrimmed",
      [phase.name],
    ),
    pacerFECPacketsTrimmedIncrease: counterIncrease(
      samples,
      "pacerFECPacketsTrimmed",
      [phase.name],
    ),
    pacerSentPrimaryIncrease: counterIncrease(samples, "pacerSentPrimary", [
      phase.name,
    ]),
    pacerSentRepairIncrease: counterIncrease(samples, "pacerSentRepair", [
      phase.name,
    ]),
    pacerSentRTXIncrease: counterIncrease(samples, "pacerSentRTX", [
      phase.name,
    ]),
    pacerSentFECIncrease: counterIncrease(samples, "pacerSentFEC", [
      phase.name,
    ]),
    recoveryKeyFrameRequestsIncrease: counterIncrease(
      samples,
      "recoveryKeyFrameRequests",
      [phase.name],
    ),
    recoveryKeyFrameCoalescedIncrease: counterIncrease(
      samples,
      "recoveryKeyFrameCoalesced",
      [phase.name],
    ),
    recoveryKeyFrameFailuresIncrease: counterIncrease(
      samples,
      "recoveryKeyFrameFailures",
      [phase.name],
    ),
    rtcpKeyFrameRequestsIncrease: counterIncrease(
      samples,
      "rtcpKeyFrameRequests",
      [phase.name],
    ),
    rtcpMalformedFeedbackIncrease: counterIncrease(
      samples,
      "rtcpMalformedFeedback",
      [phase.name],
    ),
    adaptiveBitrateUpdatesIncrease: counterIncrease(
      samples,
      "adaptiveBitrateUpdates",
      [phase.name],
    ),
    adaptiveBitrateFailuresIncrease: counterIncrease(
      samples,
      "adaptiveBitrateFailures",
      [phase.name],
    ),
    twccFeedbackPacketsIncrease: counterIncrease(
      samples,
      "twccFeedbackPackets",
      [phase.name],
    ),
    twccMalformedFeedbackIncrease: counterIncrease(
      samples,
      "twccMalformedFeedback",
      [phase.name],
    ),
    twccPaddingStatusesIncrease: counterIncrease(
      samples,
      "twccPaddingStatuses",
      [phase.name],
    ),
    twccReportedLostIncrease,
    twccReportedStatusesIncrease,
    twccReportedLossRatio:
      twccReportedStatusesIncrease > 0
        ? twccReportedLostIncrease / twccReportedStatusesIncrease
        : 0,
    retransmittedPacketsIncrease: counterIncrease(
      samples,
      "retransmittedPacketsReceived",
      [phase.name],
    ),
    encodedBytes: encoderQuality?.encodedBytes || 0,
    encodedFrames:
      encoderQuality?.frames || (qpSumIncrease !== null ? decodedFrames : 0),
    samples: samples.length,
  };
}

function summarizeTrafficControl(evidence) {
  if (evidence?.constrained?.end && evidence?.impaired?.end) {
    const transitions = (evidence.constrainedTransitions || []).map(
      (interval) => netemIntervalStats(interval.start, interval.end),
    );
    const constrained = netemIntervalStats(
      evidence.constrained.start,
      evidence.constrained.end,
    );
    const impaired = netemIntervalStats(
      evidence.impaired.start,
      evidence.impaired.end,
    );
    const recoveryDrain = evidence.recoveryDrain?.end
      ? netemIntervalStats(
          evidence.recoveryDrain.start,
          evidence.recoveryDrain.end,
        )
      : null;
    const constrainedTransitionPackets = transitions.reduce(
      (total, interval) => total + interval.packets,
      0,
    );
    const constrainedTransitionDrops = transitions.reduce(
      (total, interval) => total + interval.drops,
      0,
    );
    return {
      constrainedConfiguredLossRatio: constrained.configuredLossRatio,
      constrainedEndQueuePackets: constrained.endQueuePackets,
      constrainedEndQueueUtilization: constrained.endQueueUtilization,
      constrainedQueueLimitPackets: constrained.queueLimitPackets,
      constrainedDropRatio: dropRatio(
        constrained.packets + constrainedTransitionPackets,
        constrained.drops + constrainedTransitionDrops,
      ),
      constrainedDrops: constrained.drops + constrainedTransitionDrops,
      constrainedPackets: constrained.packets + constrainedTransitionPackets,
      constrainedSteadyDropRatio: dropRatio(
        constrained.packets,
        constrained.drops,
      ),
      constrainedSteadyDrops: constrained.drops,
      constrainedSteadyPackets: constrained.packets,
      constrainedTransitionDropRatio: dropRatio(
        constrainedTransitionPackets,
        constrainedTransitionDrops,
      ),
      constrainedTransitionDrops,
      constrainedTransitionPackets,
      impairedConfiguredLossRatio: impaired.configuredLossRatio,
      impairedEndQueuePackets: impaired.endQueuePackets,
      impairedEndQueueUtilization: impaired.endQueueUtilization,
      impairedQueueLimitPackets: impaired.queueLimitPackets,
      impairedDropRatio: dropRatio(impaired.packets, impaired.drops),
      impairedDrops: impaired.drops,
      impairedPackets: impaired.packets,
      recoveryDrainConfiguredLossRatio: recoveryDrain?.configuredLossRatio || 0,
      recoveryDrainDropRatio: recoveryDrain
        ? dropRatio(recoveryDrain.packets, recoveryDrain.drops)
        : 0,
      recoveryDrainDrops: recoveryDrain?.drops || 0,
      recoveryDrainEndQueuePackets: recoveryDrain?.endQueuePackets || 0,
      recoveryDrainEndQueueUtilization: recoveryDrain?.endQueueUtilization || 0,
      recoveryDrainEvidenceAvailable: recoveryDrain !== null,
      recoveryDrainPackets: recoveryDrain?.packets || 0,
      recoveryDrainQueueLimitPackets: recoveryDrain?.queueLimitPackets || 0,
    };
  }
  const constrainedTransition = netemStats(evidence?.constrainedTransition);
  const constrained = netemStats(evidence?.constrained);
  const impaired = netemStats(evidence?.impaired);
  const constrainedSteadyPackets = Math.max(
    0,
    constrained.packets - constrainedTransition.packets,
  );
  const constrainedSteadyDrops = Math.max(
    0,
    constrained.drops - constrainedTransition.drops,
  );
  const impairedPackets = Math.max(0, impaired.packets - constrained.packets);
  const impairedDrops = Math.max(0, impaired.drops - constrained.drops);
  return {
    constrainedPackets: constrained.packets,
    constrainedDrops: constrained.drops,
    constrainedConfiguredLossRatio: constrained.configuredLossRatio,
    constrainedEndQueuePackets: constrained.queueLengthPackets,
    constrainedEndQueueUtilization: constrained.queueUtilization,
    constrainedQueueLimitPackets: constrained.queueLimitPackets,
    constrainedDropRatio: dropRatio(constrained.packets, constrained.drops),
    constrainedSteadyPackets,
    constrainedSteadyDrops,
    constrainedSteadyDropRatio: dropRatio(
      constrainedSteadyPackets,
      constrainedSteadyDrops,
    ),
    constrainedTransitionPackets: constrainedTransition.packets,
    constrainedTransitionDrops: constrainedTransition.drops,
    constrainedTransitionDropRatio: dropRatio(
      constrainedTransition.packets,
      constrainedTransition.drops,
    ),
    impairedPackets,
    impairedDrops,
    impairedConfiguredLossRatio: impaired.configuredLossRatio,
    impairedEndQueuePackets: impaired.queueLengthPackets,
    impairedEndQueueUtilization: impaired.queueUtilization,
    impairedQueueLimitPackets: impaired.queueLimitPackets,
    impairedDropRatio: dropRatio(impairedPackets, impairedDrops),
    recoveryDrainConfiguredLossRatio: 0,
    recoveryDrainDropRatio: 0,
    recoveryDrainDrops: 0,
    recoveryDrainEndQueuePackets: 0,
    recoveryDrainEndQueueUtilization: 0,
    recoveryDrainEvidenceAvailable: false,
    recoveryDrainPackets: 0,
    recoveryDrainQueueLimitPackets: 0,
  };
}

function netemIntervalStats(startQdiscs, endQdiscs) {
  const start = netemStats(startQdiscs);
  const end = netemStats(endQdiscs);
  return {
    configuredLossRatio: end.configuredLossRatio,
    drops: counterDelta(start.drops, end.drops),
    endQueuePackets: end.queueLengthPackets,
    endQueueUtilization: end.queueUtilization,
    packets: counterDelta(start.packets, end.packets),
    queueLimitPackets: end.queueLimitPackets,
  };
}

function counterDelta(start, end) {
  return Math.max(0, end >= start ? end - start : end);
}

function dropRatio(packets, drops) {
  const attempted = packets + drops;
  return attempted > 0 ? drops / attempted : 0;
}

function mediaCapacityKbps(wireCapacityKbps, protection) {
  if (!protection?.flexFEC) {
    return wireCapacityKbps;
  }
  const mediaPackets = protection.flexFECMediaPackets;
  const repairPackets = protection.flexFECRepairPackets;
  if (
    !Number.isFinite(mediaPackets) ||
    !Number.isFinite(repairPackets) ||
    mediaPackets <= 0 ||
    repairPackets <= 0
  ) {
    return 0;
  }
  return (wireCapacityKbps * mediaPackets) / (mediaPackets + repairPackets);
}

function protectedPacingEnvelopeKbps(protectedWireTargetKbps) {
  if (
    !Number.isFinite(protectedWireTargetKbps) ||
    protectedWireTargetKbps <= 0
  ) {
    return 0;
  }
  return protectedWireTargetKbps * 1.5;
}

function netemStats(qdiscs) {
  const netem = Array.isArray(qdiscs)
    ? qdiscs.find((qdisc) => qdisc?.kind === "netem")
    : null;
  return {
    configuredLossRatio: Number.isFinite(netem?.options?.["loss-random"]?.loss)
      ? netem.options["loss-random"].loss
      : 0,
    drops: Number.isFinite(netem?.drops) ? netem.drops : 0,
    packets: Number.isFinite(netem?.packets) ? netem.packets : 0,
    queueLengthPackets: Number.isFinite(netem?.qlen) ? netem.qlen : 0,
    queueLimitPackets: Number.isFinite(netem?.options?.limit)
      ? netem.options.limit
      : 0,
    queueUtilization:
      Number.isFinite(netem?.qlen) &&
      Number.isFinite(netem?.options?.limit) &&
      netem.options.limit > 0
        ? netem.qlen / netem.options.limit
        : 0,
  };
}

function timeToEncoderTarget(
  samples,
  phase,
  target,
  comparison,
  offsetMilliseconds = 0,
) {
  const phaseSamples = samples.filter((sample) => sample.phase === phase);
  if (phaseSamples.length === 0 || !Number.isFinite(target) || target <= 0) {
    return null;
  }
  const event = phaseSamples[0].elapsedMilliseconds + offsetMilliseconds;
  const match = phaseSamples.find(
    (sample) =>
      sample.elapsedMilliseconds >= event &&
      (comparison === "at-most"
        ? sample.encoderTargetKbps > 0 && sample.encoderTargetKbps <= target
        : sample.encoderTargetKbps >= target),
  );
  return match ? match.elapsedMilliseconds - event : null;
}

function targetDoesNotIncreaseAfterObservedLoss(
  samples,
  phase,
  maximumIncreaseLoss,
) {
  if (
    !Number.isFinite(maximumIncreaseLoss) ||
    maximumIncreaseLoss < 0 ||
    maximumIncreaseLoss > 1
  ) {
    return false;
  }
  const phaseSamples = samples.filter((sample) => sample.phase === phase);
  const observedAt = phaseSamples.findIndex(
    (sample) =>
      Number.isFinite(sample.lossAverage) &&
      sample.lossAverage > maximumIncreaseLoss,
  );
  if (observedAt < 0) {
    return false;
  }
  const targetAtObservation = phaseSamples[observedAt].encoderTargetKbps;
  if (!Number.isFinite(targetAtObservation) || targetAtObservation <= 0) {
    return false;
  }
  return phaseSamples
    .slice(observedAt)
    .every(
      (sample) =>
        Number.isFinite(sample.encoderTargetKbps) &&
        sample.encoderTargetKbps <= targetAtObservation * 1.05,
    );
}

function longestEncoderTargetDuration(
  samples,
  phase,
  target,
  offsetMilliseconds = 0,
) {
  const phaseSamples = samples.filter((sample) => sample.phase === phase);
  if (phaseSamples.length === 0 || !Number.isFinite(target) || target <= 0) {
    return 0;
  }
  const event = phaseSamples[0].elapsedMilliseconds + offsetMilliseconds;
  let longest = 0;
  let start = null;
  let previous = null;
  for (const sample of phaseSamples) {
    if (sample.elapsedMilliseconds < event) {
      continue;
    }
    const gap = previous === null ? 0 : sample.elapsedMilliseconds - previous;
    if (
      sample.encoderTargetKbps < target ||
      gap > maximumQualificationSampleGapMilliseconds
    ) {
      start = null;
    }
    if (sample.encoderTargetKbps >= target) {
      if (start === null) {
        start = sample.elapsedMilliseconds;
      }
      longest = Math.max(longest, sample.elapsedMilliseconds - start);
    }
    previous = sample.elapsedMilliseconds;
  }
  return longest;
}

function counterIncrease(samples, field, phases) {
  const selected = samples.filter((sample) => phases.includes(sample.phase));
  if (selected.length < 2) {
    return 0;
  }
  const first = Number.isFinite(selected[0][field]) ? selected[0][field] : 0;
  const last = Number.isFinite(selected.at(-1)[field])
    ? selected.at(-1)[field]
    : 0;
  return Math.max(0, last - first);
}

function nullableCounterIncrease(samples, field) {
  if (samples.length < 2) {
    return null;
  }
  const first = samples[0][field];
  const last = samples.at(-1)[field];
  if (!Number.isFinite(first) || !Number.isFinite(last) || last < first) {
    return null;
  }
  return last - first;
}

function assert(assertions, passed, name, description) {
  assertions.push({ description, name, passed: Boolean(passed) });
}

function median(values) {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0
    ? (sorted[middle - 1] + sorted[middle]) / 2
    : sorted[middle];
}

function positive(value) {
  return Number.isFinite(value) && value > 0;
}

function csvValue(value) {
  if (value === null || value === undefined) {
    return "";
  }
  const stringValue = String(value);
  return /[",\n]/.test(stringValue)
    ? `"${stringValue.replaceAll('"', '""')}"`
    : stringValue;
}

function formatDuration(milliseconds) {
  return milliseconds === null
    ? "not observed"
    : `${formatNumber(milliseconds / 1000, 1)} s`;
}

function formatNumber(value, fractionDigits) {
  return Number.isFinite(value) ? value.toFixed(fractionDigits) : "n/a";
}

function durationMilliseconds(value) {
  if (Number.isFinite(value)) return value;
  const match =
    typeof value === "string" ? value.match(/^([0-9]+(?:\.[0-9]+)?)ms$/) : null;
  return match ? Number(match[1]) : null;
}

function parsePercent(value) {
  if (Number.isFinite(value)) return value * 100;
  const match =
    typeof value === "string" ? value.match(/^([0-9]+(?:\.[0-9]+)?)%$/) : null;
  return match ? Number(match[1]) : null;
}

function numberOrNull(value) {
  return Number.isFinite(value) ? value : null;
}

function round(value) {
  return Math.round(value * 10) / 10;
}

function escapeXML(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

export function parseEncoderQuality(
  rawLog,
  phaseTimeline,
  producerStartedAt = null,
  framesPerSecond = 30,
) {
  const phases = phaseTimeline
    .map((phase) => ({
      name: phase.name,
      startedAtMilliseconds: Date.parse(phase.startedAt),
    }))
    .filter((phase) => Number.isFinite(phase.startedAtMilliseconds))
    .sort(
      (left, right) => left.startedAtMilliseconds - right.startedAtMilliseconds,
    );
  const collected = new Map();
  for (const line of rawLog.split("\n")) {
    const match = line.match(
      /^(?:(\S+)\s+)?(\d+):(\d+):(\d+(?:\.\d+)?)\s+.*\bframe=\s*\d+\s+QP=([0-9]+(?:\.[0-9]+)?)\b.*\bsize=\s*([0-9]+)\s+bytes\b/,
    );
    if (!match) {
      continue;
    }
    const monotonicMilliseconds =
      ((Number(match[2]) * 60 + Number(match[3])) * 60 + Number(match[4])) *
      1000;
    const wallTimestamp = Date.parse(match[1]);
    const processTimestamp = Date.parse(producerStartedAt);
    const timestamp = Number.isFinite(wallTimestamp)
      ? wallTimestamp
      : processTimestamp + monotonicMilliseconds;
    const qp = Number(match[5]);
    const bytes = Number(match[6]);
    if (!Number.isFinite(timestamp) || !Number.isFinite(qp)) {
      continue;
    }
    const phase = [...phases]
      .reverse()
      .find((candidate) => candidate.startedAtMilliseconds <= timestamp);
    if (!phase || phase.name === "connecting" || phase.name === "complete") {
      continue;
    }
    const current = collected.get(phase.name) || {
      encodedBytes: 0,
      frameIntervals: [],
      frames: 0,
      lastMonotonicMilliseconds: null,
      qpSum: 0,
    };
    if (current.lastMonotonicMilliseconds !== null) {
      current.frameIntervals.push(
        monotonicMilliseconds - current.lastMonotonicMilliseconds,
      );
    }
    current.encodedBytes += Number.isFinite(bytes) ? bytes : 0;
    current.frames += 1;
    current.lastMonotonicMilliseconds = monotonicMilliseconds;
    current.qpSum += qp;
    collected.set(phase.name, current);
  }
  return Object.fromEntries(
    [...collected].map(([name, values]) => {
      const intervals = values.frameIntervals
        .filter((value) => Number.isFinite(value) && value >= 0)
        .sort((left, right) => left - right);
      const expectedIntervalMilliseconds = 1000 / framesPerSecond;
      return [
        name,
        {
          averageQP: values.frames > 0 ? values.qpSum / values.frames : null,
          burstFrameRatio: ratioBelow(
            intervals,
            expectedIntervalMilliseconds * 0.5,
          ),
          encodedBytes: values.encodedBytes,
          frameGapP99Milliseconds: percentile(intervals, 0.99),
          frameIntervals: intervals.length,
          frames: values.frames,
          lateFrameRatio: ratioAbove(
            intervals,
            expectedIntervalMilliseconds * 1.5,
          ),
          maximumFrameGapMilliseconds: intervals.at(-1) || 0,
        },
      ];
    }),
  );
}

function percentile(sortedValues, fraction) {
  if (sortedValues.length === 0) return 0;
  const index = Math.min(
    sortedValues.length - 1,
    Math.floor(sortedValues.length * fraction),
  );
  return sortedValues[index];
}

function ratioAbove(values, threshold) {
  if (values.length === 0) return 0;
  return values.filter((value) => value > threshold).length / values.length;
}

function ratioBelow(values, threshold) {
  if (values.length === 0) return 0;
  return values.filter((value) => value < threshold).length / values.length;
}

async function main() {
  const outputDirectory = process.argv[2];
  if (!outputDirectory) {
    throw new Error("usage: node lib/analysis.mjs <output-directory>");
  }
  const [
    rawSamples,
    rawManifest,
    constrainedStepOneStartQdisc,
    constrainedStepOneQdisc,
    constrainedStepTwoStartQdisc,
    constrainedStepTwoQdisc,
    constrainedStepThreeStartQdisc,
    constrainedStepThreeQdisc,
    constrainedStartQdisc,
    constrainedQdisc,
    impairedStartQdisc,
    impairedQdisc,
    recoveryDrainStartQdisc,
    recoveryDrainQdisc,
    rawProducerLog,
    rawEncoderLog,
    rawPhaseTimeline,
    rawReceiverUDP,
    rawProducerUDP,
    rawSetupTimeline,
    rawHostCPU,
    rawReceiverHostCPU,
    rawNetworkConditionTimeline,
  ] = await Promise.all([
    readFile(`${outputDirectory}/samples.jsonl`, "utf8"),
    readFile(`${outputDirectory}/manifest.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-1-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-1.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-2-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-2.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-3-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-step-3.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-steady-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-constrained-steady.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-impaired-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-impaired.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-recovery-drain-start.json`, "utf8"),
    readFile(`${outputDirectory}/qdisc-recovery-drain.json`, "utf8"),
    readFile(`${outputDirectory}/producer.log`, "utf8"),
    readOptional(`${outputDirectory}/encoder.log`),
    readFile(`${outputDirectory}/phase-timeline.jsonl`, "utf8"),
    readOptional(`${outputDirectory}/receiver-udp.jsonl`),
    readOptional(`${outputDirectory}/producer-udp.jsonl`),
    readOptional(`${outputDirectory}/setup-timeline.jsonl`),
    readOptional(`${outputDirectory}/producer-host-cpu.jsonl`),
    readOptional(`${outputDirectory}/receiver-host-cpu.jsonl`),
    readOptional(`${outputDirectory}/network-condition-timeline.jsonl`),
  ]);
  const samples = rawSamples
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const manifest = JSON.parse(rawManifest);
  const phaseTimeline = rawPhaseTimeline
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const setupTimeline = rawSetupTimeline
    ? rawSetupTimeline
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line))
    : null;
  const encoderQuality = parseEncoderQuality(
    rawEncoderLog || rawProducerLog,
    phaseTimeline,
    setupTimeline?.find(
      (milestone) => milestone.name === "producer-container-started",
    )?.observedAt,
    manifest.video?.framesPerSecond || 30,
  );
  const analysis = analyze(
    samples,
    manifest,
    {
      constrainedTransitions: [
        {
          start: JSON.parse(constrainedStepOneStartQdisc),
          end: JSON.parse(constrainedStepOneQdisc),
        },
        {
          start: JSON.parse(constrainedStepTwoStartQdisc),
          end: JSON.parse(constrainedStepTwoQdisc),
        },
        {
          start: JSON.parse(constrainedStepThreeStartQdisc),
          end: JSON.parse(constrainedStepThreeQdisc),
        },
      ],
      constrained: {
        start: JSON.parse(constrainedStartQdisc),
        end: JSON.parse(constrainedQdisc),
      },
      impaired: {
        start: JSON.parse(impairedStartQdisc),
        end: JSON.parse(impairedQdisc),
      },
      recoveryDrain: {
        start: JSON.parse(recoveryDrainStartQdisc),
        end: JSON.parse(recoveryDrainQdisc),
      },
    },
    encoderQuality,
    rawReceiverUDP
      ? rawReceiverUDP
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line))
      : null,
    rawProducerUDP
      ? rawProducerUDP
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line))
      : null,
    setupTimeline,
    rawHostCPU
      ? rawHostCPU
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line))
      : null,
    phaseTimeline,
    rawReceiverHostCPU
      ? rawReceiverHostCPU
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line))
      : null,
    rawNetworkConditionTimeline
      ? rawNetworkConditionTimeline
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line))
      : null,
  );
  await writeArtifacts(outputDirectory, analysis, manifest);
  process.stdout.write(
    `${analysis.passed ? "PASS" : "FAIL"}: ${outputDirectory}/summary.md\n`,
  );
  if (!analysis.passed) {
    process.exitCode = 1;
  }
}

async function readOptional(path) {
  try {
    return await readFile(path, "utf8");
  } catch (error) {
    if (error?.code === "ENOENT") return null;
    throw error;
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  await main();
}
