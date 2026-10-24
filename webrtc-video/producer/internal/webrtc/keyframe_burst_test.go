package webrtc

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

// A key picture may exceed a short queue budget even though the complete GOP
// fits the link. Exercise actual packet pacing, including the dependent frames,
// rather than accepting an IDR and silently losing the rest of its GOP.
func TestTokenBucketPacerDrainsKeyFrameBursts(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		keyBytes, bitrate, frames int
		period                    time.Duration
	}{
		{"visible", 98502, 1279318, 30, time.Second},
		{"high", 143650, 3120502, 30, time.Second},
		{"thermal", 28530, 600934, 15, 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := newTokenBucketPacer(tc.bitrate, 1.5, 1024)
				defer p.Close()
				delivered := 0
				type sentPacket struct {
					at    time.Time
					bytes int
				}
				var sent []sentPacket
				p.AddStream(10, interceptor.RTPWriterFunc(func(h *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
					if h.Marker {
						delivered++
					}
					n := h.MarshalSize() + len(payload)
					sent = append(sent, sentPacket{time.Now(), n})
					return n, nil
				}))
				deltaBytes := (int(float64(tc.bitrate)/8*tc.period.Seconds()*0.9) - tc.keyBytes) / (tc.frames - 1)
				sequence := uint16(0)
				for frame := 0; frame < 6*tc.frames; frame++ {
					key := frame%tc.frames == 0
					size := deltaBytes
					if key {
						size = tc.keyBytes
					}
					decision := p.AdmitMediaFrame(size, key)
					if !decision.admitted {
						t.Fatalf("frame %d (key=%v) rejected, backlog=%s", frame, key, p.admissionQueueDelay(0))
					}
					for remaining := size; remaining > 0; {
						payload := min(remaining, 1188)
						remaining -= payload
						timestamp := uint32(int64(frame) * 90000 * int64(tc.period) / int64(time.Second) / int64(tc.frames))
						_, err := p.Write(&rtp.Header{Version: 2, SSRC: 10, SequenceNumber: sequence, Timestamp: timestamp, Marker: remaining == 0}, make([]byte, payload), nil)
						if err != nil {
							decision.completePacketization()
							t.Fatal(err)
						}
						sequence++
					}
					decision.completePacketization()
					time.Sleep(tc.period / time.Duration(tc.frames))
				}
				synctest.Wait()
				if delivered != 6*tc.frames {
					t.Fatalf("delivered %d complete frames, want %d", delivered, 6*tc.frames)
				}
				if delay := p.admissionQueueDelay(0); delay != 0 {
					t.Fatalf("GOP backlog did not drain: %s", delay)
				}
				// Check the wire envelope in sliding windows. Admitting a key
				// must not secretly increase the network pacing rate.
				const window = 50 * time.Millisecond
				limit := int(p.sustainedBytesPerSecond()*window.Seconds()+p.maximumBurstBytes()) + 1200
				for start, packet := range sent {
					bytes := 0
					for _, next := range sent[start:] {
						if next.at.Sub(packet.at) >= window {
							break
						}
						bytes += next.bytes
					}
					if bytes > limit {
						t.Fatalf("wire burst %d bytes exceeds %d", bytes, limit)
					}
				}
				wireKeyBytes := tc.keyBytes + 12*((tc.keyBytes+1187)/1188)
				bound := queueDelayAtRate(int64(wireKeyBytes), p.sustainedBytesPerSecond()) + 20*time.Millisecond
				if actual := time.Duration(p.maximumPrimaryResidenceNs.Load()); actual > bound {
					t.Fatalf("residence %s exceeds one key burst %s", actual, bound)
				}
				t.Logf("max packet residence: %v ms", p.Stats()["pacerMaximumPrimaryResidenceMilliseconds"])
			})
		})
	}
}

func TestTokenBucketPacerKeyBurstDoesNotAdmitSustainedOverload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newTokenBucketPacer(1_279_318, 1.5, 1024)
		defer p.Close()
		p.AddStream(10, interceptor.RTPWriterFunc(func(h *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
			return h.MarshalSize() + len(payload), nil
		}))
		sequence := uint16(0)
		send := func(size int, key bool) bool {
			d := p.AdmitMediaFrame(size, key)
			defer d.completePacketization()
			if !d.admitted {
				return false
			}
			for remaining := size; remaining > 0; {
				n := min(remaining, 1188)
				remaining -= n
				if _, err := p.Write(&rtp.Header{Version: 2, SSRC: 10, SequenceNumber: sequence, Marker: remaining == 0}, make([]byte, n), nil); err != nil {
					t.Fatal(err)
				}
				sequence++
			}
			return true
		}
		if !send(98_502, true) {
			t.Fatal("recovery key rejected")
		}
		for range 10 {
			time.Sleep(time.Second / 30)
			send(30_000, false)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if p.mediaFramesDropped.Load() == 0 {
			t.Fatal("sustained overload was admitted")
		}
		if send(1_000, false) {
			t.Fatal("broken GOP must wait for a recovery key")
		}
		if !send(28_000, true) {
			t.Fatal("recovery did not restart after congestion drained")
		}
		time.Sleep(200 * time.Millisecond)
		// The earlier large key must not leave a permanently larger budget.
		if send(60_000, false) {
			t.Fatal("old key burst incorrectly relaxed later delta admission")
		}
		if p.maximumPrimaryResidenceNs.Load() > int64(450*time.Millisecond) {
			t.Fatal("overload grew the queue beyond the initial key burst")
		}
	})
}
