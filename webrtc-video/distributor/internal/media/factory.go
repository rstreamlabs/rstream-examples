package media

import (
	"fmt"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

const (
	PrimaryPayloadType uint8 = 96
	RTXPayloadType     uint8 = 97
	FlexFECPayloadType uint8 = 118
)

const H264FMTP = "packetization-mode=1;profile-level-id=42e01f;level-asymmetry-allowed=1"

func H264Capability() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: H264FMTP,
		RTCPFeedback: []webrtc.RTCPFeedback{
			{Type: webrtc.TypeRTCPFBTransportCC},
			{Type: "ccm", Parameter: "fir"},
			{Type: "nack"},
			{Type: "nack", Parameter: "pli"},
		},
	}
}

func NewSourcePeer(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := registerCodecs(mediaEngine, true); err != nil {
		return nil, err
	}
	interceptors := &interceptor.Registry{}
	if err := webrtc.ConfigureRTCPReports(interceptors); err != nil {
		return nil, fmt.Errorf("configure source RTCP reports: %w", err)
	}
	if err := webrtc.ConfigureStatsInterceptor(interceptors); err != nil {
		return nil, fmt.Errorf("configure source statistics: %w", err)
	}
	if err := webrtc.ConfigureTWCCSender(mediaEngine, interceptors); err != nil {
		return nil, fmt.Errorf("configure source TWCC feedback: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptors))
	peer, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, fmt.Errorf("create source peer connection: %w", err)
	}
	return peer, nil
}

func NewDestinationPeer(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := registerCodecs(mediaEngine, false); err != nil {
		return nil, err
	}
	interceptors := &interceptor.Registry{}
	if err := webrtc.ConfigureNack(mediaEngine, interceptors); err != nil {
		return nil, fmt.Errorf("configure destination NACK: %w", err)
	}
	if err := webrtc.ConfigureRTCPReports(interceptors); err != nil {
		return nil, fmt.Errorf("configure destination RTCP reports: %w", err)
	}
	if err := webrtc.ConfigureStatsInterceptor(interceptors); err != nil {
		return nil, fmt.Errorf("configure destination statistics: %w", err)
	}
	if err := webrtc.ConfigureTWCCHeaderExtensionSender(mediaEngine, interceptors); err != nil {
		return nil, fmt.Errorf("configure destination TWCC header: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptors))
	peer, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, fmt.Errorf("create destination peer connection: %w", err)
	}
	return peer, nil
}

func registerCodecs(mediaEngine *webrtc.MediaEngine, repair bool) error {
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: H264Capability(), PayloadType: webrtc.PayloadType(PrimaryPayloadType)}, webrtc.RTPCodecTypeVideo); err != nil {
		return fmt.Errorf("register H264 codec: %w", err)
	}
	if !repair {
		return nil
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: fmt.Sprintf("apt=%d", PrimaryPayloadType)},
		PayloadType:        webrtc.PayloadType(RTXPayloadType),
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return fmt.Errorf("register RTX codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeFlexFEC03, ClockRate: 90000, SDPFmtpLine: "repair-window=10000000"},
		PayloadType:        webrtc.PayloadType(FlexFECPayloadType),
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return fmt.Errorf("register FlexFEC codec: %w", err)
	}
	return nil
}
