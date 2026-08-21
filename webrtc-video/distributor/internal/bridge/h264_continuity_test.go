package bridge

import (
	"testing"

	"github.com/pion/rtp"
)

func TestH264ContinuityFilterDropsTheDamagedAccessUnitAfterASequenceGap(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	packets := []*rtp.Packet{
		h264Packet(10, 100, []byte{0x7c, 0x85}),
		h264Packet(11, 100, []byte{0x7c, 0x05}),
		h264Packet(13, 100, []byte{0x7c, 0x45}),
		h264Packet(14, 200, []byte{0x7c, 0x81}),
		h264Packet(15, 200, []byte{0x7c, 0x01}),
	}
	wantForward := []bool{true, true, false, true, true}
	wantDamaged := []bool{false, false, true, false, false}
	for index, packet := range packets {
		forward, damaged := filter.accept(packet)
		if forward != wantForward[index] || damaged != wantDamaged[index] {
			t.Fatalf("packet %d decision = (%t, %t), want (%t, %t)", index, forward, damaged, wantForward[index], wantDamaged[index])
		}
	}
}

func TestH264ContinuityFilterAcceptsACompleteNewAccessUnitAfterASequenceGap(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	for index, packet := range []*rtp.Packet{
		h264Packet(20, 100, []byte{0x65}),
		h264Packet(22, 200, []byte{0x7c, 0x81}),
	} {
		forward, damaged := filter.accept(packet)
		if !forward || damaged {
			t.Fatalf("packet %d decision = (%t, %t), want a complete access unit", index, forward, damaged)
		}
	}
}

func TestH264ContinuityFilterWaitsForAValidStartAfterAnOrphanedFragment(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	packets := []*rtp.Packet{
		h264Packet(30, 100, []byte{0x65}),
		h264Packet(32, 200, []byte{0x7c, 0x01}),
		h264Packet(33, 300, []byte{0x7c, 0x01}),
		h264Packet(34, 400, []byte{0x41}),
	}
	wantForward := []bool{true, false, false, true}
	wantDamaged := []bool{false, true, true, false}
	for index, packet := range packets {
		forward, damaged := filter.accept(packet)
		if forward != wantForward[index] || damaged != wantDamaged[index] {
			t.Fatalf("packet %d decision = (%t, %t), want (%t, %t)", index, forward, damaged, wantForward[index], wantDamaged[index])
		}
	}
}

func TestH264ContinuityFilterHandlesSequenceWrapAndDisabledMode(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	for _, packet := range []*rtp.Packet{
		h264Packet(65535, 100, []byte{0x65}),
		h264Packet(0, 100, []byte{0x41}),
	} {
		if forward, damaged := filter.accept(packet); !forward || damaged {
			t.Fatalf("wraparound decision = (%t, %t)", forward, damaged)
		}
	}
	disabled := newH264ContinuityFilter(false)
	if forward, damaged := disabled.accept(h264Packet(2, 100, nil)); !forward || damaged {
		t.Fatalf("disabled decision = (%t, %t)", forward, damaged)
	}
}

func h264Packet(sequence uint16, timestamp uint32, payload []byte) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: sequence, Timestamp: timestamp}, Payload: payload}
}
