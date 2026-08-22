package webrtc

import "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"

type ProducerStats struct {
	ActiveSessions                            int
	OpeningSessions                           int
	TWCCNegotiatedSessions                    int
	NACKNegotiatedSessions                    int
	RTXNegotiatedSessions                     int
	FlexFECNegotiatedSessions                 int
	EstimatedBitrateBps                       int64
	LossControllerTargetBitrateBps            int64
	DelayControllerTargetBitrateBps           int64
	DelayControllerIncreaseSessions           int
	DelayControllerDecreaseSessions           int
	DelayControllerHoldSessions               int
	DelayControllerNormalSessions             int
	DelayControllerOveruseSessions            int
	DelayControllerUnderuseSessions           int
	EncoderTargetBitrateBps                   int64
	PacerTargetBitrateBps                     int64
	PacerPacingBitrateBps                     int64
	PacerQueuePackets                         int64
	MaximumPacketLossRatio                    float64
	MaximumLossGuardObservedLossRatio         float64
	MaximumDelayEstimateSeconds               float64
	MaximumPacerQueueDelaySeconds             float64
	MaximumRetransmissionRTTSeconds           float64
	MaximumRetransmissionRetryIntervalSeconds float64
	LossGuardActiveSessions                   int
	LossGuardTargetBitrateBps                 int64
	AdaptiveBitrateUpdates                    uint64
	AdaptiveBitrateFailures                   uint64
	RecoveryKeyFrameRequests                  uint64
	RecoveryKeyFrameCoalesced                 uint64
	RecoveryKeyFrameFailures                  uint64
	RTCPKeyFrameRequests                      uint64
	RTCPMalformedFeedback                     uint64
	LossGuardReductions                       uint64
	LossGuardRecoveries                       uint64
	PacerQueueDrops                           uint64
	PacerMediaFrameDrops                      uint64
	PacerMediaByteDrops                       uint64
	PacerRepairPacketsExpired                 uint64
	PacerRepairPacketsTrimmed                 uint64
	PacerRetransmissionPacketsExpired         uint64
	PacerRetransmissionPacketsCoalesced       uint64
	PacerRetransmissionPacketsSuppressed      uint64
	PacerFECPacketsExpired                    uint64
	PacerRetransmissionPacketsTrimmed         uint64
	PacerFECPacketsTrimmed                    uint64
	PacerSentPrimary                          uint64
	PacerSentPrimaryBytes                     uint64
	PacerSentRepair                           uint64
	PacerSentRetransmission                   uint64
	PacerSentRetransmissionBytes              uint64
	PacerSentFEC                              uint64
	PacerSentFECBytes                         uint64
	StaleBitrateCallbacks                     uint64
	TWCCFeedbackPackets                       uint64
	TWCCMalformedFeedback                     uint64
	TWCCPaddingStatuses                       uint64
	TWCCReportedLost                          uint64
	TWCCReportedStatuses                      uint64
}

type producerTotals struct {
	AdaptiveBitrateUpdates    uint64
	AdaptiveBitrateFailures   uint64
	RecoveryKeyFrameRequests  uint64
	RecoveryKeyFrameCoalesced uint64
	RecoveryKeyFrameFailures  uint64
	RTCPKeyFrameRequests      uint64
	RTCPMalformedFeedback     uint64
	Bandwidth                 BandwidthStats
}

func (b *Broadcaster) MetricsSnapshot() ProducerStats {
	b.mu.Lock()
	sessions := make([]*Session, 0, len(b.sessions))
	for _, session := range b.sessions {
		sessions = append(sessions, session)
	}
	stats := ProducerStats{
		ActiveSessions:  len(sessions),
		OpeningSessions: b.opening,
	}
	sharedMedia := b.mediaMode == config.MediaModeShared
	addProducerTotals(&stats, b.retired)
	b.mu.Unlock()
	for _, session := range sessions {
		addActiveSessionStats(&stats, session.StatsSnapshot(), sharedMedia)
	}
	return stats
}

func (b *Broadcaster) retireSession(session *Session) int {
	stats := producerTotalsFromSession(session.StatsSnapshot())
	b.mu.Lock()
	if _, ok := b.sessions[session.id]; ok {
		b.retired.add(stats)
		delete(b.sessions, session.id)
	}
	count := len(b.sessions)
	b.mu.Unlock()
	return count
}

func producerTotalsFromSession(stats SessionStats) producerTotals {
	totals := producerTotals{
		AdaptiveBitrateUpdates:    stats.AdaptiveBitrateUpdates,
		AdaptiveBitrateFailures:   stats.AdaptiveBitrateFailures,
		RecoveryKeyFrameRequests:  stats.RecoveryKeyFrameRequests,
		RecoveryKeyFrameCoalesced: stats.RecoveryKeyFrameCoalesced,
		RecoveryKeyFrameFailures:  stats.RecoveryKeyFrameFailures,
		RTCPKeyFrameRequests:      stats.RTCPKeyFrameRequests,
		RTCPMalformedFeedback:     stats.RTCPMalformedFeedback,
	}
	if stats.Bandwidth != nil {
		totals.Bandwidth = *stats.Bandwidth
	}
	return totals
}

func (t *producerTotals) add(other producerTotals) {
	t.AdaptiveBitrateUpdates += other.AdaptiveBitrateUpdates
	t.AdaptiveBitrateFailures += other.AdaptiveBitrateFailures
	t.RecoveryKeyFrameRequests += other.RecoveryKeyFrameRequests
	t.RecoveryKeyFrameCoalesced += other.RecoveryKeyFrameCoalesced
	t.RecoveryKeyFrameFailures += other.RecoveryKeyFrameFailures
	t.RTCPKeyFrameRequests += other.RTCPKeyFrameRequests
	t.RTCPMalformedFeedback += other.RTCPMalformedFeedback
	addBandwidthCounters(&t.Bandwidth, other.Bandwidth)
}

func addActiveSessionStats(producer *ProducerStats, session SessionStats, sharedMedia bool) {
	addProducerTotals(producer, producerTotalsFromSession(session))
	if session.TWCCNegotiated {
		producer.TWCCNegotiatedSessions++
	}
	if session.NACKNegotiated {
		producer.NACKNegotiatedSessions++
	}
	if session.RTXNegotiated {
		producer.RTXNegotiatedSessions++
	}
	if session.FlexFECNegotiated {
		producer.FlexFECNegotiatedSessions++
	}
	producer.EstimatedBitrateBps += int64(session.EstimatedBitrateBps)
	encoderTarget := int64(session.EncoderTargetBitrateKbps) * 1000
	if sharedMedia {
		producer.EncoderTargetBitrateBps = max(producer.EncoderTargetBitrateBps, encoderTarget)
	} else {
		producer.EncoderTargetBitrateBps += encoderTarget
	}
	bandwidth := session.Bandwidth
	if bandwidth == nil {
		return
	}
	producer.PacerTargetBitrateBps += int64(bandwidth.PacerTargetBitrateBps)
	producer.PacerPacingBitrateBps += int64(bandwidth.PacerPacingBitrateBps)
	producer.PacerQueuePackets += int64(bandwidth.PacerQueuePackets)
	producer.MaximumPacketLossRatio = max(producer.MaximumPacketLossRatio, bandwidth.AverageLoss)
	producer.MaximumLossGuardObservedLossRatio = max(producer.MaximumLossGuardObservedLossRatio, bandwidth.LossGuardLastObservedLoss)
	producer.MaximumDelayEstimateSeconds = max(producer.MaximumDelayEstimateSeconds, bandwidth.DelayEstimateMs/1000)
	producer.MaximumPacerQueueDelaySeconds = max(producer.MaximumPacerQueueDelaySeconds, bandwidth.PacerQueueDelayMs/1000)
	producer.MaximumRetransmissionRTTSeconds = max(producer.MaximumRetransmissionRTTSeconds, bandwidth.PacerRetransmissionRoundTripTimeMs/1000)
	producer.MaximumRetransmissionRetryIntervalSeconds = max(producer.MaximumRetransmissionRetryIntervalSeconds, bandwidth.PacerRetransmissionRetryIntervalMs/1000)
	producer.LossControllerTargetBitrateBps += int64(bandwidth.LossTargetBitrateBps)
	producer.DelayControllerTargetBitrateBps += int64(bandwidth.DelayTargetBitrateBps)
	switch bandwidth.State {
	case "increase":
		producer.DelayControllerIncreaseSessions++
	case "decrease":
		producer.DelayControllerDecreaseSessions++
	case "hold":
		producer.DelayControllerHoldSessions++
	}
	switch bandwidth.Usage {
	case "normal":
		producer.DelayControllerNormalSessions++
	case "overuse":
		producer.DelayControllerOveruseSessions++
	case "underuse":
		producer.DelayControllerUnderuseSessions++
	}
	if bandwidth.LossGuardActive {
		producer.LossGuardActiveSessions++
		producer.LossGuardTargetBitrateBps += int64(bandwidth.LossGuardTargetBitrateBps)
	}
}

func addProducerTotals(producer *ProducerStats, totals producerTotals) {
	producer.AdaptiveBitrateUpdates += totals.AdaptiveBitrateUpdates
	producer.AdaptiveBitrateFailures += totals.AdaptiveBitrateFailures
	producer.RecoveryKeyFrameRequests += totals.RecoveryKeyFrameRequests
	producer.RecoveryKeyFrameCoalesced += totals.RecoveryKeyFrameCoalesced
	producer.RecoveryKeyFrameFailures += totals.RecoveryKeyFrameFailures
	producer.RTCPKeyFrameRequests += totals.RTCPKeyFrameRequests
	producer.RTCPMalformedFeedback += totals.RTCPMalformedFeedback
	bandwidth := totals.Bandwidth
	producer.LossGuardReductions += bandwidth.LossGuardReductions
	producer.LossGuardRecoveries += bandwidth.LossGuardRecoveries
	producer.PacerQueueDrops += bandwidth.PacerQueueDrops
	producer.PacerMediaFrameDrops += bandwidth.PacerMediaFrameDrops
	producer.PacerMediaByteDrops += bandwidth.PacerMediaByteDrops
	producer.PacerRepairPacketsExpired += bandwidth.PacerRepairPacketsExpired
	producer.PacerRepairPacketsTrimmed += bandwidth.PacerRepairPacketsTrimmed
	producer.PacerRetransmissionPacketsExpired += bandwidth.PacerRetransmissionPacketsExpired
	producer.PacerRetransmissionPacketsCoalesced += bandwidth.PacerRetransmissionPacketsCoalesced
	producer.PacerRetransmissionPacketsSuppressed += bandwidth.PacerRetransmissionPacketsSuppressed
	producer.PacerFECPacketsExpired += bandwidth.PacerFECPacketsExpired
	producer.PacerRetransmissionPacketsTrimmed += bandwidth.PacerRetransmissionPacketsTrimmed
	producer.PacerFECPacketsTrimmed += bandwidth.PacerFECPacketsTrimmed
	producer.PacerSentPrimary += bandwidth.PacerSentPrimary
	producer.PacerSentPrimaryBytes += bandwidth.PacerSentPrimaryBytes
	producer.PacerSentRepair += bandwidth.PacerSentRepair
	producer.PacerSentRetransmission += bandwidth.PacerSentRetransmission
	producer.PacerSentRetransmissionBytes += bandwidth.PacerSentRetransmissionBytes
	producer.PacerSentFEC += bandwidth.PacerSentFEC
	producer.PacerSentFECBytes += bandwidth.PacerSentFECBytes
	producer.StaleBitrateCallbacks += bandwidth.StaleBitrateCallbacks
	producer.TWCCFeedbackPackets += bandwidth.TWCCFeedbackPackets
	producer.TWCCMalformedFeedback += bandwidth.TWCCMalformedFeedback
	producer.TWCCPaddingStatuses += bandwidth.TWCCPaddingStatuses
	producer.TWCCReportedLost += bandwidth.TWCCReportedLost
	producer.TWCCReportedStatuses += bandwidth.TWCCReportedStatuses
}

func addBandwidthCounters(target *BandwidthStats, source BandwidthStats) {
	target.LossGuardReductions += source.LossGuardReductions
	target.LossGuardRecoveries += source.LossGuardRecoveries
	target.PacerQueueDrops += source.PacerQueueDrops
	target.PacerMediaFrameDrops += source.PacerMediaFrameDrops
	target.PacerMediaByteDrops += source.PacerMediaByteDrops
	target.PacerRepairPacketsExpired += source.PacerRepairPacketsExpired
	target.PacerRepairPacketsTrimmed += source.PacerRepairPacketsTrimmed
	target.PacerRetransmissionPacketsExpired += source.PacerRetransmissionPacketsExpired
	target.PacerRetransmissionPacketsCoalesced += source.PacerRetransmissionPacketsCoalesced
	target.PacerRetransmissionPacketsSuppressed += source.PacerRetransmissionPacketsSuppressed
	target.PacerFECPacketsExpired += source.PacerFECPacketsExpired
	target.PacerRetransmissionPacketsTrimmed += source.PacerRetransmissionPacketsTrimmed
	target.PacerFECPacketsTrimmed += source.PacerFECPacketsTrimmed
	target.PacerSentPrimary += source.PacerSentPrimary
	target.PacerSentPrimaryBytes += source.PacerSentPrimaryBytes
	target.PacerSentRepair += source.PacerSentRepair
	target.PacerSentRetransmission += source.PacerSentRetransmission
	target.PacerSentRetransmissionBytes += source.PacerSentRetransmissionBytes
	target.PacerSentFEC += source.PacerSentFEC
	target.PacerSentFECBytes += source.PacerSentFECBytes
	target.StaleBitrateCallbacks += source.StaleBitrateCallbacks
	target.TWCCFeedbackPackets += source.TWCCFeedbackPackets
	target.TWCCMalformedFeedback += source.TWCCMalformedFeedback
	target.TWCCPaddingStatuses += source.TWCCPaddingStatuses
	target.TWCCReportedLost += source.TWCCReportedLost
	target.TWCCReportedStatuses += source.TWCCReportedStatuses
}
