package webrtc

import (
	"encoding/binary"
	"strings"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

// newVideoTrack keeps Pion's sample timing and RTP handling, while reducing
// packet overhead for H264 encoders that emit many small slices per frame.
func newVideoTrack(codec webrtc.RTPCodecCapability, id, stream string) (*webrtc.TrackLocalStaticSample, error) {
	if !strings.EqualFold(codec.MimeType, webrtc.MimeTypeH264) {
		return webrtc.NewTrackLocalStaticSample(codec, id, stream)
	}
	return webrtc.NewTrackLocalStaticSample(codec, id, stream, webrtc.WithPayloader(
		func(negotiated webrtc.RTPCodecCapability) (rtp.Payloader, error) {
			return &h264Payloader{aggregate: nonInterleavedH264(negotiated.SDPFmtpLine)}, nil
		},
	))
}

func nonInterleavedH264(fmtp string) bool {
	for _, parameter := range strings.Split(fmtp, ";") {
		name, value, ok := strings.Cut(parameter, "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "packetization-mode") {
			return strings.TrimSpace(value) == "1"
		}
	}
	return false
}

type h264Payloader struct {
	codec     codecs.H264Payloader
	aggregate bool
}

func (p *h264Payloader) Payload(mtu uint16, frame []byte) [][]byte {
	payloads := p.codec.Payload(mtu, frame)
	if !p.aggregate {
		return payloads
	}
	return aggregateH264Payloads(payloads, int(mtu))
}

// aggregateH264Payloads combines consecutive single NAL units from one access
// unit into STAP-A packets (RFC 6184 section 5.7.1). It never waits for another
// frame, changes decoding order, or nests existing STAP-A/FU-A packets. Pion
// still owns Annex B parsing, parameter sets and fragmentation of large NALs.
// The caller must use non-interleaved packetization mode 1.
func aggregateH264Payloads(payloads [][]byte, mtu int) [][]byte {
	single := func(payload []byte) bool {
		return len(payload) > 0 && payload[0]&0x1f > 0 && payload[0]&0x1f < 24
	}
	result := make([][]byte, 0, len(payloads))
	for i := 0; i < len(payloads); {
		end, size := i, 1
		var forbidden, nri byte
		for end < len(payloads) && single(payloads[end]) && size+2+len(payloads[end]) <= mtu {
			payload := payloads[end]
			forbidden |= payload[0] & 0x80
			nri = max(nri, payload[0]&0x60)
			size += 2 + len(payload)
			end++
		}
		if end-i < 2 {
			result = append(result, payloads[i])
			i++
			continue
		}
		packet := make([]byte, 1, size)
		packet[0] = forbidden | nri | 24
		for _, payload := range payloads[i:end] {
			packet = binary.BigEndian.AppendUint16(packet, uint16(len(payload)))
			packet = append(packet, payload...)
		}
		result = append(result, packet)
		i = end
	}
	return result
}
