package webrtc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

func receiverFormatConfig() *config.SourceFormatConfig {
	return &config.SourceFormatConfig{CapsFilter: "source_format", Default: "large", Profiles: []config.SourceFormatProfile{
		{ID: "small", Format: media.SourceFormat{Width: 320, Height: 180, FrameRate: media.FrameRate{Numerator: 15, Denominator: 1}}},
		{ID: "large", Format: media.SourceFormat{Width: 1280, Height: 720, FrameRate: media.FrameRate{Numerator: 30, Denominator: 1}}},
	}}
}

func TestSourceProfilesRespectTheActuallyNegotiatedReceiverParameters(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters []string
		accepted   bool
	}{
		{"720p receiver", []string{"profile-level-id=42e01f;packetization-mode=1"}, true},
		{"smaller receiver", []string{"profile-level-id=42e01e;packetization-mode=1"}, false},
		{"larger receiver", []string{"profile-level-id=42e028;packetization-mode=1"}, true},
		{"receive extensions", []string{"profile-level-id=42e01e;packetization-mode=1;max-fs=3600;max-mbps=108000"}, true},
		{"asymmetry does not grant extra receive capacity", []string{"profile-level-id=42e01e;packetization-mode=1;level-asymmetry-allowed=1"}, false},
		{"first supported codec too small", []string{"profile-level-id=42e01e;packetization-mode=1", "profile-level-id=42e028;packetization-mode=1"}, false},
		{"first supported codec sufficient", []string{"profile-level-id=42e028;packetization-mode=1", "profile-level-id=42e01e;packetization-mode=1"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.WebRTC.UseTURN = false
			cfg.WebRTC.Adaptive.Enabled = false
			b, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logs.NewLogger(logs.NewHub(16), false))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			session, err := b.OpenSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			// Exercise Pion's real codec negotiation independently of the source
			// implementation, including multiple matching H264 payload types.
			session.formatConfig = receiverFormatConfig()
			session.formatBitrateLimit = 6000
			engine := &webrtc.MediaEngine{}
			for index, fmtp := range test.parameters {
				if err := engine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: fmtp}, PayloadType: webrtc.PayloadType(96 + index)}, webrtc.RTPCodecTypeVideo); err != nil {
					t.Fatal(err)
				}
			}
			client, err := webrtc.NewAPI(webrtc.WithMediaEngine(engine)).NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatal(err)
			}
			offer, err := client.CreateOffer(nil)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := session.createAnswer(context.Background(), offer.SDP, false)
			if test.accepted {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(answer, test.parameters[0]) {
					t.Fatal("answer lost receiver codec parameters")
				}
			} else if err == nil || !strings.Contains(err.Error(), "receiver cannot accept configured source profiles") {
				t.Fatalf("receiver bound not enforced: %v", err)
			}
		})
	}
}

type formatLifecycleSource struct {
	lifecycleTestSource
	format media.SourceFormatState
}

func (s *formatLifecycleSource) FormatController() (media.SourceFormatController, bool) {
	return s, true
}

func (s *formatLifecycleSource) EncoderController() (media.EncoderController, bool) {
	return nonRequestingEncoder{}, true
}
func (s *formatLifecycleSource) Snapshot() media.SourceFormatState { return s.format }
func (s *formatLifecycleSource) ApplyFormat(context.Context, media.SourceFormat) (media.SourceFormatState, error) {
	return s.format, nil
}

type formatLifecycleFactory struct{ source *formatLifecycleSource }

func (f formatLifecycleFactory) New() (media.Source, error) { return f.source, nil }

func TestSourceFormatInitializationErrorsReleaseTheSourceOnce(t *testing.T) {
	for _, phase := range []string{"initial caps", "worker configuration"} {
		t.Run(phase, func(t *testing.T) {
			cfg := config.Default()
			cfg.WebRTC.UseTURN = false
			cfg.Media.Format = receiverFormatConfig()
			source := &formatLifecycleSource{format: media.SourceFormatState{Requested: cfg.Media.Format.Profiles[1].Format}}
			if phase == "initial caps" {
				source.format.Requested = cfg.Media.Format.Profiles[0].Format
			} else {
				cfg.Media.Format.TransitionTimeout = "invalid"
			}
			b, err := NewBroadcaster(cfg, formatLifecycleFactory{source}, nil, logs.NewLogger(logs.NewHub(16), false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.OpenSession(context.Background()); err == nil {
				t.Fatal("invalid initialization succeeded")
			}
			_ = b.Close()
			starts, closes := source.counts()
			if closes != 1 {
				t.Fatalf("source was released %d times", closes)
			}
			if phase == "initial caps" && starts != 0 {
				t.Fatal("mismatched caps started source")
			}
			if b.opening != 0 || len(b.sessions) != 0 {
				t.Fatal("failed initialization retained admission capacity")
			}
		})
	}
}

func TestConfiguredPresetsChangeActualGStreamerOutputAndRestoreAuto(t *testing.T) {
	t.Setenv("API_URL", "https://video.example.com")
	t.Setenv("DEVICE_SECRET", "test-device-secret")
	cfg, err := config.Load(filepath.Join("..", "..", "config.provisioning.source-formats.h264.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(128), false)
	factory := media.NewGStreamerFactory(cfg.Media.Pipeline, cfg.Media.SinkName, cfg.InitialBitrateKbps(), logger, &media.GStreamerFormatConfig{CapsFilter: cfg.Media.Format.CapsFilter})
	b, err := NewBroadcaster(cfg, factory, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	idle, err := b.QualityState()
	if err != nil || idle.SourceFormat == nil || idle.SourceFormat.ActiveEncoders != 0 {
		t.Fatalf("idle format state: %+v %v", idle, err)
	}
	session, err := b.OpenSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: *cfg.WebRTC.Video.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: webrtc.TypeRTCPFBTransportCC}},
	}, PayloadType: 96}, webrtc.RTPCodecTypeVideo); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: transportCCHeaderExtensionURI}, webrtc.RTPCodecTypeVideo); err != nil {
		t.Fatal(err)
	}
	client, err := webrtc.NewAPI(webrtc.WithMediaEngine(engine)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.createAnswer(context.Background(), offer.SDP, false); err != nil {
		t.Fatal(err)
	}
	if !session.StatsSnapshot().AdaptiveActive {
		t.Fatal("negotiated TWCC did not start format worker")
	}
	for _, mode := range []string{"low", "medium", "high", "low", "auto"} {
		before, err := b.QualityState()
		if err != nil {
			t.Fatal(err)
		}
		after, err := b.SelectQuality(context.Background(), mode, before.Version)
		if err != nil {
			t.Fatal(err)
		}
		want := cfg.Media.Format.Default
		for _, preset := range after.Modes {
			if preset.ID == mode && preset.SourceProfile != "" {
				want = preset.SourceProfile
			}
		}
		deadline := time.Now().Add(4 * time.Second)
		for {
			state, err := b.QualityState()
			if err != nil {
				t.Fatal(err)
			}
			confirmed := false
			for _, profile := range state.SourceFormat.Profiles {
				if profile.ID == want && profile.ObservedEncoders == 1 && profile.RequestedEncoders == 1 {
					confirmed = true
				}
			}
			if confirmed && state.SourceFormat.PendingEncoders == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not converge: %+v", mode, state.SourceFormat)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	session.Close("format test complete")
	after, err := b.QualityState()
	if err != nil || after.SourceFormat.ActiveEncoders != 0 || after.Selected != "auto" {
		t.Fatalf("closed source state: %+v %v", after, err)
	}
}
