package webrtc

import (
	"strings"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/flexfec"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestNewPeerConnectionFactoryKeepsH264FmtpLine(t *testing.T) {
	cfg := config.Default()
	_, codec, err := newPeerConnectionFactory(cfg)
	if err != nil {
		t.Fatalf("expected H264 factory setup to succeed, got %v", err)
	}
	if codec.SDPFmtpLine == "" {
		t.Fatal("expected H264 to keep its fmtp line")
	}
}

func TestNewPeerConnectionFactoryOmitsAV1FmtpLineWhenUnset(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Video.MimeType = "video/AV1"
	cfg.WebRTC.Video.PayloadType = 45
	cfg.WebRTC.Video.RTXPayloadType = 46
	cfg.WebRTC.Video.SDPFmtpLine = nil
	cfg.Media.Pipeline = "videotestsrc is-live=true ! videoconvert ! av1enc name=encoder usage-profile=realtime end-usage=cbr cpu-used=8 lag-in-frames=0 target-bitrate=5000 keyframe-max-dist=60 ! av1parse ! video/x-av1,stream-format=obu-stream,alignment=tu ! appsink name=video emit-signals=true sync=false max-buffers=4 drop=true"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected AV1 config to validate, got %v", err)
	}
	_, codec, err := newPeerConnectionFactory(cfg)
	if err != nil {
		t.Fatalf("expected AV1 factory setup to succeed, got %v", err)
	}
	if codec.SDPFmtpLine != "" {
		t.Fatalf("expected AV1 fmtp line to be omitted, got %q", codec.SDPFmtpLine)
	}
}

func TestPeerConnectionFactorySeedsTWCCWithInitialBitrate(t *testing.T) {
	cfg := config.Default()
	cfg.Media.Mode = config.MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = config.AdaptiveBackendTWCCGCC
	factory, _, err := newPeerConnectionFactory(cfg)
	if err != nil {
		t.Fatalf("expected peer connection factory setup to succeed, got %v", err)
	}
	pc, estimator, err := factory.NewPeerConnection(5_000_000, webrtc.Configuration{})
	if err != nil {
		t.Fatalf("expected peer connection creation to succeed, got %v", err)
	}
	defer func() {
		_ = pc.Close()
	}()
	if estimator == nil {
		t.Fatal("expected a TWCC estimator")
	}
	if estimator.GetTargetBitrate() != 5_000_000 {
		t.Fatalf("expected initial TWCC bitrate to be 5000000 bps, got %d", estimator.GetTargetBitrate())
	}
}

func TestAssociatedEstimatorEnforcesConfiguredMediaFloorAcrossLossController(t *testing.T) {
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 1}
	pacer := newMinimumBitratePacerWithProtection(2_000_000, 2_000_000, protection)
	estimator, err := gcc.NewSendSideBWE(
		gcc.SendSideBWEPacer(pacer),
		gcc.SendSideBWEInitialBitrate(50_000),
		gcc.SendSideBWEMinBitrate(2_400_000),
		gcc.SendSideBWEMaxBitrate(9_600_000),
	)
	if err != nil {
		t.Fatalf("create estimator: %v", err)
	}
	t.Cleanup(func() {
		if err := estimator.Close(); err != nil {
			t.Errorf("close estimator: %v", err)
		}
	})
	wrapped := &associatedStreamBandwidthEstimator{
		SendSideBWE:         estimator,
		minimumMediaBitrate: 2_000_000,
		maximumMediaBitrate: 8_000_000,
		pacer:               pacer,
		protection:          protection,
	}
	if raw := estimator.GetTargetBitrate(); raw != 50_000 {
		t.Fatalf("raw Pion target = %d, want 50000", raw)
	}
	if effective := wrapped.GetTargetBitrate(); effective != 2_000_000 {
		t.Fatalf("effective media target = %d, want 2000000", effective)
	}
	stats := wrapped.GetStats()
	if raw, ok := stats["rawWireTargetBitrate"].(int); !ok || raw != 50_000 {
		t.Fatalf("raw wire target = %v, want 50000", stats["rawWireTargetBitrate"])
	}
	if raw, ok := stats["rawMediaTargetBitrate"].(int); !ok || raw != 41_666 {
		t.Fatalf("raw media target = %v, want 41666", stats["rawMediaTargetBitrate"])
	}
	if raw, ok := stats["wireTargetBitrate"].(int); !ok || raw != 50_000 {
		t.Fatalf("wire target = %v, want 50000", stats["wireTargetBitrate"])
	}
	if effective, ok := stats["effectiveWireTargetBitrate"].(int); !ok || effective != 2_400_000 {
		t.Fatalf("effective wire target = %v, want 2400000", stats["effectiveWireTargetBitrate"])
	}
	callbackTarget := 0
	wrapped.callbackMu.Lock()
	wrapped.targetCallback = func(bitrate int) {
		callbackTarget = bitrate
	}
	wrapped.callbackMu.Unlock()
	wrapped.deliverCurrentBitrate(50_000)
	if callbackTarget != 2_000_000 {
		t.Fatalf("callback media target = %d, want 2000000", callbackTarget)
	}
	if stale := wrapped.staleBitrateCallbacks.Load(); stale != 0 {
		t.Fatalf("matching raw callback counted as stale %d times", stale)
	}
	if wire := pacer.delegate.(*tokenBucketPacer).targetBitrateValue(); wire != 2_400_000 {
		t.Fatalf("paced effective wire target = %d, want 2400000", wire)
	}
}

func TestAssociatedEstimatorUsesPacerBacklogForRecoveryKeyFrameDelay(t *testing.T) {
	pacer := newMinimumBitratePacer(2_000_000, 2_000_000)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	delegate := pacer.delegate.(*tokenBucketPacer)
	delegate.keyFrameReserveBytes.Store(100_000)
	setSyntheticQueuedBytes(delegate, 500_000)
	estimator := &associatedStreamBandwidthEstimator{pacer: pacer}
	if delay := estimator.recoveryKeyFrameDelay(); delay <= 0 {
		t.Fatalf("recovery key-frame delay = %v, want a positive backlog delay", delay)
	}
	setSyntheticQueuedBytes(delegate, 0)
	if delay := estimator.recoveryKeyFrameDelay(); delay != 0 {
		t.Fatalf("drained recovery key-frame delay = %v, want 0", delay)
	}
}

func TestPeerConnectionFactoryKeepsTWCCProtocolWithoutEstimatorWhenAdaptiveIsOff(t *testing.T) {
	cfg := config.Default()
	factory, _, err := newPeerConnectionFactory(cfg)
	if err != nil {
		t.Fatalf("expected peer connection factory setup to succeed, got %v", err)
	}
	pc, estimator, err := factory.NewPeerConnection(5_000_000, webrtc.Configuration{})
	if err != nil {
		t.Fatalf("expected peer connection creation to succeed, got %v", err)
	}
	defer func() {
		_ = pc.Close()
	}()
	if estimator != nil {
		t.Fatal("expected no TWCC estimator when adaptive bitrate is disabled")
	}
}

func TestFlexFECRepairPacketsTraverseTWCCAndGCC(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Interceptors.FlexFEC = true
	cfg.WebRTC.Interceptors.FlexFECMediaPackets = 5
	cfg.WebRTC.Interceptors.FlexFECRepairPackets = 2
	factory, codec, err := newPeerConnectionFactory(cfg)
	if err != nil {
		t.Fatalf("create peer connection factory: %v", err)
	}
	producer, estimator, err := factory.NewPeerConnection(5_000_000, webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create producer peer connection: %v", err)
	}
	if estimator.GetTargetBitrate() != 5_000_000 {
		t.Fatalf("media target = %d, want 5000000", estimator.GetTargetBitrate())
	}
	associated, ok := estimator.(*associatedStreamBandwidthEstimator)
	if !ok {
		t.Fatalf("estimator type = %T, want *associatedStreamBandwidthEstimator", estimator)
	}
	if raw := associated.SendSideBWE.GetTargetBitrate(); raw != 7_000_000 {
		t.Fatalf("raw GCC wire target = %d, want 7000000", raw)
	}
	pacer, ok := associated.pacer.delegate.(*tokenBucketPacer)
	if !ok {
		t.Fatalf("pacer type = %T, want *tokenBucketPacer", associated.pacer.delegate)
	}
	if wire := pacer.targetBitrateValue(); wire != 7_000_000 {
		t.Fatalf("paced wire target = %d, want 7000000", wire)
	}
	if wire, ok := estimator.GetStats()["wireTargetBitrate"].(int); !ok || wire != 7_000_000 {
		t.Fatalf("wire target = %v, want 7000000", estimator.GetStats()["wireTargetBitrate"])
	}
	defer func() {
		_ = producer.Close()
	}()
	track, err := webrtc.NewTrackLocalStaticSample(codec, "video", "qualification")
	if err != nil {
		t.Fatalf("create producer track: %v", err)
	}
	if _, err := producer.AddTrack(track); err != nil {
		t.Fatalf("add producer track: %v", err)
	}
	viewerMediaEngine := &webrtc.MediaEngine{}
	if err := viewerMediaEngine.RegisterDefaultCodecs(); err != nil {
		t.Fatalf("register viewer codecs: %v", err)
	}
	viewerInterceptors := &interceptor.Registry{}
	if err := webrtc.ConfigureFlexFEC03(
		webrtc.PayloadType(cfg.FlexFECPayloadType()),
		viewerMediaEngine,
		viewerInterceptors,
	); err != nil {
		t.Fatalf("configure viewer FlexFEC: %v", err)
	}
	if err := webrtc.RegisterDefaultInterceptors(viewerMediaEngine, viewerInterceptors); err != nil {
		t.Fatalf("register viewer interceptors: %v", err)
	}
	viewerAPI := webrtc.NewAPI(
		webrtc.WithMediaEngine(viewerMediaEngine),
		webrtc.WithInterceptorRegistry(viewerInterceptors),
	)
	viewer, err := viewerAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create viewer peer connection: %v", err)
	}
	defer func() {
		_ = viewer.Close()
	}()
	connected := make(chan struct{}, 1)
	signalConnected := func() {
		if producer.ConnectionState() != webrtc.PeerConnectionStateConnected ||
			viewer.ConnectionState() != webrtc.PeerConnectionStateConnected {
			return
		}
		select {
		case connected <- struct{}{}:
		default:
		}
	}
	producer.OnConnectionStateChange(func(webrtc.PeerConnectionState) { signalConnected() })
	viewer.OnConnectionStateChange(func(webrtc.PeerConnectionState) { signalConnected() })
	if _, err := viewer.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	); err != nil {
		t.Fatalf("add viewer transceiver: %v", err)
	}
	offer, err := viewer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	viewerGatheringComplete := webrtc.GatheringCompletePromise(viewer)
	if err := viewer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set viewer local description: %v", err)
	}
	select {
	case <-viewerGatheringComplete:
	case <-time.After(5 * time.Second):
		t.Fatal("viewer ICE gathering timed out")
	}
	if err := producer.SetRemoteDescription(*viewer.LocalDescription()); err != nil {
		t.Fatalf("set producer remote description: %v", err)
	}
	answer, err := producer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	producerGatheringComplete := webrtc.GatheringCompletePromise(producer)
	if err := producer.SetLocalDescription(answer); err != nil {
		t.Fatalf("set producer local description: %v", err)
	}
	select {
	case <-producerGatheringComplete:
	case <-time.After(5 * time.Second):
		t.Fatal("producer ICE gathering timed out")
	}
	if !strings.Contains(producer.LocalDescription().SDP, "flexfec-03") {
		t.Fatal("producer answer did not negotiate FlexFEC-03")
	}
	if err := viewer.SetRemoteDescription(*producer.LocalDescription()); err != nil {
		t.Fatalf("set viewer remote description: %v", err)
	}
	signalConnected()
	select {
	case <-connected:
	case <-time.After(15 * time.Second):
		t.Fatalf(
			"peer connection timed out: producer=%s producer_ice=%s viewer=%s viewer_ice=%s",
			producer.ConnectionState(),
			producer.ICEConnectionState(),
			viewer.ConnectionState(),
			viewer.ICEConnectionState(),
		)
	}
	sample := media.Sample{
		Data:     []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21},
		Duration: time.Second / 30,
	}
	for index := 0; index < 20; index++ {
		if err := track.WriteSample(sample); err != nil {
			t.Fatalf("write protected sample %d: %v", index, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	stats := estimator.GetStats()
	if sent, ok := stats["pacerSentForwardErrorCorrection"].(uint64); !ok || sent < 8 {
		t.Fatalf("paced FEC packets = %v, want at least 8", stats["pacerSentForwardErrorCorrection"])
	}
	if averageLoss, ok := stats["averageLoss"].(float64); ok && averageLoss > 0.01 {
		t.Fatalf("loss estimator counted healthy FlexFEC traffic as %.2f%% loss", averageLoss*100)
	}
}

func TestFlexFECWireBudgetRoundsWithoutUnderProvisioning(t *testing.T) {
	tests := []struct {
		mediaTarget int
		protection  flexFECProtection
		wireTarget  int
	}{
		{mediaTarget: 1, protection: flexFECProtection{mediaPackets: 5, repairPackets: 1}, wireTarget: 2},
		{mediaTarget: 1_500_000, protection: flexFECProtection{mediaPackets: 5, repairPackets: 1}, wireTarget: 1_800_000},
		{mediaTarget: 5_000_001, protection: flexFECProtection{mediaPackets: 5, repairPackets: 1}, wireTarget: 6_000_002},
		{mediaTarget: 1_500_000, protection: flexFECProtection{mediaPackets: 5, repairPackets: 2}, wireTarget: 2_100_000},
		{mediaTarget: 5_000_001, protection: flexFECProtection{mediaPackets: 5, repairPackets: 2}, wireTarget: 7_000_002},
		{mediaTarget: 8_000_000, protection: flexFECProtection{mediaPackets: 110, repairPackets: 110}, wireTarget: 16_000_000},
	}
	for _, test := range tests {
		if wireTarget := wireBitrate(test.mediaTarget, test.protection); wireTarget != test.wireTarget {
			t.Fatalf("wire target for %d with %+v = %d, want %d", test.mediaTarget, test.protection, wireTarget, test.wireTarget)
		}
	}
	if wireBitrate(5_000_000, flexFECProtection{}) != 5_000_000 {
		t.Fatal("disabled FlexFEC changed the bitrate budget")
	}
}

func TestFlexFECWireAndMediaBudgetsPreserveTheirEnvelope(t *testing.T) {
	protections := []flexFECProtection{
		{},
		{mediaPackets: 5, repairPackets: 1},
		{mediaPackets: 5, repairPackets: 2},
		{mediaPackets: 110, repairPackets: 110},
	}
	for _, protection := range protections {
		for _, mediaTarget := range []int{1, 49_999, 1_500_000, 5_000_001, 8_000_000} {
			wireTarget := wireBitrate(mediaTarget, protection)
			if got := mediaBitrate(wireTarget, protection); got != mediaTarget {
				t.Fatalf("media round trip for %d with %+v = %d", mediaTarget, protection, got)
			}
		}
		for _, wireTarget := range []int{1, 50_000, 1_800_001, 7_000_002, 16_000_001} {
			mediaTarget := mediaBitrate(wireTarget, protection)
			if got := wireBitrate(mediaTarget, protection); got > wireTarget {
				t.Fatalf("wire round trip for %d with %+v overspent by %d", wireTarget, protection, got-wireTarget)
			}
		}
	}
}

func TestControllerTargetsExposeMediaAndWireUnits(t *testing.T) {
	stats := map[string]any{
		"lossTargetBitrate":  7_000_000,
		"delayTargetBitrate": 3_500_001,
		"unrelated":          42,
	}
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 2}
	convertControllerTargetToMedia(stats, "lossTargetBitrate", "rawWireLossTargetBitrate", protection)
	convertControllerTargetToMedia(stats, "delayTargetBitrate", "rawWireDelayTargetBitrate", protection)
	convertControllerTargetToMedia(stats, "unavailable", "rawWireUnavailable", protection)
	if got := stats["lossTargetBitrate"]; got != 5_000_000 {
		t.Fatalf("loss media target = %v, want 5000000", got)
	}
	if got := stats["rawWireLossTargetBitrate"]; got != 7_000_000 {
		t.Fatalf("loss wire target = %v, want 7000000", got)
	}
	if got := stats["delayTargetBitrate"]; got != 2_500_000 {
		t.Fatalf("delay media target = %v, want 2500000", got)
	}
	if got := stats["rawWireDelayTargetBitrate"]; got != 3_500_001 {
		t.Fatalf("delay wire target = %v, want 3500001", got)
	}
	if _, ok := stats["rawWireUnavailable"]; ok {
		t.Fatal("missing controller target produced a wire diagnostic")
	}
}

func TestFlexFECProtectionKeepsRepairWindowAndOverheadBounded(t *testing.T) {
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 1}
	mediaPackets := make([]rtp.Packet, protection.mediaPackets)
	for index := range mediaPackets {
		mediaPackets[index].SequenceNumber = uint16(index)
	}
	coverage := flexfec.NewCoverage(mediaPackets, protection.repairPackets)
	if coverage == nil {
		t.Fatal("expected a valid FlexFEC protection map")
	}
	protectedSequences := make([]uint16, 0, protection.mediaPackets)
	for repairIndex := uint32(0); repairIndex < protection.repairPackets; repairIndex++ {
		iterator := coverage.GetCoveredBy(repairIndex)
		for iterator.HasNext() {
			packet := iterator.Next()
			protectedSequences = append(protectedSequences, packet.SequenceNumber)
		}
	}
	if len(protectedSequences) != int(protection.mediaPackets) {
		t.Fatalf("protected media packets = %d, want %d", len(protectedSequences), protection.mediaPackets)
	}
	for index, sequence := range protectedSequences {
		if sequence != uint16(index) {
			t.Fatalf("protected sequence %d = %d", index, sequence)
		}
	}
	if wireBitrate(5_000_000, protection) != 6_000_000 {
		t.Fatal("bounded repair window changed the 20% wire overhead")
	}
}

func TestAssociatedEstimatorSupersedesOutOfOrderBitrateCallback(t *testing.T) {
	underlying, err := gcc.NewSendSideBWE(
		gcc.SendSideBWEInitialBitrate(6_000_000),
	)
	if err != nil {
		t.Fatalf("create bandwidth estimator: %v", err)
	}
	defer func() {
		if err := underlying.Close(); err != nil {
			t.Fatalf("close bandwidth estimator: %v", err)
		}
	}()
	estimator := &associatedStreamBandwidthEstimator{
		SendSideBWE: underlying,
		protection:  flexFECProtection{mediaPackets: 5, repairPackets: 1},
	}
	delivered := 0
	estimator.callbackMu.Lock()
	estimator.targetCallback = func(bitrate int) {
		delivered = bitrate
	}
	estimator.callbackMu.Unlock()
	estimator.deliverCurrentBitrate(1_800_000)
	if delivered != 5_000_000 {
		t.Fatalf("delivered stale bitrate %d, want current bitrate 5000000", delivered)
	}
	if stale := estimator.staleBitrateCallbacks.Load(); stale != 1 {
		t.Fatalf("stale callback count = %d, want 1", stale)
	}
}
