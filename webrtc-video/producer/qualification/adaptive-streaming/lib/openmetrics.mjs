const maximumResponseBytes = 2 * 1024 * 1024;

export async function collectProducerOpenMetrics(endpoint, fetcher = fetch) {
  const abort = AbortSignal.timeout(2_000);
  const response = await fetcher(endpoint, {
    cache: "no-store",
    redirect: "error",
    signal: abort,
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error(`producer metrics returned ${response.status}`);
  }
  const body = await response.text();
  if (Buffer.byteLength(body) > maximumResponseBytes) {
    throw new Error("producer metrics response is too large");
  }
  return producerSample(parseOpenMetrics(body));
}

export function parseOpenMetrics(body) {
  const samples = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }
    const match =
      /^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([^\s]+)(?:\s+\d+)?$/.exec(
        trimmed,
      );
    if (!match) {
      throw new Error("producer metrics contains an invalid sample");
    }
    const value = Number(match[3]);
    if (!Number.isFinite(value)) {
      throw new Error(`producer metric ${match[1]} is not finite`);
    }
    samples.push({
      labels: parseLabels(match[2] || ""),
      name: match[1],
      value,
    });
  }
  return samples;
}

export function producerSample(samples) {
  const value = (name) => metricValue(samples, name);
  const labeled = (name, ...labelMatchers) =>
    metricValue(samples, name, labelMatchers);
  const scale = (metric, factor) => (metric === null ? null : metric * factor);
  const lossGuardSessions = value(
    "rstream_video_producer_loss_guard_active_sessions",
  );
  return {
    adaptiveBitrateFailures: labeled(
      "rstream_video_producer_adaptive_bitrate_updates_total",
      "outcome",
      "failed",
    ),
    adaptiveBitrateUpdates: labeled(
      "rstream_video_producer_adaptive_bitrate_updates_total",
      "outcome",
      "applied",
    ),
    delayTargetKbps: scale(
      labeled(
        "rstream_video_producer_twcc_controller_target_bytes_per_second",
        "controller",
        "delay",
      ),
      8 / 1_000,
    ),
    delayControllerDecreaseSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_state_sessions",
      "state",
      "decrease",
    ),
    delayControllerHoldSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_state_sessions",
      "state",
      "hold",
    ),
    delayControllerIncreaseSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_state_sessions",
      "state",
      "increase",
    ),
    delayControllerNormalSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_usage_sessions",
      "usage",
      "normal",
    ),
    delayControllerOveruseSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_usage_sessions",
      "usage",
      "overuse",
    ),
    delayControllerUnderuseSessions: labeled(
      "rstream_video_producer_twcc_delay_controller_usage_sessions",
      "usage",
      "underuse",
    ),
    encoderTargetKbps: scale(
      value("rstream_video_producer_encoder_target_bytes_per_second"),
      8 / 1_000,
    ),
    encodedKeyFrames: labeled(
      "rstream_video_producer_encoded_frames_total",
      "frame_type",
      "key",
    ),
    lossAverage: value("rstream_video_producer_twcc_maximum_packet_loss_ratio"),
    lossGuardActive: lossGuardSessions === null ? null : lossGuardSessions > 0,
    lossGuardLastObservedLoss: value(
      "rstream_video_producer_loss_guard_maximum_observed_loss_ratio",
    ),
    lossGuardRecoveries: labeled(
      "rstream_video_producer_loss_guard_transitions_total",
      "transition",
      "recover",
    ),
    lossGuardReductions: labeled(
      "rstream_video_producer_loss_guard_transitions_total",
      "transition",
      "reduce",
    ),
    lossGuardTargetKbps: scale(
      value("rstream_video_producer_loss_guard_target_bytes_per_second"),
      8 / 1_000,
    ),
    lossTargetKbps: scale(
      labeled(
        "rstream_video_producer_twcc_controller_target_bytes_per_second",
        "controller",
        "loss",
      ),
      8 / 1_000,
    ),
    pacerMediaBytesDropped: value(
      "rstream_video_producer_pacer_media_dropped_bytes_total",
    ),
    pacerMediaFramesDropped: value(
      "rstream_video_producer_pacer_media_dropped_frames_total",
    ),
    pacerPacingBitrateKbps: scale(
      value("rstream_video_producer_pacer_pacing_bytes_per_second"),
      8 / 1_000,
    ),
    pacerQueueDelayMilliseconds: scale(
      value("rstream_video_producer_pacer_maximum_queue_delay_seconds"),
      1_000,
    ),
    pacerQueueDrops: value(
      "rstream_video_producer_pacer_queue_dropped_packets_total",
    ),
    pacerQueuePackets: value("rstream_video_producer_pacer_queue_packets"),
    pacerRetransmissionRTTMilliseconds: scale(
      value(
        "rstream_video_producer_pacer_maximum_retransmission_round_trip_time_seconds",
      ),
      1_000,
    ),
    pacerRetransmissionIntervalMilliseconds: scale(
      value(
        "rstream_video_producer_pacer_maximum_retransmission_interval_seconds",
      ),
      1_000,
    ),
    pacerRetransmissionPacketsCoalesced: labeled(
      "rstream_video_producer_pacer_repair_discarded_packets_total",
      "reason",
      "coalesced",
      "repair",
      "retransmission",
    ),
    pacerRetransmissionPacketsSuppressed: labeled(
      "rstream_video_producer_pacer_repair_discarded_packets_total",
      "reason",
      "suppressed",
      "repair",
      "retransmission",
    ),
    pacerSentFEC: labeled(
      "rstream_video_producer_pacer_sent_packets_total",
      "kind",
      "fec",
    ),
    pacerSentPrimary: labeled(
      "rstream_video_producer_pacer_sent_packets_total",
      "kind",
      "primary",
    ),
    pacerSentRetransmission: labeled(
      "rstream_video_producer_pacer_sent_packets_total",
      "kind",
      "retransmission",
    ),
    pacerTargetBitrateKbps: scale(
      value("rstream_video_producer_pacer_target_bytes_per_second"),
      8 / 1_000,
    ),
    producerMetricsSource: "openmetrics",
    recoveryKeyFrameCoalesced: value(
      "rstream_video_producer_key_frame_requests_coalesced_total",
    ),
    recoveryKeyFrameFailures: value(
      "rstream_video_producer_key_frame_request_failures_total",
    ),
    recoveryKeyFrameRequests: labeled(
      "rstream_video_producer_key_frame_requests_total",
      "source",
      "recovery",
    ),
    rtcpKeyFrameRequests: labeled(
      "rstream_video_producer_key_frame_requests_total",
      "source",
      "rtcp",
    ),
    staleBitrateCallbacks: value(
      "rstream_video_producer_stale_bitrate_callbacks_total",
    ),
    twccFeedbackPackets: value(
      "rstream_video_producer_twcc_feedback_packets_total",
    ),
    twccMalformedFeedback: labeled(
      "rstream_video_producer_malformed_feedback_total",
      "protocol",
      "twcc",
    ),
    twccReportedLost: value(
      "rstream_video_producer_twcc_reported_lost_packets_total",
    ),
    twccReportedStatuses: value(
      "rstream_video_producer_twcc_reported_packet_statuses_total",
    ),
    twccTargetKbps: scale(
      value("rstream_video_producer_twcc_estimated_available_bytes_per_second"),
      8 / 1_000,
    ),
  };
}

function metricValue(samples, name, labelMatchers = []) {
  if (labelMatchers.length % 2 !== 0) {
    throw new Error("producer metric label matchers must be pairs");
  }
  const matches = samples.filter(
    (sample) =>
      sample.name === name &&
      labelMatchers.every(
        (matcher, index) =>
          index % 2 !== 0 ||
          sample.labels[matcher] === labelMatchers[index + 1],
      ),
  );
  if (matches.length === 0) {
    return null;
  }
  return matches.reduce((total, sample) => total + sample.value, 0);
}

function parseLabels(raw) {
  if (!raw) {
    return {};
  }
  const labels = {};
  const expression = /([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"])*)"(?:,|$)/gy;
  let offset = 0;
  while (offset < raw.length) {
    expression.lastIndex = offset;
    const match = expression.exec(raw);
    if (!match) {
      throw new Error("producer metric contains invalid labels");
    }
    labels[match[1]] = match[2].replace(/\\([\\"n])/g, (_, value) =>
      value === "n" ? "\n" : value,
    );
    offset = expression.lastIndex;
  }
  return labels;
}
