package bridge

import "github.com/pion/rtp"

type rtpSequenceRewriter struct {
	initialized bool
	next        uint16
}

func (r *rtpSequenceRewriter) rewrite(packet *rtp.Packet) {
	if !r.initialized {
		r.initialized = true
		r.next = packet.SequenceNumber
	}
	packet.SequenceNumber = r.next
	r.next++
}
