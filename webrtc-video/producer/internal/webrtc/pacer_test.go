package webrtc

import (
	"math"
	"sync"
	"testing"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtp"
)

type recordingPacer struct {
	mu       sync.Mutex
	bitrates []int
	closed   bool
	streams  map[uint32]interceptor.RTPWriter
	writes   int
}

type recordingWriter struct{}

func (*recordingWriter) Write(
	header *rtp.Header,
	payload []byte,
	_ interceptor.Attributes,
) (int, error) {
	return header.MarshalSize() + len(payload), nil
}

func (p *recordingPacer) AddStream(ssrc uint32, writer interceptor.RTPWriter) {
	p.mu.Lock()
	if p.streams == nil {
		p.streams = make(map[uint32]interceptor.RTPWriter)
	}
	p.streams[ssrc] = writer
	p.mu.Unlock()
}

func (p *recordingPacer) SetTargetBitrate(bitrate int) {
	p.mu.Lock()
	p.bitrates = append(p.bitrates, bitrate)
	p.mu.Unlock()
}

func (p *recordingPacer) Write(
	header *rtp.Header,
	payload []byte,
	_ interceptor.Attributes,
) (int, error) {
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
	return header.MarshalSize() + len(payload), nil
}

func TestMinimumBitratePacerDelegatesLifecycleAndWrites(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacer(delegate, 1_500_000)
	pacer.AddStream(42, nil)
	header := &rtp.Header{SSRC: 42}
	written, err := pacer.Write(header, []byte{1, 2, 3}, nil)
	if err != nil {
		t.Fatalf("write returned an error: %v", err)
	}
	if written != header.MarshalSize()+3 {
		t.Fatalf("written bytes = %d, want %d", written, header.MarshalSize()+3)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close returned an error: %v", err)
	}
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.streams) != 1 {
		t.Fatalf("delegated stream count = %d, want 1", len(delegate.streams))
	}
	if _, ok := delegate.streams[42]; !ok {
		t.Fatal("primary SSRC 42 was not delegated")
	}
	if delegate.writes != 1 || !delegate.closed {
		t.Fatalf("delegated writes = %d, closed = %t", delegate.writes, delegate.closed)
	}
}

func TestMinimumBitratePacerUsesRealTimePacingHeadroom(t *testing.T) {
	pacer := newMinimumBitratePacer(5_000_000, 500_000)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	delegate, ok := pacer.delegate.(*tokenBucketPacer)
	if !ok {
		t.Fatalf("delegate type = %T, want *tokenBucketPacer", pacer.delegate)
	}
	if delegate.pacingFactor != realTimePacingFactor {
		t.Fatalf(
			"pacing factor = %g, want %g",
			delegate.pacingFactor,
			realTimePacingFactor,
		)
	}
}

func TestMinimumBitratePacerSharesMediaHeadroomWithFlexFEC(t *testing.T) {
	tests := []struct {
		name              string
		mediaPacingFactor float64
		protection        flexFECProtection
		want              float64
	}{
		{name: "disabled", mediaPacingFactor: 1.5, want: 1.5},
		{
			name:              "two repairs per five media packets",
			mediaPacingFactor: 1.5,
			protection:        flexFECProtection{mediaPackets: 5, repairPackets: 2},
			want:              15.0 / 14.0,
		},
		{
			name:              "two repairs per four media packets",
			mediaPacingFactor: 1.5,
			protection:        flexFECProtection{mediaPackets: 4, repairPackets: 2},
			want:              1,
		},
		{
			name:              "repair overhead above media headroom",
			mediaPacingFactor: 1.5,
			protection:        flexFECProtection{mediaPackets: 3, repairPackets: 2},
			want:              1,
		},
		{
			name:              "invalid protection",
			mediaPacingFactor: 1.5,
			protection:        flexFECProtection{repairPackets: 2},
			want:              1.5,
		},
		{name: "factor below one", mediaPacingFactor: 0.5, want: 1},
		{name: "not a number", mediaPacingFactor: math.NaN(), want: 1},
		{name: "positive infinity", mediaPacingFactor: math.Inf(1), want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := pacingFactorForProtection(test.mediaPacingFactor, test.protection)
			if math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("pacing factor = %.15g, want %.15g", got, test.want)
			}
		})
	}
}

func TestMinimumBitratePacerBoundsProtectedWireEnvelope(t *testing.T) {
	protection := flexFECProtection{mediaPackets: 4, repairPackets: 2}
	pacer := newMinimumBitratePacerWithProtection(2_200_000, 500_000, protection)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	delegate, ok := pacer.delegate.(*tokenBucketPacer)
	if !ok {
		t.Fatalf("delegate type = %T, want *tokenBucketPacer", pacer.delegate)
	}
	assertPacerEnvelope := func(mediaTarget, wireTarget, pacingTarget int) {
		t.Helper()
		stats := delegate.Stats()
		if got := delegate.targetBitrateValue(); got != wireTarget {
			t.Fatalf("wire target for media target %d = %d, want %d", mediaTarget, got, wireTarget)
		}
		if got, ok := stats["pacerTargetBitrateBps"].(int); !ok || got != wireTarget {
			t.Fatalf("reported wire target for media target %d = %v, want %d", mediaTarget, stats["pacerTargetBitrateBps"], wireTarget)
		}
		if got, ok := stats["pacerPacingBitrateBps"].(int); !ok || got != pacingTarget {
			t.Fatalf("pacing envelope for media target %d = %v, want %d", mediaTarget, stats["pacerPacingBitrateBps"], pacingTarget)
		}
	}
	assertPacerEnvelope(2_200_000, 3_300_000, 3_300_000)
	pacer.SetTargetBitrate(3_000_000)
	assertPacerEnvelope(3_000_000, 4_500_000, 4_500_000)
	pacer.SetTargetBitrate(100_000)
	assertPacerEnvelope(500_000, 750_000, 750_000)
}

func TestMinimumBitratePacerPreservesUnusedHeadroomWithLighterFlexFEC(t *testing.T) {
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 2}
	pacer := newMinimumBitratePacerWithProtection(5_000_000, 500_000, protection)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	delegate, ok := pacer.delegate.(*tokenBucketPacer)
	if !ok {
		t.Fatalf("delegate type = %T, want *tokenBucketPacer", pacer.delegate)
	}
	stats := delegate.Stats()
	if got := stats["pacerTargetBitrateBps"]; got != 7_000_000 {
		t.Fatalf("protected wire target = %v, want 7000000", got)
	}
	if got := stats["pacerPacingBitrateBps"]; got != 7_500_000 {
		t.Fatalf("pacing envelope = %v, want 7500000", got)
	}
}

func TestTokenBucketPacerReportsOneAtomicPacingEnvelope(t *testing.T) {
	pacer := newTokenBucketPacer(3_300_000, 1, 16)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 100_000; index++ {
			if index%2 == 0 {
				pacer.SetTargetBitrate(3_300_000)
				continue
			}
			pacer.SetTargetBitrate(4_500_000)
		}
	}()
	observations := 0
	for {
		stats := pacer.Stats()
		target, targetOK := stats["pacerTargetBitrateBps"].(int)
		envelope, envelopeOK := stats["pacerPacingBitrateBps"].(int)
		if !targetOK || !envelopeOK || target != envelope {
			t.Fatalf("inconsistent pacing snapshot: target=%v envelope=%v", stats["pacerTargetBitrateBps"], stats["pacerPacingBitrateBps"])
		}
		observations++
		select {
		case <-done:
			if observations == 0 {
				t.Fatal("expected at least one concurrent pacing observation")
			}
			return
		default:
		}
	}
}

func TestMinimumBitratePacerMapsMediaTargetToProtectedWireBudget(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacerWithProtection(
		delegate,
		2_000_000,
		flexFECProtection{mediaPackets: 5, repairPackets: 2},
	)
	pacer.SetTargetBitrate(5_000_000)
	pacer.SetTargetBitrate(1_000_000)
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.bitrates) != 2 {
		t.Fatalf("delegated bitrate updates = %d, want 2", len(delegate.bitrates))
	}
	if delegate.bitrates[0] != 7_000_000 {
		t.Fatalf("protected wire target = %d, want 7000000", delegate.bitrates[0])
	}
	if delegate.bitrates[1] != 2_800_000 {
		t.Fatalf("protected wire floor = %d, want 2800000", delegate.bitrates[1])
	}
}

func TestMinimumBitratePacerAssociatesRepairStreamsWithTheirPrimaryWriter(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacer(delegate, 1_500_000)
	firstWriter := &recordingWriter{}
	secondWriter := &recordingWriter{}
	pacer.AddStream(10, firstWriter)
	pacer.AddStream(20, secondWriter)
	pacer.addAssociatedStreams(10, 11, 12, 0, 10)
	pacer.addAssociatedStreams(20, 21, 22)
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.streams) != 6 {
		t.Fatalf("delegated stream count = %d, want 6", len(delegate.streams))
	}
	for _, ssrc := range []uint32{10, 11, 12} {
		if delegate.streams[ssrc] != firstWriter {
			t.Fatalf("SSRC %d is not associated with the first writer", ssrc)
		}
	}
	for _, ssrc := range []uint32{20, 21, 22} {
		if delegate.streams[ssrc] != secondWriter {
			t.Fatalf("SSRC %d is not associated with the second writer", ssrc)
		}
	}
}

func TestAssociatedStreamBandwidthEstimatorRegistersRTXAndFECBeforeFirstWrite(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacer(delegate, 1_500_000)
	estimator, err := gcc.NewSendSideBWE(gcc.SendSideBWEPacer(pacer))
	if err != nil {
		t.Fatalf("create estimator: %v", err)
	}
	t.Cleanup(func() {
		if err := estimator.Close(); err != nil {
			t.Errorf("close estimator: %v", err)
		}
	})
	wrapped := &associatedStreamBandwidthEstimator{
		SendSideBWE: estimator,
		pacer:       pacer,
	}
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		return header.MarshalSize() + len(payload), nil
	})
	wrapped.AddStream(&interceptor.StreamInfo{
		SSRC:                       42,
		SSRCRetransmission:         43,
		SSRCForwardErrorCorrection: 44,
	}, writer)
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	for _, ssrc := range []uint32{42, 43, 44} {
		if _, ok := delegate.streams[ssrc]; !ok {
			t.Fatalf("SSRC %d was not registered with the pacer", ssrc)
		}
	}
}

func TestMinimumBitratePacerConcurrentStreamAssociation(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := wrapMinimumBitratePacer(delegate, 1_500_000)
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		return header.MarshalSize() + len(payload), nil
	})
	const streams = 64
	var wait sync.WaitGroup
	wait.Add(streams)
	for index := 0; index < streams; index++ {
		go func(primary uint32) {
			defer wait.Done()
			pacer.AddStream(primary, writer)
			pacer.addAssociatedStreams(primary, primary+1, primary+2)
		}(uint32(index*3 + 1))
	}
	wait.Wait()
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.streams) != streams*3 {
		t.Fatalf("delegated stream count = %d, want %d", len(delegate.streams), streams*3)
	}
}

func (p *recordingPacer) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}

func TestMinimumBitratePacerClampsUpdates(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := &minimumBitratePacer{
		delegate:       delegate,
		minimumBitrate: 1_500_000,
	}
	pacer.SetTargetBitrate(100_000)
	pacer.SetTargetBitrate(2_400_000)
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.bitrates) != 2 {
		t.Fatalf("recorded bitrate updates = %d, want 2", len(delegate.bitrates))
	}
	if delegate.bitrates[0] != 1_500_000 {
		t.Fatalf("clamped bitrate = %d, want 1500000", delegate.bitrates[0])
	}
	if delegate.bitrates[1] != 2_400_000 {
		t.Fatalf("unchanged bitrate = %d, want 2400000", delegate.bitrates[1])
	}
}

func TestMinimumBitratePacerConcurrentUpdates(t *testing.T) {
	delegate := &recordingPacer{}
	pacer := &minimumBitratePacer{
		delegate:       delegate,
		minimumBitrate: 1_500_000,
	}
	const updates = 128
	var wait sync.WaitGroup
	wait.Add(updates)
	for index := 0; index < updates; index++ {
		go func(bitrate int) {
			defer wait.Done()
			pacer.SetTargetBitrate(bitrate)
		}(index * 20_000)
	}
	wait.Wait()
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.bitrates) != updates {
		t.Fatalf("recorded bitrate updates = %d, want %d", len(delegate.bitrates), updates)
	}
	for _, bitrate := range delegate.bitrates {
		if bitrate < 1_500_000 {
			t.Fatalf("recorded bitrate %d below configured minimum", bitrate)
		}
	}
}
