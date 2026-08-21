package bridge

import (
	"encoding/binary"

	"github.com/pion/rtp"
)

type h264ContinuityFilter struct {
	enabled             bool
	sequenceInitialized bool
	lastSequence        uint16
	dropping            bool
	damagedTimestamp    uint32
}

func newH264ContinuityFilter(enabled bool) *h264ContinuityFilter {
	return &h264ContinuityFilter{enabled: enabled}
}

func (f *h264ContinuityFilter) accept(packet *rtp.Packet) (bool, bool) {
	if !f.enabled {
		return true, false
	}
	discontinuity := false
	if f.sequenceInitialized {
		discontinuity = packet.SequenceNumber != f.lastSequence+1
	}
	f.sequenceInitialized = true
	f.lastSequence = packet.SequenceNumber
	if f.dropping {
		if isH264ParameterSetPacket(packet.Payload) {
			return true, false
		}
		if !startsH264KeyFrame(packet.Payload) {
			newDamagedFrame := packet.Timestamp != f.damagedTimestamp
			f.damagedTimestamp = packet.Timestamp
			return false, newDamagedFrame
		}
		f.dropping = false
	}
	if discontinuity && !startsH264KeyFrame(packet.Payload) {
		f.dropping = true
		f.damagedTimestamp = packet.Timestamp
		if isH264ParameterSetPacket(packet.Payload) {
			return true, false
		}
		return false, true
	}
	return true, false
}

func startsH264KeyFrame(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	naluType := payload[0] & 0x1f
	switch naluType {
	case 5:
		return true
	case 24:
		found := false
		valid := walkH264STAPA(payload, func(value byte) bool {
			found = found || value&0x1f == 5
			return true
		})
		return valid && found
	case 28:
		return len(payload) >= 2 && payload[1]&0x80 != 0 && payload[1]&0x1f == 5
	default:
		return false
	}
}

func isH264ParameterSetPacket(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	naluType := payload[0] & 0x1f
	if naluType == 7 || naluType == 8 {
		return true
	}
	if naluType != 24 {
		return false
	}
	return walkH264STAPA(payload, func(value byte) bool {
		typeID := value & 0x1f
		return typeID == 7 || typeID == 8
	})
}

func walkH264STAPA(payload []byte, visit func(byte) bool) bool {
	if len(payload) < 4 || visit == nil {
		return false
	}
	seen := false
	for offset := 1; offset < len(payload); {
		if len(payload)-offset < 2 {
			return false
		}
		size := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if size == 0 || size > len(payload)-offset {
			return false
		}
		if !visit(payload[offset]) {
			return false
		}
		seen = true
		offset += size
	}
	return seen
}
