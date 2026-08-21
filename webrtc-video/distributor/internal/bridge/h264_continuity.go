package bridge

import "github.com/pion/rtp"

type h264ContinuityFilter struct {
	enabled               bool
	sequenceInitialized   bool
	lastSequence          uint16
	forwardedTimestamp    uint32
	hasForwardedTimestamp bool
	dropping              bool
	damagedTimestamp      uint32
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
		if packet.Timestamp == f.damagedTimestamp || !startsH264AccessUnit(packet.Payload) {
			newDamagedFrame := packet.Timestamp != f.damagedTimestamp
			f.damagedTimestamp = packet.Timestamp
			return false, newDamagedFrame
		}
		f.dropping = false
	}
	if discontinuity && ((!f.hasForwardedTimestamp || packet.Timestamp == f.forwardedTimestamp) || !startsH264AccessUnit(packet.Payload)) {
		f.dropping = true
		f.damagedTimestamp = packet.Timestamp
		return false, true
	}
	f.forwardedTimestamp = packet.Timestamp
	f.hasForwardedTimestamp = true
	return true, false
}

func startsH264AccessUnit(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	naluType := payload[0] & 0x1f
	if naluType >= 1 && naluType <= 24 {
		return true
	}
	return naluType == 28 && len(payload) >= 2 && payload[1]&0x80 != 0
}
