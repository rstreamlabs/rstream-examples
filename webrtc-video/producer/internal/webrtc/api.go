package webrtc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/flexfec"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

type bandwidthEstimator interface {
	GetTargetBitrate() int
	OnTargetBitrateChange(func(int))
	GetStats() map[string]any
}

type peerConnectionFactory struct {
	cfg   config.Config
	codec webrtc.RTPCodecCapability
}

const transportCCHeaderExtensionURI = "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01"

type flexFECProtection struct {
	mediaPackets  uint32
	repairPackets uint32
}

func (p flexFECProtection) enabled() bool {
	return p.mediaPackets > 0 && p.repairPackets > 0
}

func newPeerConnectionFactory(cfg config.Config) (*peerConnectionFactory, webrtc.RTPCodecCapability, error) {
	codec := webrtc.RTPCodecCapability{
		MimeType:  cfg.WebRTC.Video.MimeType,
		ClockRate: cfg.WebRTC.Video.ClockRate,
		RTCPFeedback: []webrtc.RTCPFeedback{
			{Type: webrtc.TypeRTCPFBGoogREMB},
			{Type: "ccm", Parameter: "fir"},
			{Type: "nack"},
			{Type: "nack", Parameter: "pli"},
		},
	}
	if cfg.WebRTC.Video.SDPFmtpLine != nil {
		codec.SDPFmtpLine = *cfg.WebRTC.Video.SDPFmtpLine
	}
	return &peerConnectionFactory{
		cfg:   cfg,
		codec: codec,
	}, codec, nil
}

func (f *peerConnectionFactory) NewPeerConnection(
	initialBitrateBps int,
	configuration webrtc.Configuration,
) (*webrtc.PeerConnection, bandwidthEstimator, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: f.codec,
		PayloadType:        webrtc.PayloadType(f.cfg.WebRTC.Video.PayloadType),
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("failed to register the primary video codec: %w", err)
	}
	if f.cfg.WebRTC.Interceptors.RTX {
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:    webrtc.MimeTypeRTX,
				ClockRate:   f.cfg.WebRTC.Video.ClockRate,
				SDPFmtpLine: fmt.Sprintf("apt=%d", f.cfg.WebRTC.Video.PayloadType),
			},
			PayloadType: webrtc.PayloadType(f.cfg.RTXPayloadType()),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, nil, fmt.Errorf("failed to register the RTX codec: %w", err)
		}
	}
	interceptors := &interceptor.Registry{}
	var estimators chan bandwidthEstimator
	if f.cfg.WebRTC.Interceptors.TWCC && f.cfg.AdaptiveBackend() != config.AdaptiveBackendOff {
		estimators = make(chan bandwidthEstimator, 1)
		congestionController, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			protection := f.flexFECProtection()
			minimumMediaBitrateBps := f.cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps * 1000
			maximumMediaBitrateBps := f.cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps * 1000
			initialWireBitrateBps := wireBitrate(initialBitrateBps, protection)
			minimumWireBitrateBps := wireBitrate(minimumMediaBitrateBps, protection)
			maximumWireBitrateBps := wireBitrate(maximumMediaBitrateBps, protection)
			pacer := newMinimumBitratePacerWithProtection(
				initialBitrateBps,
				minimumMediaBitrateBps,
				protection,
			)
			options := []gcc.Option{
				gcc.WithLoggerFactory(newPionLoggerFactory(f.cfg.Logging.Verbose)),
				gcc.SendSideBWEPacer(pacer),
			}
			if initialWireBitrateBps > 0 {
				options = append(options, gcc.SendSideBWEInitialBitrate(initialWireBitrateBps))
			}
			options = append(
				options,
				gcc.SendSideBWEMinBitrate(minimumWireBitrateBps),
				gcc.SendSideBWEMaxBitrate(maximumWireBitrateBps),
			)
			estimator, err := gcc.NewSendSideBWE(options...)
			if err != nil {
				return nil, err
			}
			return &associatedStreamBandwidthEstimator{
				SendSideBWE:         estimator,
				minimumMediaBitrate: minimumMediaBitrateBps,
				maximumMediaBitrate: maximumMediaBitrateBps,
				lossGuard:           newFeedbackLossGuard(minimumMediaBitrateBps),
				pacer:               pacer,
				protection:          protection,
			}, nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create the congestion controller: %w", err)
		}
		congestionController.OnNewPeerConnection(func(_ string, estimator cc.BandwidthEstimator) {
			estimators <- estimator
		})
		interceptors.Add(congestionController)
	}
	if f.cfg.WebRTC.Interceptors.FlexFEC {
		if err := webrtc.ConfigureFlexFEC03(
			webrtc.PayloadType(f.cfg.FlexFECPayloadType()),
			mediaEngine,
			interceptors,
			flexfec.NumMediaPackets(f.cfg.FlexFECMediaPackets()),
			flexfec.NumFECPackets(f.cfg.FlexFECRepairPackets()),
		); err != nil {
			return nil, nil, fmt.Errorf("failed to enable FlexFEC: %w", err)
		}
	}
	if f.cfg.WebRTC.Interceptors.TWCC {
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(mediaEngine, interceptors); err != nil {
			return nil, nil, fmt.Errorf("failed to enable TWCC header extensions: %w", err)
		}
	}
	if f.cfg.WebRTC.Interceptors.NACK {
		if err := webrtc.ConfigureNack(mediaEngine, interceptors); err != nil {
			return nil, nil, fmt.Errorf("failed to enable NACK: %w", err)
		}
	}
	if err := webrtc.ConfigureRTCPReports(interceptors); err != nil {
		return nil, nil, fmt.Errorf("failed to enable RTCP reports: %w", err)
	}
	if f.cfg.WebRTC.Interceptors.TWCC {
		if err := webrtc.ConfigureTWCCSender(mediaEngine, interceptors); err != nil {
			return nil, nil, fmt.Errorf("failed to enable TWCC feedback: %w", err)
		}
	}
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptors),
	)
	peerConnection, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, nil, err
	}
	if estimators == nil {
		return peerConnection, nil, nil
	}
	select {
	case estimator := <-estimators:
		return peerConnection, estimator, nil
	case <-time.After(time.Second):
		_ = peerConnection.Close()
		return nil, nil, fmt.Errorf("failed to initialize the TWCC bandwidth estimator")
	}
}

type associatedStreamBandwidthEstimator struct {
	*gcc.SendSideBWE
	minimumMediaBitrate   int
	maximumMediaBitrate   int
	lossGuard             *feedbackLossGuard
	pacer                 *minimumBitratePacer
	protection            flexFECProtection
	callbackMu            sync.RWMutex
	targetCallback        func(int)
	lastDeliveredBitrate  atomic.Int64
	staleBitrateCallbacks atomic.Uint64
	twccFeedbackPackets   atomic.Uint64
	twccMalformedFeedback atomic.Uint64
	twccPaddingStatuses   atomic.Uint64
	twccReportedLost      atomic.Uint64
	twccReportedStatuses  atomic.Uint64
}

func (e *associatedStreamBandwidthEstimator) GetTargetBitrate() int {
	target := e.effectiveMediaBitrate(mediaBitrate(e.SendSideBWE.GetTargetBitrate(), e.protection))
	if e.lossGuard != nil {
		target = e.lossGuard.effectiveBitrate(target)
	}
	return target
}

func (e *associatedStreamBandwidthEstimator) OnTargetBitrateChange(callback func(int)) {
	e.callbackMu.Lock()
	e.targetCallback = callback
	e.callbackMu.Unlock()
	e.SendSideBWE.OnTargetBitrateChange(func(bitrate int) {
		e.deliverCurrentBitrate(bitrate)
	})
}

func (e *associatedStreamBandwidthEstimator) deliverCurrentBitrate(callbackWireBitrate int) {
	currentRawWireBitrate := e.SendSideBWE.GetTargetBitrate()
	if callbackWireBitrate != currentRawWireBitrate {
		e.staleBitrateCallbacks.Add(1)
	}
	e.deliverEffectiveBitrate(e.GetTargetBitrate())
}

func (e *associatedStreamBandwidthEstimator) deliverEffectiveBitrate(bitrate int) {
	if e.pacer != nil {
		e.pacer.SetMediaTargetBitrate(bitrate)
	}
	previous := e.lastDeliveredBitrate.Swap(int64(bitrate))
	if previous == int64(bitrate) {
		return
	}
	e.callbackMu.RLock()
	callback := e.targetCallback
	e.callbackMu.RUnlock()
	if callback != nil {
		callback(bitrate)
	}
}

func (e *associatedStreamBandwidthEstimator) GetStats() map[string]any {
	stats := e.SendSideBWE.GetStats()
	rawWireBitrate := e.SendSideBWE.GetTargetBitrate()
	rawMediaBitrate := mediaBitrate(rawWireBitrate, e.protection)
	effectiveMediaBitrate := e.effectiveMediaBitrate(rawMediaBitrate)
	if e.lossGuard != nil {
		effectiveMediaBitrate = e.lossGuard.effectiveBitrate(effectiveMediaBitrate)
	}
	convertControllerTargetToMedia(stats, "lossTargetBitrate", "rawWireLossTargetBitrate", e.protection)
	convertControllerTargetToMedia(stats, "delayTargetBitrate", "rawWireDelayTargetBitrate", e.protection)
	stats["rawWireTargetBitrate"] = rawWireBitrate
	stats["rawMediaTargetBitrate"] = rawMediaBitrate
	stats["mediaTargetBitrate"] = effectiveMediaBitrate
	stats["wireTargetBitrate"] = rawWireBitrate
	stats["effectiveWireTargetBitrate"] = wireBitrate(effectiveMediaBitrate, e.protection)
	stats["flexFECMediaPackets"] = e.protection.mediaPackets
	stats["flexFECRepairPackets"] = e.protection.repairPackets
	stats["staleBitrateCallbacks"] = e.staleBitrateCallbacks.Load()
	stats["twccFeedbackPackets"] = e.twccFeedbackPackets.Load()
	stats["twccMalformedFeedback"] = e.twccMalformedFeedback.Load()
	stats["twccPaddingStatuses"] = e.twccPaddingStatuses.Load()
	stats["twccReportedLost"] = e.twccReportedLost.Load()
	stats["twccReportedStatuses"] = e.twccReportedStatuses.Load()
	if e.lossGuard != nil {
		guard := e.lossGuard.snapshot()
		stats["lossGuardActive"] = guard.Active
		stats["lossGuardTargetBitrate"] = guard.TargetBitrate
		stats["lossGuardLastObservedLoss"] = guard.LastObservedLoss
		stats["lossGuardReductions"] = guard.Reductions
		stats["lossGuardRecoveries"] = guard.Recoveries
	}
	for name, value := range e.pacerStats() {
		stats[name] = value
	}
	return stats
}

func (e *associatedStreamBandwidthEstimator) effectiveMediaBitrate(mediaBitrateBps int) int {
	if e.minimumMediaBitrate > 0 && mediaBitrateBps < e.minimumMediaBitrate {
		mediaBitrateBps = e.minimumMediaBitrate
	}
	if e.maximumMediaBitrate > 0 && mediaBitrateBps > e.maximumMediaBitrate {
		mediaBitrateBps = e.maximumMediaBitrate
	}
	return mediaBitrateBps
}

func (e *associatedStreamBandwidthEstimator) AdmitMediaFrame(
	size int,
	keyFrame bool,
) mediaFrameAdmission {
	controller, ok := e.pacer.delegate.(interface {
		AdmitMediaFrame(int, bool) mediaFrameAdmission
	})
	if !ok {
		return mediaFrameAdmission{admitted: true}
	}
	return controller.AdmitMediaFrame(size, keyFrame)
}

func (e *associatedStreamBandwidthEstimator) UseFixedMediaTargetBitrate(bitrate int) {
	if e.pacer != nil {
		e.pacer.UseFixedMediaTargetBitrate(bitrate)
	}
}

func wireBitrate(mediaBitrateBps int, protection flexFECProtection) int {
	if !protection.enabled() || mediaBitrateBps <= 0 {
		return mediaBitrateBps
	}
	totalPackets := int64(protection.mediaPackets) + int64(protection.repairPackets)
	mediaPackets := int64(protection.mediaPackets)
	mediaBitrate := int64(mediaBitrateBps)
	return int((mediaBitrate*totalPackets + mediaPackets - 1) / mediaPackets)
}

func mediaBitrate(wireBitrateBps int, protection flexFECProtection) int {
	if !protection.enabled() || wireBitrateBps <= 0 {
		return wireBitrateBps
	}
	totalPackets := int64(protection.mediaPackets) + int64(protection.repairPackets)
	mediaPackets := int64(protection.mediaPackets)
	return int(int64(wireBitrateBps) * mediaPackets / totalPackets)
}

func convertControllerTargetToMedia(
	stats map[string]any,
	mediaName string,
	wireName string,
	protection flexFECProtection,
) {
	wireTarget, ok := stats[mediaName].(int)
	if !ok {
		return
	}
	stats[wireName] = wireTarget
	stats[mediaName] = mediaBitrate(wireTarget, protection)
}

func (f *peerConnectionFactory) flexFECProtection() flexFECProtection {
	if !f.cfg.WebRTC.Interceptors.FlexFEC {
		return flexFECProtection{}
	}
	return flexFECProtection{
		mediaPackets:  f.cfg.FlexFECMediaPackets(),
		repairPackets: f.cfg.FlexFECRepairPackets(),
	}
}

func (e *associatedStreamBandwidthEstimator) pacerStats() map[string]any {
	provider, ok := e.pacer.delegate.(interface{ Stats() map[string]any })
	if !ok {
		return nil
	}
	return provider.Stats()
}

func (e *associatedStreamBandwidthEstimator) AddStream(
	info *interceptor.StreamInfo,
	writer interceptor.RTPWriter,
) interceptor.RTPWriter {
	result := e.SendSideBWE.AddStream(info, writer)
	transportCCExtensionID := findTransportCCExtensionID(info)
	e.pacer.addAssociatedStreams(
		info.SSRC,
		info.SSRCRetransmission,
	)
	e.pacer.setTransportCCExtension(info.SSRC, transportCCExtensionID, true)
	e.pacer.setTransportCCExtension(
		info.SSRCRetransmission,
		transportCCExtensionID,
		true,
	)
	// Chromium does not acknowledge FlexFEC packets in transport-wide
	// feedback. Pace them, but do not assign TWCC sequence numbers or count
	// their absence as media loss in GCC.
	e.pacer.addUntrackedRepairStream(info.SSRCForwardErrorCorrection, writer)
	e.pacer.setTransportCCExtension(
		info.SSRCForwardErrorCorrection,
		transportCCExtensionID,
		false,
	)
	return result
}

func findTransportCCExtensionID(info *interceptor.StreamInfo) uint8 {
	if info == nil {
		return 0
	}
	for _, extension := range info.RTPHeaderExtensions {
		if extension.URI == transportCCHeaderExtensionURI && extension.ID > 0 && extension.ID <= 255 {
			return uint8(extension.ID)
		}
	}
	return 0
}

func newPionLoggerFactory(verbose bool) logging.LoggerFactory {
	factory := logging.NewDefaultLoggerFactory()
	if verbose {
		factory.ScopeLevels["gcc_delay_controller"] = logging.LogLevelDebug
		factory.ScopeLevels["gcc_loss_controller"] = logging.LogLevelDebug
	}
	return factory
}
