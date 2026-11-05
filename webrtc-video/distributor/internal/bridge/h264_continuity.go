package bridge

import (
	"encoding/binary"
	"time"

	"github.com/pion/rtp"
)

const (
	maximumH264AccessUnitBytes   = 8 << 20
	maximumH264AccessUnitPackets = 8192
	maximumH264AssemblyTime      = 500 * time.Millisecond
)

// Repair precedes this gate. An access unit is released only at its RTP marker:
// forwarding its prefix would let a downstream recorder accept an incomplete
// picture when a later slice is lost. At most one access unit is retained; no
// decoder, extra worker, retransmission queue or transcoding is involved.
type h264ContinuityFilter struct {
	enabled             bool
	sequenceInitialized bool
	lastSequence        uint16
	timestamp           uint32
	started             time.Time
	packets             []*rtp.Packet
	size                int
	damaged             bool
	needsKeyFrame       bool
	keyFrame            bool
	parametersOnly      bool
}

type h264ContinuityResult struct {
	packets        []*rtp.Packet
	droppedPackets uint64
	droppedFrames  uint64
}

func newH264ContinuityFilter(enabled bool) *h264ContinuityFilter {
	return &h264ContinuityFilter{enabled: enabled, needsKeyFrame: true}
}

func (f *h264ContinuityFilter) accept(packet *rtp.Packet, now time.Time) h264ContinuityResult {
	if !f.enabled {
		return h264ContinuityResult{packets: []*rtp.Packet{packet}}
	}
	result := h264ContinuityResult{}
	discontinuity := f.sequenceInitialized && packet.SequenceNumber != f.lastSequence+1
	newFrame := !f.sequenceInitialized || packet.Timestamp != f.timestamp
	f.sequenceInitialized = true
	f.lastSequence = packet.SequenceNumber
	if newFrame {
		if len(f.packets) != 0 {
			result.droppedFrames++
			result.droppedPackets += uint64(len(f.packets))
			f.needsKeyFrame = true
		}
		f.packets, f.size = nil, 0
		f.timestamp, f.started = packet.Timestamp, now
		f.damaged, f.keyFrame = false, false
		f.parametersOnly = true
	}
	if discontinuity {
		f.needsKeyFrame = true
	}
	packetBytes := packet.MarshalSize()
	if !f.damaged && ((!newFrame && discontinuity) ||
		len(f.packets) >= maximumH264AccessUnitPackets ||
		packetBytes > maximumH264AccessUnitBytes-f.size ||
		now.Sub(f.started) > maximumH264AssemblyTime) {
		result.droppedFrames++
		result.droppedPackets += uint64(len(f.packets))
		f.packets, f.size = nil, 0
		f.damaged, f.needsKeyFrame = true, true
	}
	if f.damaged {
		result.droppedPackets++
		return result
	}
	f.keyFrame = f.keyFrame || startsH264KeyFrame(packet.Payload)
	f.parametersOnly = f.parametersOnly && isH264ParameterSetPacket(packet.Payload)
	f.packets = append(f.packets, packet)
	f.size += packetBytes
	if !packet.Marker {
		return result
	}
	if f.needsKeyFrame && !f.keyFrame && !f.parametersOnly {
		result.droppedPackets += uint64(len(f.packets))
		result.droppedFrames++
	} else {
		result.packets = f.packets
		if f.keyFrame {
			f.needsKeyFrame = false
		}
	}
	f.packets, f.size = nil, 0
	return result
}

// An IDR NALU can be one of many slices. first_mb_in_slice is the first
// Exp-Golomb field: a leading one means zero. A later slice, or the start of
// its FU-A fragmentation, must not be mistaken for a complete recovery point.
func startsH264KeyFrame(payload []byte) bool {
	if len(payload) < 2 {
		return false
	}
	switch payload[0] & 0x1f {
	case 5:
		return payload[1]&0x80 != 0
	case 24:
		found := false
		valid := walkH264STAPA(payload, func(nalu []byte) bool {
			found = found || len(nalu) > 1 && nalu[0]&0x1f == 5 && nalu[1]&0x80 != 0
			return true
		})
		return valid && found
	case 28:
		return len(payload) >= 3 && payload[1]&0x80 != 0 && payload[1]&0x1f == 5 && payload[2]&0x80 != 0
	default:
		return false
	}
}

func isH264ParameterSetPacket(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	parameter := func(nalu []byte) bool { return nalu[0]&0x1f == 7 || nalu[0]&0x1f == 8 }
	if payload[0]&0x1f == 24 {
		return walkH264STAPA(payload, parameter)
	}
	return parameter(payload)
}

func walkH264STAPA(payload []byte, visit func([]byte) bool) bool {
	if len(payload) < 4 {
		return false
	}
	for offset := 1; offset < len(payload); {
		if len(payload)-offset < 2 {
			return false
		}
		size := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if size == 0 || size > len(payload)-offset || !visit(payload[offset:offset+size]) {
			return false
		}
		offset += size
	}
	return true
}
