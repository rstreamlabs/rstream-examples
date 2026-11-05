package webrtc

import (
	"bytes"
	"testing"

	"github.com/pion/rtp/codecs"
)

func decodeH264Payloads(t *testing.T, payloads [][]byte, mtu int) []byte {
	t.Helper()
	decoder := new(codecs.H264Packet)
	var decoded []byte
	for _, payload := range payloads {
		if len(payload) > mtu {
			t.Fatalf("RTP payload has %d bytes, exceeds %d", len(payload), mtu)
		}
		part, err := decoder.Unmarshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, part...)
	}
	return decoded
}

func TestH264SmallSlicesUseBoundedAggregation(t *testing.T) {
	var accessUnit []byte
	for i := range 104 {
		accessUnit = append(accessUnit, 0, 0, 0, 1, 0x41)
		accessUnit = append(accessUnit, bytes.Repeat([]byte{byte(i + 1)}, 39)...)
	}
	const mtu = 1188
	payloads := new(codecs.H264Payloader).Payload(mtu, accessUnit)
	if len(payloads) != 104 {
		t.Fatalf("unexpected baseline packetization: %d", len(payloads))
	}
	aggregated := aggregateH264Payloads(payloads, mtu)
	if len(aggregated) != 4 {
		t.Fatalf("small slices need four MTU-bounded packets, got %d", len(aggregated))
	}
	if got := decodeH264Payloads(t, aggregated, mtu); !bytes.Equal(got, accessUnit) {
		t.Fatal("aggregation changed NAL contents or decoding order")
	}
}

func TestH264AggregationHonorsBoundaryAndHeader(t *testing.T) {
	// F is the union of the forbidden bits; NRI is the highest contained NRI.
	payloads := [][]byte{{0x21, 1, 2}, {0xc1, 3, 4}}
	for _, mtu := range []int{10, 11} {
		result := aggregateH264Payloads(payloads, mtu)
		want := 2
		if mtu == 11 {
			want = 1
			if result[0][0] != 0xd8 {
				t.Fatalf("incorrect aggregation F/NRI header: %02x", result[0][0])
			}
		}
		if len(result) != want {
			t.Fatalf("MTU %d produced %d packets, want %d", mtu, len(result), want)
		}
		if !bytes.Equal(decodeH264Payloads(t, result, mtu), decodeH264Payloads(t, payloads, mtu)) {
			t.Fatal("NAL headers or bytes changed")
		}
	}
	if !bytes.Equal(payloads[0], []byte{0x21, 1, 2}) || !bytes.Equal(payloads[1], []byte{0xc1, 3, 4}) {
		t.Fatal("aggregation mutated input buffers")
	}
}

func TestH264AggregationPreservesFragmentsAndParameterSets(t *testing.T) {
	accessUnit := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x68, 3, 4}
	for _, size := range []int{10, 10, 4000, 10, 10} {
		accessUnit = append(accessUnit, 0, 0, 0, 1, 0x65)
		accessUnit = append(accessUnit, bytes.Repeat([]byte{0x55}, size)...)
	}
	const mtu = 100
	payloads := new(codecs.H264Payloader).Payload(mtu, accessUnit)
	got := aggregateH264Payloads(payloads, mtu)
	if !bytes.Equal(decodeH264Payloads(t, got, mtu), accessUnit) {
		t.Fatal("fragmentation or parameter sets changed")
	}
	// Existing STAP-A and FU-A packets must not be nested in a new STAP-A.
	var originalSpecial, aggregatedSpecial [][]byte
	for _, p := range payloads {
		if p[0]&0x1f >= 24 {
			originalSpecial = append(originalSpecial, p)
		}
	}
	for _, p := range got {
		if p[0]&0x1f == 28 || bytes.Equal(p, payloads[0]) {
			aggregatedSpecial = append(aggregatedSpecial, p)
		}
	}
	if len(originalSpecial) != len(aggregatedSpecial) {
		t.Fatal("fragmented or aggregated packet count changed")
	}
	for i := range originalSpecial {
		if !bytes.Equal(originalSpecial[i], aggregatedSpecial[i]) {
			t.Fatal("existing STAP-A or FU-A changed")
		}
	}
}

func TestH264AggregationHasNoCrossFrameBuffer(t *testing.T) {
	for _, payload := range [][]byte{{0x61, 1}, {0x61, 2}} {
		result := aggregateH264Payloads([][]byte{payload}, 1188)
		if len(result) != 1 || !bytes.Equal(result[0], payload) {
			t.Fatal("single NAL was delayed or changed")
		}
	}
	if len(aggregateH264Payloads(nil, 1188)) != 0 {
		t.Fatal("empty input generated a packet")
	}
}

func FuzzH264AggregationRoundTrip(f *testing.F) {
	f.Add(uint16(1188), uint8(104), uint16(40))
	f.Add(uint16(100), uint8(5), uint16(4000))
	f.Add(uint16(11), uint8(2), uint16(3))
	f.Fuzz(func(t *testing.T, mtu uint16, count uint8, size uint16) {
		mtu = 8 + mtu%2040
		var input []byte
		for i := range int(count%128) + 1 {
			input = append(input, 0, 0, 0, 1, 0x61)
			input = append(input, bytes.Repeat([]byte{byte(i%127 + 1)}, int(size%4096))...)
		}
		payloads := new(codecs.H264Payloader).Payload(mtu, input)
		result := aggregateH264Payloads(payloads, int(mtu))
		if !bytes.Equal(decodeH264Payloads(t, result, int(mtu)), input) {
			t.Fatal("aggregation changed the encoded access unit")
		}
	})
}
