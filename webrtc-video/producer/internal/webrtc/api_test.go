package webrtc

import (
	"testing"

	"github.com/pion/webrtc/v4"
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
