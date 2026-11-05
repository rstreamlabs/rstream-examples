package webrtc

import (
	"bytes"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type packetCapture struct {
	codec   webrtc.RTPCodecCapability
	packets []*rtp.Packet
}

func (c *packetCapture) CodecParameters() []webrtc.RTPCodecParameters {
	return []webrtc.RTPCodecParameters{{RTPCodecCapability: c.codec, PayloadType: 96}}
}
func (*packetCapture) HeaderExtensions() []webrtc.RTPHeaderExtensionParameter { return nil }
func (*packetCapture) SSRC() webrtc.SSRC                                      { return 123 }
func (*packetCapture) SSRCRetransmission() webrtc.SSRC                        { return 0 }
func (*packetCapture) SSRCForwardErrorCorrection() webrtc.SSRC                { return 0 }
func (c *packetCapture) WriteStream() webrtc.TrackLocalWriter                 { return c }
func (*packetCapture) ID() string                                             { return "capture" }
func (*packetCapture) RTCPReader() interceptor.RTCPReader                     { return nil }
func (c *packetCapture) WriteRTP(header *rtp.Header, payload []byte) (int, error) {
	packet := (&rtp.Packet{Header: *header, Payload: payload}).Clone()
	c.packets = append(c.packets, packet)
	return packet.MarshalSize(), nil
}

func (c *packetCapture) Write(data []byte) (int, error) {
	packet := new(rtp.Packet)
	if err := packet.Unmarshal(data); err != nil {
		return 0, err
	}
	c.packets = append(c.packets, packet.Clone())
	return len(data), nil
}

func TestVideoTrackPreservesFrameTimingWithH264Aggregation(t *testing.T) {
	frame := []byte{0, 0, 0, 1, 0x61, 1, 0, 0, 0, 1, 0x61, 2}
	for _, parameters := range []string{"", "packetization-mode=0", "profile-level-id=4d0028; packetization-mode=1"} {
		t.Run(parameters, func(t *testing.T) {
			codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: parameters}
			track, err := newVideoTrack(codec, "video", "source")
			if err != nil {
				t.Fatal(err)
			}
			capture := &packetCapture{codec: codec}
			if _, err := track.Bind(capture); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := track.Unbind(capture); err != nil {
					t.Error(err)
				}
			}()
			for range 2 {
				if err := track.WriteSample(media.Sample{Data: frame, Duration: time.Second / 30}); err != nil {
					t.Fatal(err)
				}
			}
			perFrame := 2
			if nonInterleavedH264(parameters) {
				perFrame = 1
			}
			if len(capture.packets) != 2*perFrame {
				t.Fatalf("got %d packets, want %d", len(capture.packets), 2*perFrame)
			}
			for index := range 2 {
				packets := capture.packets[index*perFrame : (index+1)*perFrame]
				var payloads [][]byte
				for i, packet := range packets {
					if packet.Timestamp != packets[0].Timestamp || packet.Marker != (i == len(packets)-1) {
						t.Fatal("frame timestamp or last-packet marker changed")
					}
					if packet.SSRC != 123 || packet.PayloadType != 96 {
						t.Fatal("negotiated RTP header changed")
					}
					payloads = append(payloads, packet.Payload)
				}
				if !bytes.Equal(decodeH264Payloads(t, payloads, 1188), frame) {
					t.Fatal("track changed frame bytes")
				}
			}
			// Pion truncates the nanosecond duration on the 90 kHz clock.
			if step := capture.packets[perFrame].Timestamp - capture.packets[0].Timestamp; step < 2999 || step > 3000 {
				t.Fatalf("unexpected frame clock step: %d", step)
			}
		})
	}
}
