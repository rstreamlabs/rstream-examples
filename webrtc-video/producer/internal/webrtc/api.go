package webrtc

import (
	"fmt"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
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
			options := []gcc.Option{
				gcc.WithLoggerFactory(newPionLoggerFactory(f.cfg.Logging.Verbose)),
			}
			if initialBitrateBps > 0 {
				options = append(options, gcc.SendSideBWEInitialBitrate(initialBitrateBps))
			}
			options = append(
				options,
				gcc.SendSideBWEMinBitrate(f.cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps*1000),
				gcc.SendSideBWEMaxBitrate(f.cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps*1000),
			)
			return gcc.NewSendSideBWE(options...)
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

func newPionLoggerFactory(verbose bool) logging.LoggerFactory {
	factory := logging.NewDefaultLoggerFactory()
	if verbose {
		factory.ScopeLevels["gcc_delay_controller"] = logging.LogLevelDebug
		factory.ScopeLevels["gcc_loss_controller"] = logging.LogLevelDebug
	}
	return factory
}
