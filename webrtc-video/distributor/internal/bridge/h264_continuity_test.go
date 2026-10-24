package bridge

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func h264Packet(sequence uint16, timestamp uint32, marker bool, payload ...byte) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: sequence, Timestamp: timestamp, Marker: marker}, Payload: payload}
}

func TestH264ContinuityEmitsOnlyCompleteAccessUnits(t *testing.T) {
	f := newH264ContinuityFilter(true)
	now := time.Now()
	packets := []*rtp.Packet{
		h264Packet(65534, 100, false, 0x78, 0, 1, 0x67, 0, 1, 0x68),
		h264Packet(65535, 100, false, 0x65, 0x80),
		h264Packet(0, 100, true, 0x65, 0x40),
	}
	for i, packet := range packets {
		result := f.accept(packet, now)
		if result.droppedFrames != 0 || result.droppedPackets != 0 {
			t.Fatalf("unexpected drop: %+v", result)
		}
		if i != len(packets)-1 && len(result.packets) != 0 {
			t.Fatal("partial access unit escaped before marker")
		}
		if i == len(packets)-1 && !reflect.DeepEqual(result.packets, packets) {
			t.Fatal("complete access unit was changed or lost")
		}
	}
	delta := h264Packet(1, 200, true, 0x41, 0x80)
	if result := f.accept(delta, now); len(result.packets) != 1 || result.packets[0] != delta {
		t.Fatal("complete delta frame lost")
	}
	if len(f.packets) != 0 || f.size != 0 {
		t.Fatal("completed frame retained")
	}
}

func TestH264ContinuityDoesNotLeakPartialPicturesAfterLoss(t *testing.T) {
	for _, keyFrame := range []bool{false, true} {
		t.Run(map[bool]string{false: "delta", true: "IDR"}[keyFrame], func(t *testing.T) {
			f := newH264ContinuityFilter(true)
			now := time.Now()
			f.accept(h264Packet(10, 100, true, 0x65, 0x80), now)
			nalu := byte(0x41)
			if keyFrame {
				nalu = 0x65
			}
			packets := []*rtp.Packet{
				h264Packet(11, 200, false, nalu, 0x80),
				h264Packet(13, 200, true, nalu, 0x40), // A whole slice was lost.
				h264Packet(14, 300, true, 0x41, 0x80),
			}
			var dropped uint64
			for _, packet := range packets {
				result := f.accept(packet, now)
				if len(result.packets) != 0 {
					t.Fatal("damaged image or dependent delta escaped")
				}
				dropped += result.droppedPackets
			}
			if dropped != 3 {
				t.Fatalf("dropped packets = %d, want 3", dropped)
			}
			recovery := h264Packet(15, 400, true, 0x65, 0x80)
			if result := f.accept(recovery, now); len(result.packets) != 1 {
				t.Fatal("complete IDR did not recover")
			}
		})
	}
}

func TestH264ContinuityDoesNotRecoverFromLaterIDRSlice(t *testing.T) {
	f := newH264ContinuityFilter(true)
	now := time.Now()
	f.accept(h264Packet(10, 100, true, 0x65, 0x80), now)
	result := f.accept(h264Packet(12, 200, true, 0x65, 0x40), now)
	if len(result.packets) != 0 || result.droppedFrames != 1 {
		t.Fatal("later IDR slice accepted as recovery")
	}
}

func TestH264ContinuityDropsFrameWithMissingMarker(t *testing.T) {
	f := newH264ContinuityFilter(true)
	now := time.Now()
	f.accept(h264Packet(10, 100, false, 0x65, 0x80), now)
	recovery := h264Packet(11, 200, true, 0x65, 0x80)
	result := f.accept(recovery, now)
	if result.droppedFrames != 1 || result.droppedPackets != 1 || len(result.packets) != 1 || result.packets[0] != recovery {
		t.Fatalf("result = %+v", result)
	}
}

func TestH264ContinuityBoundsAssembly(t *testing.T) {
	for _, limit := range []string{"bytes", "packets", "age"} {
		t.Run(limit, func(t *testing.T) {
			f := newH264ContinuityFilter(true)
			now := time.Now()
			f.accept(h264Packet(10, 100, false, 0x65, 0x80), now)
			switch limit {
			case "bytes":
				f.size = maximumH264AccessUnitBytes
			case "packets":
				f.packets = make([]*rtp.Packet, maximumH264AccessUnitPackets)
			case "age":
				now = now.Add(maximumH264AssemblyTime + time.Nanosecond)
			}
			result := f.accept(h264Packet(11, 100, true, 0x65, 0x40), now)
			if len(result.packets) != 0 || result.droppedFrames != 1 || len(f.packets) != 0 || f.size != 0 {
				t.Fatalf("limit did not release assembly: %+v", result)
			}
			if result := f.accept(h264Packet(12, 200, true, 0x65, 0x80), now); len(result.packets) != 1 {
				t.Fatal("limit prevented subsequent recovery")
			}
		})
	}
	f := newH264ContinuityFilter(true)
	result := f.accept(h264Packet(1, 1, true, bytes.Repeat([]byte{0x80}, maximumH264AccessUnitBytes+1)...), time.Now())
	if result.droppedFrames != 1 || len(f.packets) != 0 {
		t.Fatal("oversized first packet was retained")
	}
}

func TestH264KeyFrameStartsRequireTheFirstSlice(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		want    bool
	}{
		{"IDR first slice", []byte{0x65, 0x80}, true},
		{"IDR later slice", []byte{0x65, 0x40}, false},
		{"delta", []byte{0x41, 0x80}, false},
		{"first IDR fragment", []byte{0x7c, 0x85, 0x80}, true},
		{"later IDR fragment", []byte{0x7c, 0x85, 0x40}, false},
		{"fragment continuation", []byte{0x7c, 0x05, 0x80}, false},
		{"aggregation", []byte{0x78, 0, 1, 0x67, 0, 2, 0x65, 0x80}, true},
		{"later slice aggregation", []byte{0x78, 0, 2, 0x65, 0x40}, false},
		{"truncated aggregation", []byte{0x78, 0, 2, 0x65, 0x80, 0}, false},
		{"empty NALU", []byte{0x78, 0, 0}, false},
		{"short slice", []byte{0x65}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := startsH264KeyFrame(test.payload); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}

func TestH264ContinuityDisabledPassesPacketsThrough(t *testing.T) {
	packet := h264Packet(2, 100, false, 0x90)
	result := newH264ContinuityFilter(false).accept(packet, time.Now())
	if len(result.packets) != 1 || result.packets[0] != packet {
		t.Fatal("disabled filter retained packet")
	}
}

func TestH264ContinuityRetainsStandaloneParameterSetsDuringRecovery(t *testing.T) {
	f := newH264ContinuityFilter(true)
	now := time.Now()
	parameters := h264Packet(1, 100, true, 0x78, 0, 1, 0x67, 0, 1, 0x68)
	if result := f.accept(parameters, now); len(result.packets) != 1 || !f.needsKeyFrame {
		t.Fatal("parameter sets were lost or mistaken for a recovery picture")
	}
	if result := f.accept(h264Packet(2, 200, true, 0x41, 0x80), now); len(result.packets) != 0 {
		t.Fatal("parameter sets allowed a delta before recovery")
	}
	if result := f.accept(h264Packet(3, 300, true, 0x65, 0x80), now); len(result.packets) != 1 {
		t.Fatal("IDR recovery lost")
	}
}
