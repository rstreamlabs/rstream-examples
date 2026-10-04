package webrtc

import (
	"context"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func TestFixedH264PipelineHonorsReceiverEnvelope(t *testing.T) {
	for _, test := range []struct {
		name, sender string
		receivers    []string
		accepted     bool
		profiles     bool
	}{
		{"matching 720p level", "42e01f", []string{"42e01f"}, true, false},
		{"larger receiver", "42e01f", []string{"42e028"}, true, false},
		{"smaller receiver", "42e01f", []string{"42e01e"}, false, false},
		{"1080p to matching receiver", "42e028", []string{"42e028"}, true, false},
		{"asymmetry cannot authorize 1080p to 720p receiver", "42e028", []string{"42e01f"}, false, false},
		{"selected payload is too small", "42e028", []string{"42e01f", "42e028"}, false, false},
		{"selected payload is sufficient", "42e028", []string{"42e028", "42e01f"}, true, false},
		{"small profiles do not lower declared encoded level", "42e028", []string{"42e01f"}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.WebRTC.UseTURN = false
			fmtp := "packetization-mode=1;level-asymmetry-allowed=1;profile-level-id=" + test.sender
			cfg.WebRTC.Video.SDPFmtpLine = &fmtp
			b, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logs.NewLogger(logs.NewHub(16), false))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			session, err := b.OpenSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if test.profiles {
				session.formatConfig = receiverFormatConfig()
				session.formatBitrateLimit = cfg.MaximumVideoBitrateKbps()
			}
			engine := &webrtc.MediaEngine{}
			for i, profile := range test.receivers {
				err := engine.RegisterCodec(webrtc.RTPCodecParameters{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
						SDPFmtpLine: "packetization-mode=1;level-asymmetry-allowed=1;profile-level-id=" + profile,
					},
					PayloadType: webrtc.PayloadType(96 + i),
				}, webrtc.RTPCodecTypeVideo)
				if err != nil {
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
			_, err = session.createAnswer(context.Background(), offer.SDP, false)
			if (err == nil) != test.accepted {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, test.accepted, err)
			}
			if !test.accepted && !strings.Contains(err.Error(), "receiver cannot accept") {
				t.Fatalf("receiver was refused for an unexpected reason: %v", err)
			}
		})
	}
}
