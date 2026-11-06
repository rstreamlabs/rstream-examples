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
	if err := registerCodecs(mediaEngine, sourceCapabilities(), true); err != nil {
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

func NewDestinationPeer(configuration webrtc.Configuration, codec webrtc.RTPCodecCapability) (*webrtc.PeerConnection, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := registerCodecs(mediaEngine, []webrtc.RTPCodecCapability{codec}, false); err != nil {
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

// Receive compressed media without decoding or transcoding. Offer the common
// WebRTC H264 profiles through level 4.2; the negotiated source capability is
// then preserved on the destination leg instead of relabeling every source as
// constrained baseline. Each profile has its own RTX association.
func sourceCapabilities() []webrtc.RTPCodecCapability {
	profiles := []string{"42e02a", "42002a", "4d002a", "64002a"}
	codecs := make([]webrtc.RTPCodecCapability, 0, len(profiles))
	for _, profile := range profiles {
		codec := H264Capability()
		codec.SDPFmtpLine = "packetization-mode=1;profile-level-id=" + profile + ";level-asymmetry-allowed=1"
		codecs = append(codecs, codec)
	}
	return codecs
}

// ForwardCapability keeps the negotiated codec parameters. Feedback belongs
// to the new transport and is not copied from the upstream peer.
func ForwardCapability(source webrtc.RTPCodecCapability) webrtc.RTPCodecCapability {
	codec := source
	codec.RTCPFeedback = H264Capability().RTCPFeedback
	return codec
}

func registerCodecs(mediaEngine *webrtc.MediaEngine, codecs []webrtc.RTPCodecCapability, repair bool) error {
	for index, codec := range codecs {
		if codec.MimeType != webrtc.MimeTypeH264 || codec.ClockRate != 90000 || codec.Channels != 0 {
			return fmt.Errorf("unsupported forwarded video codec")
		}
		primary := webrtc.PayloadType(PrimaryPayloadType + uint8(2*index))
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: codec, PayloadType: primary}, webrtc.RTPCodecTypeVideo); err != nil {
			return fmt.Errorf("register H264 codec: %w", err)
		}
		if repair {
			if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: fmt.Sprintf("apt=%d", primary)},
				PayloadType:        primary + 1,
			}, webrtc.RTPCodecTypeVideo); err != nil {
				return fmt.Errorf("register RTX codec: %w", err)
			}
		}
	}
	if !repair {
		return nil
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeFlexFEC03, ClockRate: 90000, SDPFmtpLine: "repair-window=10000000"},
		PayloadType:        webrtc.PayloadType(FlexFECPayloadType),
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return fmt.Errorf("register FlexFEC codec: %w", err)
	}
	return nil
}
