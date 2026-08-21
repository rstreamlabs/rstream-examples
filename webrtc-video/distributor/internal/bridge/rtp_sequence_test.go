package bridge

import (
	"testing"

	"github.com/pion/rtp"
)

func TestRTPSequenceRewriterClosesIntentionalGapsAndWraps(t *testing.T) {
	rewriter := rtpSequenceRewriter{}
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 65534}},
		{Header: rtp.Header{SequenceNumber: 12}},
		{Header: rtp.Header{SequenceNumber: 42}},
	}
	for index, want := range []uint16{65534, 65535, 0} {
		rewriter.rewrite(packets[index])
		if packets[index].SequenceNumber != want {
			t.Fatalf("packet %d sequence = %d, want %d", index, packets[index].SequenceNumber, want)
		}
	}
}
