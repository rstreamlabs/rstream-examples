package webrtc

import (
	"sync"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestProducerMetricsRemainMonotonicAfterSessionRetirement(t *testing.T) {
	session := &Session{
		id: "session",
		estimator: fakeBandwidthEstimator{stats: map[string]any{
			"averageLoss":                               0.025,
			"lossTargetBitrate":                         2_000_000,
			"delayTargetBitrate":                        3_000_000,
			"state":                                     "decrease",
			"usage":                                     "overuse",
			"delayEstimate":                             18.0,
			"pacerTargetBitrateBps":                     4_000_000,
			"pacerPacingBitrateBps":                     6_000_000,
			"pacerQueuePackets":                         3,
			"pacerQueueDelayMilliseconds":               4.5,
			"lossGuardActive":                           true,
			"lossGuardTargetBitrate":                    1_500_000,
			"lossGuardReductions":                       uint64(7),
			"lossGuardRecoveries":                       uint64(2),
			"pacerQueueDrops":                           uint64(3),
			"pacerMediaFramesDropped":                   uint64(4),
			"pacerMediaBytesDropped":                    uint64(5),
			"pacerRetransmissionPacketsExpired":         uint64(6),
			"pacerRetransmissionPacketsCoalesced":       uint64(26),
			"pacerForwardErrorCorrectionPacketsExpired": uint64(7),
			"pacerRetransmissionPacketsTrimmed":         uint64(8),
			"pacerForwardErrorCorrectionPacketsTrimmed": uint64(9),
			"pacerSentPrimary":                          uint64(10),
			"pacerSentPrimaryBytes":                     uint64(1000),
			"pacerSentRetransmission":                   uint64(11),
			"pacerSentRetransmissionBytes":              uint64(1100),
			"pacerSentForwardErrorCorrection":           uint64(12),
			"pacerSentForwardErrorCorrectionBytes":      uint64(1200),
			"staleBitrateCallbacks":                     uint64(13),
			"twccFeedbackPackets":                       uint64(14),
			"twccMalformedFeedback":                     uint64(15),
			"twccPaddingStatuses":                       uint64(16),
			"twccReportedLost":                          uint64(17),
			"twccReportedStatuses":                      uint64(18),
		}},
		stats: SessionStats{
			EstimatedBitrateBps:      3_000_000,
			EncoderTargetBitrateKbps: 2_500,
			AdaptiveBitrateUpdates:   19,
			AdaptiveBitrateFailures:  20,
			TWCCNegotiated:           true,
			NACKNegotiated:           true,
			RTXNegotiated:            true,
			FlexFECNegotiated:        false,
		},
	}
	session.recoveryKeyFrameRequests.Add(21)
	session.recoveryKeyFrameCoalesced.Add(22)
	session.recoveryKeyFrameFailures.Add(23)
	session.rtcpKeyFrameRequests.Add(24)
	session.rtcpMalformedFeedback.Add(25)
	broadcaster := &Broadcaster{
		sessions: map[string]*Session{session.id: session},
		opening:  1,
	}
	active := broadcaster.MetricsSnapshot()
	if active.ActiveSessions != 1 || active.OpeningSessions != 1 {
		t.Fatalf("unexpected active lifecycle metrics: %+v", active)
	}
	if active.TWCCNegotiatedSessions != 1 || active.NACKNegotiatedSessions != 1 ||
		active.RTXNegotiatedSessions != 1 || active.FlexFECNegotiatedSessions != 0 {
		t.Fatalf("unexpected negotiated transport metrics: %+v", active)
	}
	if active.MaximumPacketLossRatio != 0.025 {
		t.Fatalf("maximum packet loss ratio = %f, want 0.025", active.MaximumPacketLossRatio)
	}
	if active.EstimatedBitrateBps != 3_000_000 || active.EncoderTargetBitrateBps != 2_500_000 {
		t.Fatalf("unexpected bitrate metrics: %+v", active)
	}
	if active.LossControllerTargetBitrateBps != 2_000_000 ||
		active.DelayControllerTargetBitrateBps != 3_000_000 ||
		active.LossGuardTargetBitrateBps != 1_500_000 {
		t.Fatalf("unexpected controller target metrics: %+v", active)
	}
	if active.DelayControllerDecreaseSessions != 1 || active.DelayControllerOveruseSessions != 1 ||
		active.DelayControllerIncreaseSessions != 0 || active.DelayControllerHoldSessions != 0 ||
		active.DelayControllerNormalSessions != 0 || active.DelayControllerUnderuseSessions != 0 {
		t.Fatalf("unexpected delay controller state metrics: %+v", active)
	}
	if active.TWCCReportedStatuses != 18 || active.RecoveryKeyFrameRequests != 21 ||
		active.PacerRTXPacketsCoalesced != 26 {
		t.Fatalf("unexpected active counters: %+v", active)
	}
	if active.PacerSentPrimaryBytes != 1000 || active.PacerSentRTXBytes != 1100 || active.PacerSentFECBytes != 1200 {
		t.Fatalf("unexpected wire byte counters: %+v", active)
	}
	if count := broadcaster.retireSession(session); count != 0 {
		t.Fatalf("active sessions after retirement = %d, want 0", count)
	}
	retired := broadcaster.MetricsSnapshot()
	if retired.ActiveSessions != 0 {
		t.Fatalf("active sessions = %d, want 0", retired.ActiveSessions)
	}
	if retired.EstimatedBitrateBps != 0 || retired.LossControllerTargetBitrateBps != 0 ||
		retired.DelayControllerTargetBitrateBps != 0 || retired.LossGuardTargetBitrateBps != 0 ||
		retired.DelayControllerDecreaseSessions != 0 || retired.DelayControllerOveruseSessions != 0 ||
		retired.MaximumPacketLossRatio != 0 {
		t.Fatalf("retired session leaked into current gauges: %+v", retired)
	}
	if retired.TWCCReportedStatuses != active.TWCCReportedStatuses ||
		retired.RecoveryKeyFrameRequests != active.RecoveryKeyFrameRequests ||
		retired.PacerSentRTX != active.PacerSentRTX ||
		retired.PacerSentRTXBytes != active.PacerSentRTXBytes {
		t.Fatalf("lifetime counters changed at retirement: active=%+v retired=%+v", active, retired)
	}
	broadcaster.retireSession(session)
	afterSecondRetirement := broadcaster.MetricsSnapshot()
	if afterSecondRetirement.TWCCReportedStatuses != retired.TWCCReportedStatuses {
		t.Fatalf("duplicate retirement changed counters: before=%+v after=%+v", retired, afterSecondRetirement)
	}
}

func TestProducerMetricsCanRaceWithSessionRetirement(t *testing.T) {
	session := &Session{
		id: "session",
		estimator: fakeBandwidthEstimator{stats: map[string]any{
			"twccFeedbackPackets": uint64(100),
		}},
	}
	broadcaster := &Broadcaster{sessions: map[string]*Session{session.id: session}}
	const readers = 16
	const snapshotsPerReader = 500
	var readersDone sync.WaitGroup
	readersDone.Add(readers)
	for range readers {
		go func() {
			defer readersDone.Done()
			for range snapshotsPerReader {
				if got := broadcaster.MetricsSnapshot().TWCCFeedbackPackets; got != 100 {
					t.Errorf("TWCC feedback packets = %d, want 100", got)
					return
				}
			}
		}()
	}
	broadcaster.retireSession(session)
	readersDone.Wait()
}

func TestProducerMetricsDoNotMultiplyASharedEncoderTarget(t *testing.T) {
	first := &Session{stats: SessionStats{EncoderTargetBitrateKbps: 2500}}
	second := &Session{stats: SessionStats{EncoderTargetBitrateKbps: 2500}}
	broadcaster := &Broadcaster{
		mediaMode: config.MediaModeShared,
		sessions: map[string]*Session{
			"first":  first,
			"second": second,
		},
	}
	if got := broadcaster.MetricsSnapshot().EncoderTargetBitrateBps; got != 2_500_000 {
		t.Fatalf("shared encoder target = %d bps, want 2500000", got)
	}
	broadcaster.mediaMode = config.MediaModePerViewer
	if got := broadcaster.MetricsSnapshot().EncoderTargetBitrateBps; got != 5_000_000 {
		t.Fatalf("per-viewer encoder target = %d bps, want 5000000", got)
	}
}
