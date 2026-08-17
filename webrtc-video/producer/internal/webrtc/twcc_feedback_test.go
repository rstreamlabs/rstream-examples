package webrtc

import (
	"testing"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func TestTrimTransportCCPaddingPreservesTheReportedStatuses(t *testing.T) {
	feedback := transportCCFeedbackWithPadding()
	reported, lost, padding, valid := transportCCStatusCounts(feedback)
	if !valid || reported != 2 || lost != 0 || padding != 5 {
		t.Fatalf(
			"status counts = (%d, %d, %d, %t), want (2, 0, 5, true)",
			reported,
			lost,
			padding,
			valid,
		)
	}
	trimmed, changed := trimTransportCCPadding(feedback)
	if !changed {
		t.Fatal("expected transport feedback padding to be trimmed")
	}
	if trimmed == feedback {
		t.Fatal("transport feedback was mutated instead of copied")
	}
	if len(feedback.PacketChunks[0].(*rtcp.StatusVectorChunk).SymbolList) != 7 {
		t.Fatal("original transport feedback was mutated")
	}
	chunk, ok := trimmed.PacketChunks[0].(*rtcp.StatusVectorChunk)
	if !ok {
		t.Fatalf("trimmed chunk type = %T, want *rtcp.StatusVectorChunk", trimmed.PacketChunks[0])
	}
	if len(chunk.SymbolList) != int(feedback.PacketStatusCount) {
		t.Fatalf("trimmed status count = %d, want %d", len(chunk.SymbolList), feedback.PacketStatusCount)
	}
}

func TestTrimTransportCCPaddingBoundsARunLengthChunk(t *testing.T) {
	feedback := &rtcp.TransportLayerCC{
		PacketStatusCount: 2,
		PacketChunks: []rtcp.PacketStatusChunk{
			&rtcp.RunLengthChunk{
				PacketStatusSymbol: rtcp.TypeTCCPacketNotReceived,
				RunLength:          7,
			},
		},
	}
	trimmed, changed := trimTransportCCPadding(feedback)
	if !changed {
		t.Fatal("expected run-length padding to be trimmed")
	}
	chunk, ok := trimmed.PacketChunks[0].(*rtcp.RunLengthChunk)
	if !ok {
		t.Fatalf("trimmed chunk type = %T, want *rtcp.RunLengthChunk", trimmed.PacketChunks[0])
	}
	if chunk.RunLength != feedback.PacketStatusCount {
		t.Fatalf("trimmed run length = %d, want %d", chunk.RunLength, feedback.PacketStatusCount)
	}
	if original := feedback.PacketChunks[0].(*rtcp.RunLengthChunk).RunLength; original != 7 {
		t.Fatalf("original run length was mutated to %d", original)
	}
}

func TestTransportCCStatusCountsRejectsAnIncompleteReport(t *testing.T) {
	feedback := &rtcp.TransportLayerCC{
		PacketStatusCount: 2,
		PacketChunks: []rtcp.PacketStatusChunk{
			&rtcp.RunLengthChunk{
				PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta,
				RunLength:          1,
			},
		},
	}
	if _, _, _, valid := transportCCStatusCounts(feedback); valid {
		t.Fatal("incomplete transport feedback was accepted")
	}
	if trimmed, changed := trimTransportCCPadding(feedback); changed || trimmed != feedback {
		t.Fatal("incomplete transport feedback was rewritten")
	}
}

func TestAssociatedEstimatorDoesNotCountTransportFeedbackPaddingAsLoss(t *testing.T) {
	pacer := wrapMinimumBitratePacer(gcc.NewNoOpPacer(), 500_000)
	underlying, err := gcc.NewSendSideBWE(
		gcc.SendSideBWEInitialBitrate(5_000_000),
		gcc.SendSideBWEPacer(pacer),
	)
	if err != nil {
		t.Fatalf("create bandwidth estimator: %v", err)
	}
	estimator := &associatedStreamBandwidthEstimator{
		SendSideBWE: underlying,
		pacer:       pacer,
	}
	t.Cleanup(func() {
		if err := estimator.Close(); err != nil {
			t.Errorf("close bandwidth estimator: %v", err)
		}
	})
	writer := estimator.AddStream(
		&interceptor.StreamInfo{
			SSRC: 42,
			RTPHeaderExtensions: []interceptor.RTPHeaderExtension{
				{URI: transportCCHeaderExtensionURI, ID: 1},
			},
		},
		interceptor.RTPWriterFunc(func(
			header *rtp.Header,
			payload []byte,
			_ interceptor.Attributes,
		) (int, error) {
			return header.MarshalSize() + len(payload), nil
		}),
	)
	for sequence := uint16(0); sequence < 7; sequence++ {
		extension, err := (rtp.TransportCCExtension{TransportSequence: sequence}).Marshal()
		if err != nil {
			t.Fatalf("marshal transport sequence %d: %v", sequence, err)
		}
		header := &rtp.Header{SSRC: 42, SequenceNumber: sequence}
		if err := header.SetExtension(1, extension); err != nil {
			t.Fatalf("set transport sequence %d: %v", sequence, err)
		}
		if _, err := writer.Write(header, []byte{1}, nil); err != nil {
			t.Fatalf("write packet %d: %v", sequence, err)
		}
	}
	if err := estimator.WriteRTCP(
		[]rtcp.Packet{transportCCFeedbackWithPadding()},
		nil,
	); err != nil {
		t.Fatalf("process transport feedback: %v", err)
	}
	stats := estimator.GetStats()
	averageLoss, ok := stats["averageLoss"].(float64)
	if !ok {
		t.Fatal("bandwidth estimator did not expose average loss")
	}
	if averageLoss != 0 {
		t.Fatalf("feedback padding produced %.2f%% false packet loss", averageLoss*100)
	}
	for name, expected := range map[string]uint64{
		"twccFeedbackPackets":  1,
		"twccPaddingStatuses":  5,
		"twccReportedLost":     0,
		"twccReportedStatuses": 2,
	} {
		if actual, ok := stats[name].(uint64); !ok || actual != expected {
			t.Fatalf("%s = %v, want %d", name, stats[name], expected)
		}
	}
}

func TestAssociatedEstimatorAppliesPersistentHighLossWithoutDelayCallback(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacer(delegate, 2_000_000)
	underlying, err := gcc.NewSendSideBWE(
		gcc.SendSideBWEInitialBitrate(8_000_000),
		gcc.SendSideBWEMinBitrate(2_000_000),
		gcc.SendSideBWEMaxBitrate(8_000_000),
		gcc.SendSideBWEPacer(pacer),
	)
	if err != nil {
		t.Fatalf("create bandwidth estimator: %v", err)
	}
	estimator := &associatedStreamBandwidthEstimator{
		SendSideBWE:         underlying,
		minimumMediaBitrate: 2_000_000,
		maximumMediaBitrate: 8_000_000,
		lossGuard:           newFeedbackLossGuard(2_000_000),
		pacer:               pacer,
	}
	callbackTargets := make(chan int, 4)
	estimator.OnTargetBitrateChange(func(bitrate int) {
		callbackTargets <- bitrate
	})
	t.Cleanup(func() {
		if err := estimator.Close(); err != nil {
			t.Errorf("close bandwidth estimator: %v", err)
		}
	})
	writer := estimator.AddStream(
		&interceptor.StreamInfo{
			SSRC: 42,
			RTPHeaderExtensions: []interceptor.RTPHeaderExtension{
				{URI: transportCCHeaderExtensionURI, ID: 1},
			},
		},
		interceptor.RTPWriterFunc(func(
			header *rtp.Header,
			payload []byte,
			_ interceptor.Attributes,
		) (int, error) {
			return header.MarshalSize() + len(payload), nil
		}),
	)
	writeTransportPackets(t, writer, 0, 200)
	for index, base := range []uint16{0, 100} {
		if err := estimator.WriteRTCP(
			[]rtcp.Packet{transportCCFeedbackWithLoss(base, uint8(index))},
			nil,
		); err != nil {
			t.Fatalf("process high-loss feedback %d: %v", index, err)
		}
	}
	if target := estimator.GetTargetBitrate(); target >= 8_000_000 {
		t.Fatalf("guarded target = %d, want an immediate reduction", target)
	}
	lastCallbackTarget := 0
drainCallbacks:
	for {
		select {
		case lastCallbackTarget = <-callbackTargets:
		default:
			break drainCallbacks
		}
	}
	if lastCallbackTarget == 0 || lastCallbackTarget >= 8_000_000 {
		t.Fatalf("last callback target = %d, want an immediate reduction", lastCallbackTarget)
	}
	delegate.mu.Lock()
	lastPacerTarget := delegate.bitrates[len(delegate.bitrates)-1]
	delegate.mu.Unlock()
	if lastPacerTarget >= 8_000_000 {
		t.Fatalf("pacer target = %d, want an immediate reduction", lastPacerTarget)
	}
	stats := estimator.GetStats()
	if reductions, ok := stats["lossGuardReductions"].(uint64); !ok || reductions == 0 {
		t.Fatalf("loss guard reductions = %v, want at least one", stats["lossGuardReductions"])
	}
}

func writeTransportPackets(
	t *testing.T,
	writer interceptor.RTPWriter,
	first uint16,
	count int,
) {
	t.Helper()
	for offset := 0; offset < count; offset++ {
		sequence := first + uint16(offset)
		extension, err := (rtp.TransportCCExtension{TransportSequence: sequence}).Marshal()
		if err != nil {
			t.Fatalf("marshal transport sequence %d: %v", sequence, err)
		}
		header := &rtp.Header{SSRC: 42, SequenceNumber: sequence}
		if err := header.SetExtension(1, extension); err != nil {
			t.Fatalf("set transport sequence %d: %v", sequence, err)
		}
		if _, err := writer.Write(header, []byte{1}, nil); err != nil {
			t.Fatalf("write packet %d: %v", sequence, err)
		}
	}
}

func transportCCFeedbackWithLoss(base uint16, feedbackCount uint8) *rtcp.TransportLayerCC {
	deltas := make([]*rtcp.RecvDelta, 0, 80)
	for index := 0; index < 80; index++ {
		deltas = append(deltas, &rtcp.RecvDelta{
			Type:  rtcp.TypeTCCPacketReceivedSmallDelta,
			Delta: 250,
		})
	}
	return &rtcp.TransportLayerCC{
		BaseSequenceNumber: base,
		PacketStatusCount:  100,
		FbPktCount:         feedbackCount,
		PacketChunks: []rtcp.PacketStatusChunk{
			&rtcp.RunLengthChunk{
				PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta,
				RunLength:          40,
			},
			&rtcp.RunLengthChunk{
				PacketStatusSymbol: rtcp.TypeTCCPacketNotReceived,
				RunLength:          20,
			},
			&rtcp.RunLengthChunk{
				PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta,
				RunLength:          40,
			},
		},
		RecvDeltas: deltas,
	}
}

func transportCCFeedbackWithPadding() *rtcp.TransportLayerCC {
	return &rtcp.TransportLayerCC{
		BaseSequenceNumber: 0,
		PacketStatusCount:  2,
		PacketChunks: []rtcp.PacketStatusChunk{
			&rtcp.StatusVectorChunk{
				Type:       rtcp.TypeTCCStatusVectorChunk,
				SymbolSize: rtcp.TypeTCCSymbolSizeTwoBit,
				SymbolList: []uint16{
					rtcp.TypeTCCPacketReceivedSmallDelta,
					rtcp.TypeTCCPacketReceivedSmallDelta,
					rtcp.TypeTCCPacketNotReceived,
					rtcp.TypeTCCPacketNotReceived,
					rtcp.TypeTCCPacketNotReceived,
					rtcp.TypeTCCPacketNotReceived,
					rtcp.TypeTCCPacketNotReceived,
				},
			},
		},
		RecvDeltas: []*rtcp.RecvDelta{
			{Type: rtcp.TypeTCCPacketReceivedSmallDelta, Delta: 250},
			{Type: rtcp.TypeTCCPacketReceivedSmallDelta, Delta: 250},
		},
	}
}
