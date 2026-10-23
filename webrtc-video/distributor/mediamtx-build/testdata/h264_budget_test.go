package main

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
)

const sampleMaxNALUs = 1024

func accessUnit(count, size int) [][]byte {
	nalus := make([][]byte, count)
	for i := range nalus {
		nalus[i] = bytes.Repeat([]byte{0x88}, size)
		nalus[i][0] = 0x65
	}
	return nalus
}

func decodeRTP(decoder *rtph264.Decoder, nalus [][]byte, mode string, timestamp uint32) ([][]byte, error) {
	var sequence uint16
	var result [][]byte
	feed := func(payload []byte, marker bool) error {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: sequence, Timestamp: timestamp, Marker: marker}, Payload: payload}
		sequence++
		decoded, err := decoder.Decode(packet)
		if errors.Is(err, rtph264.ErrMorePacketsNeeded) {
			return nil
		}
		if err == nil {
			result = decoded
		}
		return err
	}
	for i := 0; i < len(nalus); i++ {
		nalu := nalus[i]
		switch mode {
		case "stap-a":
			packet := []byte{0x78}
			last := min(i+64, len(nalus))
			for ; i < last; i++ {
				packet = append(packet, byte(len(nalus[i])>>8), byte(len(nalus[i])))
				packet = append(packet, nalus[i]...)
			}
			i--
			if err := feed(packet, i == len(nalus)-1); err != nil {
				return nil, err
			}
		case "fu-a":
			for position := 1; position < len(nalu); {
				end := min(position+1024, len(nalu))
				if position == 1 && end == len(nalu) {
					end--
				}
				header := nalu[0] & 0x1f
				if position == 1 {
					header |= 0x80
				}
				if end == len(nalu) {
					header |= 0x40
				}
				packet := append([]byte{nalu[0]&0xe0 | 28, header}, nalu[position:end]...)
				if err := feed(packet, end == len(nalu) && i == len(nalus)-1); err != nil {
					return nil, err
				}
				position = end
			}
		default:
			if err := feed(nalu, i == len(nalus)-1); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func TestH264NALCountAndRecovery(t *testing.T) {
	for _, mode := range []string{"single", "stap-a", "fu-a"} {
		for _, count := range []int{50, 51, 56, sampleMaxNALUs, sampleMaxNALUs + 1} {
			t.Run(fmt.Sprintf("%s/%d", mode, count), func(t *testing.T) {
				decoder := &rtph264.Decoder{PacketizationMode: 1}
				if err := decoder.Init(); err != nil {
					t.Fatal(err)
				}
				nalus := accessUnit(count, 3)
				decoded, err := decodeRTP(decoder, nalus, mode, 1)
				if count <= sampleMaxNALUs {
					if err != nil || !reflect.DeepEqual(decoded, nalus) {
						t.Fatalf("decoded %d/%d NALs: %v", len(decoded), count, err)
					}
				} else if err == nil {
					t.Fatal("excessive NAL count accepted")
				}
				next := accessUnit(1, 3)
				decoded, err = decodeRTP(decoder, next, mode, 2)
				if err != nil || !reflect.DeepEqual(decoded, next) {
					t.Fatalf("next access unit did not recover: %v", err)
				}
			})
		}
	}
}

func TestH264ConversionsAndByteLimit(t *testing.T) {
	for _, format := range []string{"annex-b", "avcc"} {
		for _, item := range []struct{ count, size int }{{56, 3}, {sampleMaxNALUs, 3}, {sampleMaxNALUs + 1, 3}, {1, 8 * 1024 * 1024}, {1, 8*1024*1024 + 1}} {
			t.Run(fmt.Sprintf("%s/%d/%d", format, item.count, item.size), func(t *testing.T) {
				nalus := accessUnit(item.count, item.size)
				var decoded [][]byte
				var err error
				if format == "annex-b" {
					data, marshalErr := h264.AnnexB(nalus).Marshal()
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					var result h264.AnnexB
					err = result.Unmarshal(data)
					decoded = result
				} else {
					data, marshalErr := h264.AVCC(nalus).Marshal()
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					var result h264.AVCC
					err = result.Unmarshal(data)
					decoded = result
				}
				excessive := item.count > sampleMaxNALUs || item.count*item.size > 8*1024*1024
				if excessive && err == nil {
					t.Fatal("resource limit was not enforced")
				}
				if !excessive && (err != nil || !reflect.DeepEqual(decoded, nalus)) {
					t.Fatalf("round trip failed: %v", err)
				}
			})
		}
	}
	for _, size := range []int{8 * 1024 * 1024, 8*1024*1024 + 1} {
		t.Run(fmt.Sprintf("fragmented/%d", size), func(t *testing.T) {
			decoder := &rtph264.Decoder{PacketizationMode: 1}
			if err := decoder.Init(); err != nil {
				t.Fatal(err)
			}
			nalus := accessUnit(1, size)
			decoded, err := decodeRTP(decoder, nalus, "fu-a", 1)
			if size == 8*1024*1024 && (err != nil || !reflect.DeepEqual(decoded, nalus)) {
				t.Fatalf("bounded fragmented access unit failed: %v", err)
			}
			if size > 8*1024*1024 && err == nil {
				t.Fatal("oversized fragmented access unit accepted")
			}
			next := accessUnit(1, 3)
			decoded, err = decodeRTP(decoder, next, "fu-a", 2)
			if err != nil || !reflect.DeepEqual(decoded, next) {
				t.Fatalf("fragmented access unit did not recover: %v", err)
			}
		})
	}
}
