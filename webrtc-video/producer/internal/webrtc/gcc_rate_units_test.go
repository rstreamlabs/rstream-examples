package webrtc

import (
	"encoding/binary"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func TestProtectedEstimatorPreservesAcknowledgedRTPUnits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		protection := flexFECProtection{mediaPackets: 5, repairPackets: 1}
		delegate := &recordingPacer{}
		pacer := wrapMinimumBitratePacerWithProtection(delegate, 100_000, protection)
		native, err := gcc.NewSendSideBWE(gcc.SendSideBWEPacer(pacer), gcc.SendSideBWEInitialBitrate(800_000))
		if err != nil {
			t.Fatal(err)
		}
		defer native.Close()
		estimator := &associatedStreamBandwidthEstimator{SendSideBWE: native, pacer: pacer, protection: protection}
		writes := map[uint32]int{}
		estimator.AddStream(&interceptor.StreamInfo{
			SSRC: 10, SSRCForwardErrorCorrection: 12,
			RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: transportCCHeaderExtensionURI, ID: 1}},
		}, interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
			writes[header.SSRC]++
			return header.MarshalSize() + len(payload), nil
		}))
		feedback := &rtcp.TransportLayerCC{
			BaseSequenceNumber: 1, PacketStatusCount: 10, ReferenceTime: 1,
			PacketChunks: []rtcp.PacketStatusChunk{&rtcp.RunLengthChunk{
				Type: rtcp.TypeTCCRunLengthChunk, PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta, RunLength: 10,
			}},
		}
		for sequence := uint16(1); sequence <= 10; sequence++ {
			header := &rtp.Header{Version: 2, SSRC: 10, SequenceNumber: sequence}
			if err := header.SetExtension(1, binary.BigEndian.AppendUint16(nil, sequence)); err != nil {
				t.Fatal(err)
			}
			// Exercise the registered GCC writer, then its untracked FEC sibling.
			if _, err := delegate.streams[10].Write(header, make([]byte, 1000), nil); err != nil {
				t.Fatal(err)
			}
			if sequence%5 == 0 {
				if _, err := delegate.streams[12].Write(&rtp.Header{Version: 2, SSRC: 12}, make([]byte, 1000), nil); err != nil {
					t.Fatal(err)
				}
			}
			feedback.RecvDeltas = append(feedback.RecvDeltas, &rtcp.RecvDelta{Type: rtcp.TypeTCCPacketReceivedSmallDelta, Delta: 10_000})
			time.Sleep(10 * time.Millisecond)
		}
		if err := native.WriteRTCP([]rtcp.Packet{feedback}, nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if writes[10] != 10 || writes[12] != 2 {
			t.Fatalf("primary/FEC delivery = %v", writes)
		}
		// (1000 payload + 20 RTP header) bytes / 10 ms = 816 kbit/s.
		// FEC consumes capacity but is absent from acknowledged throughput.
		if rate := native.GetStats()["acknowledgedBitrate"]; rate != 816_000 {
			t.Fatalf("acknowledged primary RTP = %v, want 816000", rate)
		}
		nativeTarget := native.GetTargetBitrate()
		if target := estimator.GetTargetBitrate(); target != nativeTarget {
			t.Fatalf("encoder target = %d, tracked-RTP GCC target = %d; untracked FEC must not be deducted again", target, nativeTarget)
		}
		estimator.deliverCurrentBitrate(nativeTarget)
		delegate.mu.Lock()
		defer delegate.mu.Unlock()
		if actual := delegate.bitrates[len(delegate.bitrates)-1]; actual != wireBitrate(nativeTarget, protection) {
			t.Fatalf("protected pacing target = %d, want %d", actual, wireBitrate(nativeTarget, protection))
		}
	})
}
