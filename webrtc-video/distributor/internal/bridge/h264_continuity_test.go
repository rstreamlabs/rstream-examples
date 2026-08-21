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
		h264Packet(16, 300, h264ParameterSets()),
		h264Packet(17, 300, []byte{0x7c, 0x85}),
		h264Packet(18, 300, []byte{0x7c, 0x05}),
	}
	wantForward := []bool{true, true, false, false, false, true, true, true}
	wantDamaged := []bool{false, false, true, true, false, false, false, false}
	for index, packet := range packets {
		forward, damaged := filter.accept(packet)
		if forward != wantForward[index] || damaged != wantDamaged[index] {
			t.Fatalf("packet %d decision = (%t, %t), want (%t, %t)", index, forward, damaged, wantForward[index], wantDamaged[index])
		}
	}
}

func TestH264ContinuityFilterAcceptsACompleteKeyFrameAfterASequenceGap(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	for index, packet := range []*rtp.Packet{
		h264Packet(20, 100, []byte{0x65}),
		h264Packet(22, 200, []byte{0x7c, 0x85}),
	} {
		forward, damaged := filter.accept(packet)
		if !forward || damaged {
			t.Fatalf("packet %d decision = (%t, %t), want a complete key frame", index, forward, damaged)
		}
	}
}

func TestH264ContinuityFilterWaitsForAValidStartAfterAnOrphanedFragment(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	packets := []*rtp.Packet{
		h264Packet(30, 100, []byte{0x65}),
		h264Packet(32, 200, []byte{0x7c, 0x01}),
		h264Packet(33, 300, []byte{0x7c, 0x01}),
		h264Packet(34, 400, []byte{0x65}),
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

func TestH264ContinuityFilterRejectsMalformedAndNonIDRRecoveryPackets(t *testing.T) {
	filter := newH264ContinuityFilter(true)
	packets := []*rtp.Packet{
		h264Packet(40, 100, []byte{0x65}),
		h264Packet(42, 200, []byte{0x78, 0x00, 0x02, 0x67}),
		h264Packet(43, 300, []byte{0x41}),
		h264Packet(44, 400, []byte{0x78, 0x00, 0x01, 0x65}),
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

func TestH264PacketClassifiersCoverSingleNALUFragmentationAndAggregation(t *testing.T) {
	tests := []struct {
		name          string
		payload       []byte
		keyFrame      bool
		parameterSets bool
	}{
		{name: "single IDR", payload: []byte{0x65}, keyFrame: true},
		{name: "single delta", payload: []byte{0x41}},
		{name: "IDR fragment start", payload: []byte{0x7c, 0x85}, keyFrame: true},
		{name: "IDR fragment continuation", payload: []byte{0x7c, 0x05}},
		{name: "parameter aggregation", payload: h264ParameterSets(), parameterSets: true},
		{name: "IDR aggregation", payload: []byte{0x78, 0x00, 0x01, 0x67, 0x00, 0x01, 0x65}, keyFrame: true},
		{name: "mixed aggregation", payload: []byte{0x78, 0x00, 0x01, 0x67, 0x00, 0x01, 0x41}},
		{name: "truncated aggregation", payload: []byte{0x78, 0x00, 0x02, 0x67}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := startsH264KeyFrame(test.payload); got != test.keyFrame {
				t.Fatalf("key-frame classification = %t, want %t", got, test.keyFrame)
			}
			if got := isH264ParameterSetPacket(test.payload); got != test.parameterSets {
				t.Fatalf("parameter-set classification = %t, want %t", got, test.parameterSets)
			}
		})
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

func h264ParameterSets() []byte {
	return []byte{0x78, 0x00, 0x01, 0x67, 0x00, 0x01, 0x68}
}
