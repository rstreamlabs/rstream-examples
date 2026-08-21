package media

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestSourceOfferNegotiatesConfiguredRepairAndFeedback(t *testing.T) {
	peer, err := NewSourcePeer(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create source peer: %v", err)
	}
	defer func() { _ = peer.Close() }()
	_, err = peer.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	)
	if err != nil {
		t.Fatalf("add source transceiver: %v", err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create source offer: %v", err)
	}
	for _, expected := range []string{
		"a=rtpmap:96 H264/90000",
		"a=rtpmap:97 rtx/90000",
		"a=rtpmap:118 flexfec-03/90000",
		"a=rtcp-fb:96 transport-cc",
		"a=rtcp-fb:96 nack",
		"draft-holmer-rmcat-transport-wide-cc-extensions-01",
	} {
		if !strings.Contains(offer.SDP, expected) {
			t.Fatalf("source offer does not contain %q:\n%s", expected, offer.SDP)
		}
	}
}

func TestDestinationOfferDoesNotAdvertiseUpstreamRepairStreams(t *testing.T) {
	peer, err := NewDestinationPeer(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create destination peer: %v", err)
	}
	defer func() { _ = peer.Close() }()
	track, err := webrtc.NewTrackLocalStaticRTP(H264Capability(), "video", "distributor")
	if err != nil {
		t.Fatalf("create destination track: %v", err)
	}
	if _, err := peer.AddTrack(track); err != nil {
		t.Fatalf("add destination track: %v", err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create destination offer: %v", err)
	}
	if strings.Contains(offer.SDP, "flexfec") || strings.Contains(offer.SDP, " rtx/") {
		t.Fatalf("destination offer advertises source-leg repair streams:\n%s", offer.SDP)
	}
	for _, expected := range []string{
		"a=rtpmap:96 H264/90000",
		"a=rtcp-fb:96 transport-cc",
		"a=rtcp-fb:96 nack",
		"draft-holmer-rmcat-transport-wide-cc-extensions-01",
	} {
		if !strings.Contains(offer.SDP, expected) {
			t.Fatalf("destination offer does not contain %q:\n%s", expected, offer.SDP)
		}
	}
}
