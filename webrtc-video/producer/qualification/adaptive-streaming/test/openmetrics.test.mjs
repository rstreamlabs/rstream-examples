import assert from "node:assert/strict";
import test from "node:test";

import { parseOpenMetrics, producerSample } from "../lib/openmetrics.mjs";

test("producer OpenMetrics map bounded transport signals", () => {
  const sample = producerSample(
    parseOpenMetrics(`# HELP ignored help
rstream_video_producer_encoder_target_bytes_per_second 1000000
rstream_video_producer_encoded_frames_total{frame_type="key"} 9
rstream_video_producer_twcc_estimated_available_bytes_per_second 750000
rstream_video_producer_twcc_controller_target_bytes_per_second{controller="loss"} 700000
rstream_video_producer_twcc_controller_target_bytes_per_second{controller="delay"} 650000
rstream_video_producer_twcc_delay_controller_state_sessions{state="increase"} 0
rstream_video_producer_twcc_delay_controller_state_sessions{state="decrease"} 1
rstream_video_producer_twcc_delay_controller_state_sessions{state="hold"} 0
rstream_video_producer_twcc_delay_controller_usage_sessions{usage="normal"} 0
rstream_video_producer_twcc_delay_controller_usage_sessions{usage="overuse"} 1
rstream_video_producer_twcc_delay_controller_usage_sessions{usage="underuse"} 0
rstream_video_producer_loss_guard_active_sessions 1
rstream_video_producer_loss_guard_maximum_observed_loss_ratio 0.2
rstream_video_producer_loss_guard_target_bytes_per_second 500000
rstream_video_producer_loss_guard_transitions_total{transition="reduce"} 3
rstream_video_producer_loss_guard_transitions_total{transition="recover"} 2
rstream_video_producer_pacer_target_bytes_per_second 1200000
rstream_video_producer_pacer_maximum_retransmission_round_trip_time_seconds 0.06
rstream_video_producer_pacer_maximum_retransmission_interval_seconds 0.065
rstream_video_producer_pacer_repair_discarded_packets_total{reason="coalesced",repair="retransmission"} 11
rstream_video_producer_pacer_repair_discarded_packets_total{reason="suppressed",repair="retransmission"} 13
rstream_video_producer_pacer_sent_packets_total{kind="primary"} 100
rstream_video_producer_pacer_sent_packets_total{kind="retransmission"} 7
rstream_video_producer_adaptive_bitrate_updates_total{outcome="applied"} 4
rstream_video_producer_adaptive_bitrate_updates_total{outcome="failed"} 1
rstream_video_producer_key_frame_requests_total{source="recovery"} 5
rstream_video_producer_key_frame_requests_total{source="rtcp"} 6
rstream_video_producer_key_frame_requests_coalesced_total 7
rstream_video_producer_key_frame_request_failures_total 0
rstream_video_producer_malformed_feedback_total{protocol="twcc"} 2
`),
  );
  assert.equal(sample.encoderTargetKbps, 8000);
  assert.equal(sample.encodedKeyFrames, 9);
  assert.equal(sample.twccTargetKbps, 6000);
  assert.equal(sample.lossTargetKbps, 5600);
  assert.equal(sample.delayTargetKbps, 5200);
  assert.equal(sample.delayControllerDecreaseSessions, 1);
  assert.equal(sample.delayControllerOveruseSessions, 1);
  assert.equal(sample.lossGuardActive, true);
  assert.equal(sample.lossGuardLastObservedLoss, 0.2);
  assert.equal(sample.lossGuardTargetKbps, 4000);
  assert.equal(sample.lossGuardReductions, 3);
  assert.equal(sample.lossGuardRecoveries, 2);
  assert.equal(sample.pacerTargetBitrateKbps, 9600);
  assert.equal(sample.pacerRetransmissionRTTMilliseconds, 60);
  assert.equal(sample.pacerRetransmissionIntervalMilliseconds, 65);
  assert.equal(sample.pacerRetransmissionPacketsCoalesced, 11);
  assert.equal(sample.pacerRetransmissionPacketsSuppressed, 13);
  assert.equal(sample.pacerSentPrimary, 100);
  assert.equal(sample.pacerSentRetransmission, 7);
  assert.equal(sample.adaptiveBitrateUpdates, 4);
  assert.equal(sample.adaptiveBitrateFailures, 1);
  assert.equal(sample.recoveryKeyFrameRequests, 5);
  assert.equal(sample.rtcpKeyFrameRequests, 6);
  assert.equal(sample.recoveryKeyFrameCoalesced, 7);
  assert.equal(sample.recoveryKeyFrameFailures, 0);
  assert.equal(sample.twccMalformedFeedback, 2);
});

test("producer OpenMetrics reject ambiguous syntax and non-finite values", () => {
  assert.throws(
    () => parseOpenMetrics("invalid sample value extra\n"),
    /invalid sample/,
  );
  assert.throws(() => parseOpenMetrics("metric NaN\n"), /not finite/);
  assert.throws(
    () => parseOpenMetrics('metric{broken="label" trailing} 1\n'),
    /invalid labels/,
  );
});

test("producer OpenMetrics preserve absent and explicit zero values", () => {
  const absent = producerSample(parseOpenMetrics("unrelated_metric 1\n"));
  assert.equal(absent.lossTargetKbps, null);
  assert.equal(absent.lossGuardActive, null);
  assert.equal(absent.lossGuardLastObservedLoss, null);
  assert.equal(absent.adaptiveBitrateUpdates, null);
  const zero = producerSample(
    parseOpenMetrics(`
rstream_video_producer_twcc_controller_target_bytes_per_second{controller="loss"} 0
rstream_video_producer_loss_guard_active_sessions 0
rstream_video_producer_loss_guard_maximum_observed_loss_ratio 0
rstream_video_producer_adaptive_bitrate_updates_total{outcome="applied"} 0
`),
  );
  assert.equal(zero.lossTargetKbps, 0);
  assert.equal(zero.lossGuardActive, false);
  assert.equal(zero.lossGuardLastObservedLoss, 0);
  assert.equal(zero.adaptiveBitrateUpdates, 0);
});
