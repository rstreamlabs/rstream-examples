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
		"a=fmtp:97 apt=96",
		"a=fmtp:99 apt=98",
		"a=fmtp:101 apt=100",
		"a=fmtp:103 apt=102",
		"profile-level-id=4d002a",
		"profile-level-id=64002a",
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
	peer, err := NewDestinationPeer(webrtc.Configuration{}, H264Capability())
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

// Exercise actual offer/answer negotiation: the default Pion matching fallback
// must not turn a Main-profile source into a Baseline track on the next hop.
func TestMainSourceProfileSurvivesBothLegs(t *testing.T) {
	sourceCodec := H264Capability()
	sourceCodec.SDPFmtpLine = "packetization-mode=1;profile-level-id=4d0028;level-asymmetry-allowed=1"
	sender, err := NewDestinationPeer(webrtc.Configuration{}, sourceCodec)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	track, err := webrtc.NewTrackLocalStaticRTP(sourceCodec, "video", "source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sender.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	receiver, err := NewSourcePeer(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	tr, err := receiver.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := receiver.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = receiver.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err = sender.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}
	answer, err := sender.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	if err = receiver.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}
	var negotiated webrtc.RTPCodecCapability
	for _, codec := range tr.Receiver().GetParameters().Codecs {
		if codec.MimeType == webrtc.MimeTypeH264 {
			if !strings.Contains(codec.SDPFmtpLine, "profile-level-id=4d00") {
				t.Fatalf("source mislabeled: %+v", codec)
			}
			negotiated = codec.RTPCodecCapability
		}
	}
	if negotiated.MimeType == "" {
		t.Fatal("H264 not negotiated")
	}
	destination, err := NewDestinationPeer(webrtc.Configuration{}, ForwardCapability(negotiated))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	out, err := webrtc.NewTrackLocalStaticRTP(ForwardCapability(negotiated), "video", "forwarded")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = destination.AddTrack(out); err != nil {
		t.Fatal(err)
	}
	destOffer, err := destination.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(destOffer.SDP, negotiated.SDPFmtpLine) || strings.Contains(destOffer.SDP, "profile-level-id=42") {
		t.Fatalf("destination relabeled source:\n%s", destOffer.SDP)
	}
}
