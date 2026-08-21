package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
)

const namespace = "rstream_video_producer"

type sourceProvider interface {
	StatsSnapshot() media.SourceStats
}

type producerProvider interface {
	MetricsSnapshot() rtc.ProducerStats
}

type whepProvider interface {
	WHEPInitialRequests() map[string]uint64
}

type Collector struct {
	cfg                           config.Config
	source                        sourceProvider
	producer                      producerProvider
	whep                          whepProvider
	descriptors                   []*prometheus.Desc
	info                          *prometheus.Desc
	featureEnabled                *prometheus.Desc
	sourceInstances               *prometheus.Desc
	encodedBytes                  *prometheus.Desc
	encodedFrames                 *prometheus.Desc
	encodedMediaSeconds           *prometheus.Desc
	lastEncodedFrameTimestamp     *prometheus.Desc
	sourceDeliveryDroppedBytes    *prometheus.Desc
	sourceDeliveryDroppedFrames   *prometheus.Desc
	sourceErrors                  *prometheus.Desc
	sessions                      *prometheus.Desc
	whepInitialRequests           *prometheus.Desc
	transportNegotiatedSessions   *prometheus.Desc
	estimatedAvailableBytesSecond *prometheus.Desc
	controllerTargetBytesSecond   *prometheus.Desc
	delayControllerStateSessions  *prometheus.Desc
	delayControllerUsageSessions  *prometheus.Desc
	encoderTargetBytesSecond      *prometheus.Desc
	pacerTargetBytesSecond        *prometheus.Desc
	pacerPacingBytesSecond        *prometheus.Desc
	pacerQueuePackets             *prometheus.Desc
	packetLossRatio               *prometheus.Desc
	delayEstimateSeconds          *prometheus.Desc
	pacerQueueDelaySeconds        *prometheus.Desc
	lossGuardActiveSessions       *prometheus.Desc
	lossGuardTargetBytesSecond    *prometheus.Desc
	adaptiveUpdates               *prometheus.Desc
	keyFrameRequests              *prometheus.Desc
	keyFrameRequestsCoalesced     *prometheus.Desc
	keyFrameRequestFailures       *prometheus.Desc
	malformedFeedback             *prometheus.Desc
	lossGuardTransitions          *prometheus.Desc
	pacerQueueDrops               *prometheus.Desc
	pacerMediaFrameDrops          *prometheus.Desc
	pacerMediaByteDrops           *prometheus.Desc
	pacerRepairPacketsDiscarded   *prometheus.Desc
	pacerSentPackets              *prometheus.Desc
	pacerSentBytes                *prometheus.Desc
	staleBitrateCallbacks         *prometheus.Desc
	twccFeedbackPackets           *prometheus.Desc
	twccReportedStatuses          *prometheus.Desc
	twccReportedLost              *prometheus.Desc
	twccPaddingStatuses           *prometheus.Desc
}

func NewCollector(cfg config.Config, source sourceProvider, producer producerProvider, whep whepProvider) *Collector {
	c := &Collector{
		cfg:      cfg,
		source:   source,
		producer: producer,
		whep:     whep,
		info: newDesc(
			namespace+"_info",
			"Static information about this video producer.",
			[]string{"codec", "media_mode", "adaptive_backend"},
			nil,
		),
		featureEnabled: newDesc(
			namespace+"_feature_enabled",
			"Whether an optional transport or adaptation feature is enabled in the producer configuration.",
			[]string{"feature"},
			nil,
		),
		sourceInstances: newDesc(
			namespace+"_source_instances",
			"Current number of allocated media source instances.",
			nil,
			nil,
		),
		encodedBytes: newDesc(
			namespace+"_encoded_bytes_total",
			"Total bytes produced by the video encoder before packetization and transport repair.",
			nil,
			nil,
			"bytes",
		),
		encodedFrames: newDesc(
			namespace+"_encoded_frames_total",
			"Total encoded video frames by frame type.",
			[]string{"frame_type"},
			nil,
		),
		encodedMediaSeconds: newDesc(
			namespace+"_encoded_media_seconds_total",
			"Total media duration represented by encoded video frames.",
			nil,
			nil,
			"seconds",
		),
		lastEncodedFrameTimestamp: newDesc(
			namespace+"_last_encoded_frame_timestamp_seconds",
			"Unix timestamp of the most recently encoded video frame.",
			nil,
			nil,
			"seconds",
		),
		sourceDeliveryDroppedBytes: newDesc(
			namespace+"_source_delivery_dropped_bytes_total",
			"Total encoded bytes not delivered to a slow source subscriber because its bounded queue was full.",
			nil,
			nil,
			"bytes",
		),
		sourceDeliveryDroppedFrames: newDesc(
			namespace+"_source_delivery_dropped_frames_total",
			"Total encoded frames not delivered to a slow source subscriber because its bounded queue was full.",
			nil,
			nil,
		),
		sourceErrors: newDesc(
			namespace+"_source_errors_total",
			"Total media source errors by processing stage.",
			[]string{"stage"},
			nil,
		),
		sessions: newDesc(
			namespace+"_sessions",
			"Current WebRTC sessions by lifecycle state.",
			[]string{"state"},
			nil,
		),
		whepInitialRequests: newDesc(
			namespace+"_whep_initial_requests_total",
			"Total initial WHEP session requests by bounded outcome.",
			[]string{"outcome"},
			nil,
		),
		transportNegotiatedSessions: newDesc(
			namespace+"_transport_negotiated_sessions",
			"Current WebRTC sessions that negotiated each configured transport feature.",
			[]string{"feature"},
			nil,
		),
		estimatedAvailableBytesSecond: newDesc(
			namespace+"_twcc_estimated_available_bytes_per_second",
			"Sum of the current media throughput available to encoders after reserving configured repair capacity across active sessions.",
			nil,
			nil,
			"bytes_per_second",
		),
		controllerTargetBytesSecond: newDesc(
			namespace+"_twcc_controller_target_bytes_per_second",
			"Sum of current TWCC controller bitrate targets across active sessions, before the lower loss or delay target is selected.",
			[]string{"controller"},
			nil,
			"bytes_per_second",
		),
		delayControllerStateSessions: newDesc(
			namespace+"_twcc_delay_controller_state_sessions",
			"Current sessions in each delay controller rate-control state.",
			[]string{"state"},
			nil,
		),
		delayControllerUsageSessions: newDesc(
			namespace+"_twcc_delay_controller_usage_sessions",
			"Current sessions by delay controller link-usage classification.",
			[]string{"usage"},
			nil,
		),
		encoderTargetBytesSecond: newDesc(
			namespace+"_encoder_target_bytes_per_second",
			"Aggregate current encoder bitrate target, counting a shared media source once.",
			nil,
			nil,
			"bytes_per_second",
		),
		pacerTargetBytesSecond: newDesc(
			namespace+"_pacer_target_bytes_per_second",
			"Sum of the sustained wire capacity budgets presented to packet pacers across active sessions, including configured proactive repair.",
			nil,
			nil,
			"bytes_per_second",
		),
		pacerPacingBytesSecond: newDesc(
			namespace+"_pacer_pacing_bytes_per_second",
			"Sum of the current short-burst wire pacing allowances across active sessions.",
			nil,
			nil,
			"bytes_per_second",
		),
		pacerQueuePackets: newDesc(
			namespace+"_pacer_queue_packets",
			"Current packets queued across active session pacers.",
			nil,
			nil,
		),
		packetLossRatio: newDesc(
			namespace+"_twcc_maximum_packet_loss_ratio",
			"Highest current TWCC packet loss ratio among active sessions.",
			nil,
			nil,
		),
		delayEstimateSeconds: newDesc(
			namespace+"_twcc_maximum_delay_estimate_seconds",
			"Highest current TWCC delay estimate among active sessions.",
			nil,
			nil,
			"seconds",
		),
		pacerQueueDelaySeconds: newDesc(
			namespace+"_pacer_maximum_queue_delay_seconds",
			"Highest current pacer queue delay among active sessions.",
			nil,
			nil,
			"seconds",
		),
		lossGuardActiveSessions: newDesc(
			namespace+"_loss_guard_active_sessions",
			"Current sessions whose loss guard is constraining bitrate recovery.",
			nil,
			nil,
		),
		lossGuardTargetBytesSecond: newDesc(
			namespace+"_loss_guard_target_bytes_per_second",
			"Sum of current loss guard bitrate ceilings across sessions actively constrained by the guard.",
			nil,
			nil,
			"bytes_per_second",
		),
		adaptiveUpdates: newDesc(
			namespace+"_adaptive_bitrate_updates_total",
			"Total adaptive bitrate update attempts by outcome.",
			[]string{"outcome"},
			nil,
		),
		keyFrameRequests: newDesc(
			namespace+"_key_frame_requests_total",
			"Total key-frame requests by source.",
			[]string{"source"},
			nil,
		),
		keyFrameRequestsCoalesced: newDesc(
			namespace+"_key_frame_requests_coalesced_total",
			"Total recovery key-frame requests coalesced by the producer rate limiter.",
			nil,
			nil,
		),
		keyFrameRequestFailures: newDesc(
			namespace+"_key_frame_request_failures_total",
			"Total recovery key-frame requests that could not be applied to the encoder.",
			nil,
			nil,
		),
		malformedFeedback: newDesc(
			namespace+"_malformed_feedback_total",
			"Total malformed transport feedback messages by protocol.",
			[]string{"protocol"},
			nil,
		),
		lossGuardTransitions: newDesc(
			namespace+"_loss_guard_transitions_total",
			"Total loss guard state transitions by direction.",
			[]string{"transition"},
			nil,
		),
		pacerQueueDrops: newDesc(
			namespace+"_pacer_queue_dropped_packets_total",
			"Total packets rejected because a pacer queue was full.",
			nil,
			nil,
		),
		pacerMediaFrameDrops: newDesc(
			namespace+"_pacer_media_dropped_frames_total",
			"Total encoded media frames dropped before packetization by pacer admission control.",
			nil,
			nil,
		),
		pacerMediaByteDrops: newDesc(
			namespace+"_pacer_media_dropped_bytes_total",
			"Total encoded media bytes dropped before packetization by pacer admission control.",
			nil,
			nil,
			"bytes",
		),
		pacerRepairPacketsDiscarded: newDesc(
			namespace+"_pacer_repair_discarded_packets_total",
			"Total repair packets discarded before transmission by repair kind and reason.",
			[]string{"repair", "reason"},
			nil,
		),
		pacerSentPackets: newDesc(
			namespace+"_pacer_sent_packets_total",
			"Total packets sent by the producer pacers by packet kind.",
			[]string{"kind"},
			nil,
		),
		pacerSentBytes: newDesc(
			namespace+"_pacer_sent_bytes_total",
			"Total RTP bytes written by the producer pacers by packet kind.",
			[]string{"kind"},
			nil,
			"bytes",
		),
		staleBitrateCallbacks: newDesc(
			namespace+"_stale_bitrate_callbacks_total",
			"Total estimator callbacks ignored after their owning session became stale.",
			nil,
			nil,
		),
		twccFeedbackPackets: newDesc(
			namespace+"_twcc_feedback_packets_total",
			"Total valid TWCC feedback packets processed by the producer.",
			nil,
			nil,
		),
		twccReportedStatuses: newDesc(
			namespace+"_twcc_reported_packet_statuses_total",
			"Total packet statuses reported in valid TWCC feedback.",
			nil,
			nil,
		),
		twccReportedLost: newDesc(
			namespace+"_twcc_reported_lost_packets_total",
			"Total packets reported lost in valid TWCC feedback.",
			nil,
			nil,
		),
		twccPaddingStatuses: newDesc(
			namespace+"_twcc_padding_packet_statuses_total",
			"Total TWCC statuses associated with producer padding packets.",
			nil,
			nil,
		),
	}
	c.descriptors = []*prometheus.Desc{
		c.info,
		c.featureEnabled,
		c.sourceInstances,
		c.encodedBytes,
		c.encodedFrames,
		c.encodedMediaSeconds,
		c.lastEncodedFrameTimestamp,
		c.sourceDeliveryDroppedBytes,
		c.sourceDeliveryDroppedFrames,
		c.sourceErrors,
		c.sessions,
		c.whepInitialRequests,
		c.transportNegotiatedSessions,
		c.estimatedAvailableBytesSecond,
		c.controllerTargetBytesSecond,
		c.delayControllerStateSessions,
		c.delayControllerUsageSessions,
		c.encoderTargetBytesSecond,
		c.pacerTargetBytesSecond,
		c.pacerPacingBytesSecond,
		c.pacerQueuePackets,
		c.packetLossRatio,
		c.delayEstimateSeconds,
		c.pacerQueueDelaySeconds,
		c.lossGuardActiveSessions,
		c.lossGuardTargetBytesSecond,
		c.adaptiveUpdates,
		c.keyFrameRequests,
		c.keyFrameRequestsCoalesced,
		c.keyFrameRequestFailures,
		c.malformedFeedback,
		c.lossGuardTransitions,
		c.pacerQueueDrops,
		c.pacerMediaFrameDrops,
		c.pacerMediaByteDrops,
		c.pacerRepairPacketsDiscarded,
		c.pacerSentPackets,
		c.pacerSentBytes,
		c.staleBitrateCallbacks,
		c.twccFeedbackPackets,
		c.twccReportedStatuses,
		c.twccReportedLost,
		c.twccPaddingStatuses,
	}
	return c
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, descriptor := range c.descriptors {
		ch <- descriptor
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	source := c.source.StatsSnapshot()
	producer := c.producer.MetricsSnapshot()
	ch <- prometheus.MustNewConstMetric(
		c.info,
		prometheus.GaugeValue,
		1,
		string(c.cfg.VideoCodec()),
		string(c.cfg.MediaMode()),
		string(c.cfg.AdaptiveBackend()),
	)
	features := []struct {
		name    string
		enabled bool
	}{
		{name: "twcc", enabled: c.cfg.WebRTC.Interceptors.TWCC},
		{name: "nack", enabled: c.cfg.WebRTC.Interceptors.NACK},
		{name: "rtx", enabled: c.cfg.WebRTC.Interceptors.RTX},
		{name: "flexfec", enabled: c.cfg.WebRTC.Interceptors.FlexFEC},
		{name: "adaptive_bitrate", enabled: c.cfg.AdaptiveBackend() != config.AdaptiveBackendOff},
	}
	for _, feature := range features {
		ch <- prometheus.MustNewConstMetric(c.featureEnabled, prometheus.GaugeValue, boolFloat(feature.enabled), feature.name)
	}
	ch <- prometheus.MustNewConstMetric(c.sourceInstances, prometheus.GaugeValue, float64(source.Sources))
	ch <- prometheus.MustNewConstMetric(c.encodedBytes, prometheus.CounterValue, float64(source.EncodedBytes))
	deltaFrames := source.EncodedFrames - min(source.EncodedFrames, source.EncodedKeyFrames)
	ch <- prometheus.MustNewConstMetric(c.encodedFrames, prometheus.CounterValue, float64(source.EncodedKeyFrames), "key")
	ch <- prometheus.MustNewConstMetric(c.encodedFrames, prometheus.CounterValue, float64(deltaFrames), "delta")
	ch <- prometheus.MustNewConstMetric(c.encodedMediaSeconds, prometheus.CounterValue, float64(source.EncodedMediaNanoseconds)/1e9)
	ch <- prometheus.MustNewConstMetric(c.lastEncodedFrameTimestamp, prometheus.GaugeValue, float64(source.LastEncodedFrameUnixNano)/1e9)
	ch <- prometheus.MustNewConstMetric(c.sourceDeliveryDroppedBytes, prometheus.CounterValue, float64(source.DeliveryDroppedBytes))
	ch <- prometheus.MustNewConstMetric(c.sourceDeliveryDroppedFrames, prometheus.CounterValue, float64(source.DeliveryDroppedFrames))
	ch <- prometheus.MustNewConstMetric(c.sourceErrors, prometheus.CounterValue, float64(source.PipelineCreateErrors), "create")
	ch <- prometheus.MustNewConstMetric(c.sourceErrors, prometheus.CounterValue, float64(source.SampleExtractionErrors), "extract")
	ch <- prometheus.MustNewConstMetric(c.sourceErrors, prometheus.CounterValue, float64(source.PipelineErrors), "runtime")
	ch <- prometheus.MustNewConstMetric(c.sessions, prometheus.GaugeValue, float64(producer.ActiveSessions), "active")
	ch <- prometheus.MustNewConstMetric(c.sessions, prometheus.GaugeValue, float64(producer.OpeningSessions), "opening")
	whepRequests := map[string]uint64(nil)
	if c.whep != nil {
		whepRequests = c.whep.WHEPInitialRequests()
	}
	for _, outcome := range []string{"created", "invalid", "rate_limited", "capacity_limited", "negotiation_failed", "internal_failed", "response_failed"} {
		ch <- prometheus.MustNewConstMetric(c.whepInitialRequests, prometheus.CounterValue, float64(whepRequests[outcome]), outcome)
	}
	ch <- prometheus.MustNewConstMetric(c.transportNegotiatedSessions, prometheus.GaugeValue, float64(producer.TWCCNegotiatedSessions), "twcc")
	ch <- prometheus.MustNewConstMetric(c.transportNegotiatedSessions, prometheus.GaugeValue, float64(producer.NACKNegotiatedSessions), "nack")
	ch <- prometheus.MustNewConstMetric(c.transportNegotiatedSessions, prometheus.GaugeValue, float64(producer.RTXNegotiatedSessions), "rtx")
	ch <- prometheus.MustNewConstMetric(c.transportNegotiatedSessions, prometheus.GaugeValue, float64(producer.FlexFECNegotiatedSessions), "flexfec")
	ch <- prometheus.MustNewConstMetric(c.estimatedAvailableBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.EstimatedBitrateBps))
	ch <- prometheus.MustNewConstMetric(c.controllerTargetBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.LossControllerTargetBitrateBps), "loss")
	ch <- prometheus.MustNewConstMetric(c.controllerTargetBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.DelayControllerTargetBitrateBps), "delay")
	ch <- prometheus.MustNewConstMetric(c.delayControllerStateSessions, prometheus.GaugeValue, float64(producer.DelayControllerIncreaseSessions), "increase")
	ch <- prometheus.MustNewConstMetric(c.delayControllerStateSessions, prometheus.GaugeValue, float64(producer.DelayControllerDecreaseSessions), "decrease")
	ch <- prometheus.MustNewConstMetric(c.delayControllerStateSessions, prometheus.GaugeValue, float64(producer.DelayControllerHoldSessions), "hold")
	ch <- prometheus.MustNewConstMetric(c.delayControllerUsageSessions, prometheus.GaugeValue, float64(producer.DelayControllerNormalSessions), "normal")
	ch <- prometheus.MustNewConstMetric(c.delayControllerUsageSessions, prometheus.GaugeValue, float64(producer.DelayControllerOveruseSessions), "overuse")
	ch <- prometheus.MustNewConstMetric(c.delayControllerUsageSessions, prometheus.GaugeValue, float64(producer.DelayControllerUnderuseSessions), "underuse")
	ch <- prometheus.MustNewConstMetric(c.encoderTargetBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.EncoderTargetBitrateBps))
	ch <- prometheus.MustNewConstMetric(c.pacerTargetBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.PacerTargetBitrateBps))
	ch <- prometheus.MustNewConstMetric(c.pacerPacingBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.PacerPacingBitrateBps))
	ch <- prometheus.MustNewConstMetric(c.pacerQueuePackets, prometheus.GaugeValue, float64(producer.PacerQueuePackets))
	ch <- prometheus.MustNewConstMetric(c.packetLossRatio, prometheus.GaugeValue, producer.MaximumPacketLossRatio)
	ch <- prometheus.MustNewConstMetric(c.delayEstimateSeconds, prometheus.GaugeValue, producer.MaximumDelayEstimateSeconds)
	ch <- prometheus.MustNewConstMetric(c.pacerQueueDelaySeconds, prometheus.GaugeValue, producer.MaximumPacerQueueDelaySeconds)
	ch <- prometheus.MustNewConstMetric(c.lossGuardActiveSessions, prometheus.GaugeValue, float64(producer.LossGuardActiveSessions))
	ch <- prometheus.MustNewConstMetric(c.lossGuardTargetBytesSecond, prometheus.GaugeValue, bitsToBytes(producer.LossGuardTargetBitrateBps))
	ch <- prometheus.MustNewConstMetric(c.adaptiveUpdates, prometheus.CounterValue, float64(producer.AdaptiveBitrateUpdates), "applied")
	ch <- prometheus.MustNewConstMetric(c.adaptiveUpdates, prometheus.CounterValue, float64(producer.AdaptiveBitrateFailures), "failed")
	ch <- prometheus.MustNewConstMetric(c.keyFrameRequests, prometheus.CounterValue, float64(producer.RecoveryKeyFrameRequests), "recovery")
	ch <- prometheus.MustNewConstMetric(c.keyFrameRequests, prometheus.CounterValue, float64(producer.RTCPKeyFrameRequests), "rtcp")
	ch <- prometheus.MustNewConstMetric(c.keyFrameRequestsCoalesced, prometheus.CounterValue, float64(producer.RecoveryKeyFrameCoalesced))
	ch <- prometheus.MustNewConstMetric(c.keyFrameRequestFailures, prometheus.CounterValue, float64(producer.RecoveryKeyFrameFailures))
	ch <- prometheus.MustNewConstMetric(c.malformedFeedback, prometheus.CounterValue, float64(producer.RTCPMalformedFeedback), "rtcp")
	ch <- prometheus.MustNewConstMetric(c.malformedFeedback, prometheus.CounterValue, float64(producer.TWCCMalformedFeedback), "twcc")
	ch <- prometheus.MustNewConstMetric(c.lossGuardTransitions, prometheus.CounterValue, float64(producer.LossGuardReductions), "reduce")
	ch <- prometheus.MustNewConstMetric(c.lossGuardTransitions, prometheus.CounterValue, float64(producer.LossGuardRecoveries), "recover")
	ch <- prometheus.MustNewConstMetric(c.pacerQueueDrops, prometheus.CounterValue, float64(producer.PacerQueueDrops))
	ch <- prometheus.MustNewConstMetric(c.pacerMediaFrameDrops, prometheus.CounterValue, float64(producer.PacerMediaFrameDrops))
	ch <- prometheus.MustNewConstMetric(c.pacerMediaByteDrops, prometheus.CounterValue, float64(producer.PacerMediaByteDrops))
	ch <- prometheus.MustNewConstMetric(c.pacerRepairPacketsDiscarded, prometheus.CounterValue, float64(producer.PacerRTXPacketsExpired), "rtx", "expired")
	ch <- prometheus.MustNewConstMetric(c.pacerRepairPacketsDiscarded, prometheus.CounterValue, float64(producer.PacerFECPacketsExpired), "fec", "expired")
	ch <- prometheus.MustNewConstMetric(c.pacerRepairPacketsDiscarded, prometheus.CounterValue, float64(producer.PacerRTXPacketsTrimmed), "rtx", "trimmed")
	ch <- prometheus.MustNewConstMetric(c.pacerRepairPacketsDiscarded, prometheus.CounterValue, float64(producer.PacerFECPacketsTrimmed), "fec", "trimmed")
	ch <- prometheus.MustNewConstMetric(c.pacerRepairPacketsDiscarded, prometheus.CounterValue, float64(producer.PacerRTXPacketsCoalesced), "rtx", "coalesced")
	ch <- prometheus.MustNewConstMetric(c.pacerSentPackets, prometheus.CounterValue, float64(producer.PacerSentPrimary), "primary")
	ch <- prometheus.MustNewConstMetric(c.pacerSentPackets, prometheus.CounterValue, float64(producer.PacerSentRTX), "rtx")
	ch <- prometheus.MustNewConstMetric(c.pacerSentPackets, prometheus.CounterValue, float64(producer.PacerSentFEC), "fec")
	ch <- prometheus.MustNewConstMetric(c.pacerSentBytes, prometheus.CounterValue, float64(producer.PacerSentPrimaryBytes), "primary")
	ch <- prometheus.MustNewConstMetric(c.pacerSentBytes, prometheus.CounterValue, float64(producer.PacerSentRTXBytes), "rtx")
	ch <- prometheus.MustNewConstMetric(c.pacerSentBytes, prometheus.CounterValue, float64(producer.PacerSentFECBytes), "fec")
	ch <- prometheus.MustNewConstMetric(c.staleBitrateCallbacks, prometheus.CounterValue, float64(producer.StaleBitrateCallbacks))
	ch <- prometheus.MustNewConstMetric(c.twccFeedbackPackets, prometheus.CounterValue, float64(producer.TWCCFeedbackPackets))
	ch <- prometheus.MustNewConstMetric(c.twccReportedStatuses, prometheus.CounterValue, float64(producer.TWCCReportedStatuses))
	ch <- prometheus.MustNewConstMetric(c.twccReportedLost, prometheus.CounterValue, float64(producer.TWCCReportedLost))
	ch <- prometheus.MustNewConstMetric(c.twccPaddingStatuses, prometheus.CounterValue, float64(producer.TWCCPaddingStatuses))
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func bitsToBytes(value int64) float64 {
	return float64(value) / 8
}

func newDesc(
	name string,
	help string,
	variableLabels []string,
	constLabels prometheus.Labels,
	unit ...string,
) *prometheus.Desc {
	options := make([]prometheus.DescOpt, 0, 1)
	if len(unit) > 0 && unit[0] != "" {
		options = append(options, prometheus.WithUnit(unit[0]))
	}
	return prometheus.V2.NewDesc(
		name,
		help,
		prometheus.UnconstrainedLabels(variableLabels),
		constLabels,
		options...,
	)
}
