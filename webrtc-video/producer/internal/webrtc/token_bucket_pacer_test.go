package webrtc

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

func TestTokenBucketPacerWritesPrimaryAndRepairStreams(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	written := make(chan uint32, 2)
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		_ []byte,
		_ interceptor.Attributes,
	) (int, error) {
		written <- header.SSRC
		return header.MarshalSize(), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	for _, ssrc := range []uint32{10, 11} {
		if _, err := pacer.Write(&rtp.Header{SSRC: ssrc}, []byte{1}, nil); err != nil {
			t.Fatalf("queue SSRC %d: %v", ssrc, err)
		}
	}
	seen := map[uint32]bool{}
	for range 2 {
		select {
		case ssrc := <-written:
			seen[ssrc] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced writes")
		}
	}
	if !seen[10] || !seen[11] {
		t.Fatalf("paced SSRCs = %v, want 10 and 11", seen)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerReportsRepairStreamIdentityAndSequence(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	written := make(chan struct{}, 1)
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		written <- struct{}{}
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.markPrimaryStream(10)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	pacer.AddStream(12, writer)
	pacer.markForwardErrorCorrectionStream(12)
	if _, err := pacer.Write(&rtp.Header{
		SSRC:           11,
		SequenceNumber: 65535,
	}, []byte{0, 42}, nil); err != nil {
		t.Fatalf("queue RTX packet: %v", err)
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RTX write")
	}
	stats := pacer.Stats()
	if stats["pacerPrimarySSRC"] != uint32(10) ||
		stats["pacerRetransmissionSSRC"] != uint32(11) ||
		stats["pacerForwardErrorCorrectionSSRC"] != uint32(12) {
		t.Fatalf("unexpected stream identity diagnostics: %+v", stats)
	}
	if stats["pacerFirstRetransmissionSequence"] != uint32(65535) ||
		stats["pacerLastRetransmissionSequence"] != uint32(65535) ||
		stats["pacerRetransmissionSequenceSamples"] != uint64(1) {
		t.Fatalf("unexpected RTX sequence diagnostics: %+v", stats)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerOwnsQueuedPacketData(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	result := make(chan struct {
		attribute string
		payload   byte
		sequence  uint16
	}, 1)
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		attributes interceptor.Attributes,
	) (int, error) {
		close(entered)
		<-release
		result <- struct {
			attribute string
			payload   byte
			sequence  uint16
		}{
			attribute: attributes.Get("key").(string),
			payload:   payload[0],
			sequence:  header.SequenceNumber,
		}
		return header.MarshalSize() + len(payload), nil
	}))
	header := &rtp.Header{SSRC: 10, SequenceNumber: 12}
	payload := []byte{42}
	attributes := interceptor.Attributes{"key": "before"}
	if _, err := pacer.Write(header, payload, attributes); err != nil {
		t.Fatalf("queue packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the packet")
	}
	header.SequenceNumber = 99
	payload[0] = 7
	attributes["key"] = "after"
	close(release)
	select {
	case actual := <-result:
		if actual.sequence != 12 || actual.payload != 42 || actual.attribute != "before" {
			t.Fatalf("queued packet changed after caller mutation: %+v", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not complete")
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerPrioritizesPrimarySSRCNACKRepair(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	defer func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	}()
	firstWrite := make(chan struct{})
	releaseFirstWrite := make(chan struct{})
	written := make(chan uint16, 3)
	var calls atomic.Uint32
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		_ []byte,
		_ interceptor.Attributes,
	) (int, error) {
		if calls.Add(1) == 1 {
			close(firstWrite)
			<-releaseFirstWrite
		}
		written <- header.SequenceNumber
		return header.MarshalSize() + 1, nil
	}))
	write := func(sequence uint16) {
		t.Helper()
		if _, err := pacer.Write(&rtp.Header{
			SSRC:           10,
			SequenceNumber: sequence,
			Marker:         true,
		}, []byte{1}, nil); err != nil {
			t.Fatalf("queue sequence %d: %v", sequence, err)
		}
	}
	write(100)
	select {
	case <-firstWrite:
	case <-time.After(time.Second):
		t.Fatal("pacer did not start the first primary packet")
	}
	write(101)
	write(100)
	close(releaseFirstWrite)
	got := make([]uint16, 0, 3)
	for range 3 {
		select {
		case sequence := <-written:
			got = append(got, sequence)
		case <-time.After(time.Second):
			t.Fatalf("paced sequence order = %v, want [100 100 101]", got)
		}
	}
	want := []uint16{100, 100, 101}
	if !slices.Equal(got, want) {
		t.Fatalf("paced sequence order = %v, want %v", got, want)
	}
	stats := pacer.Stats()
	if stats["pacerSentPrimary"] != uint64(2) || stats["pacerSentRetransmission"] != uint64(1) {
		t.Fatalf("unexpected primary-SSRC repair stats: %+v", stats)
	}
}

func TestTokenBucketPacerClassifiesPrimarySequenceWrap(t *testing.T) {
	tracker := &primarySequenceTracker{}
	if repeated := tracker.repeated(65535); repeated {
		t.Fatal("first primary sequence was classified as a retransmission")
	}
	if repeated := tracker.repeated(0); repeated {
		t.Fatal("wrapped primary sequence was classified as a retransmission")
	}
	if repeated := tracker.repeated(65535); !repeated {
		t.Fatal("pre-wrap primary sequence was not classified as a retransmission")
	}
}

func TestTokenBucketPacerDoesNotClassifyUnseenReorderedPrimaryPacketsAsRepair(t *testing.T) {
	tracker := &primarySequenceTracker{}
	if repeated := tracker.repeated(101); repeated {
		t.Fatal("first primary sequence was classified as a retransmission")
	}
	if repeated := tracker.repeated(100); repeated {
		t.Fatal("unseen reordered primary sequence was classified as a retransmission")
	}
	if repeated := tracker.repeated(100); !repeated {
		t.Fatal("repeated reordered primary sequence was not classified as a retransmission")
	}
}

func TestTokenBucketPacerClassifiesConcurrentPrimaryDuplicatesOnce(t *testing.T) {
	const callers = 100
	tracker := &primarySequenceTracker{}
	start := make(chan struct{})
	var repeated atomic.Uint32
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			if tracker.repeated(42) {
				repeated.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := repeated.Load(); got != callers-1 {
		t.Fatalf("concurrent duplicate classifications = %d, want %d", got, callers-1)
	}
}

func TestTokenBucketPacerRejectsInvalidStreams(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	if _, err := pacer.Write(nil, nil, nil); !errors.Is(err, errPacerNilHeader) {
		t.Fatalf("nil-header error = %v, want nil-header error", err)
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, nil, nil); !errors.Is(err, errPacerUnknownStream) {
		t.Fatalf("unknown-stream error = %v, want unknown-stream error", err)
	}
	pacer.AddStream(10, nil)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, nil, nil); !errors.Is(err, errPacerUnknownStream) {
		t.Fatalf("nil-writer error = %v, want unknown-stream error", err)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerHonorsPacketBudget(t *testing.T) {
	pacer := newTokenBucketPacer(80_000, 1, 16)
	written := make(chan time.Time, 4)
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		_ *rtp.Header,
		_ []byte,
		_ interceptor.Attributes,
	) (int, error) {
		written <- time.Now()
		return 1012, nil
	}))
	for sequence := range uint16(4) {
		if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: sequence}, make([]byte, 1000), nil); err != nil {
			t.Fatalf("queue packet: %v", err)
		}
	}
	var times []time.Time
	for range 4 {
		select {
		case timestamp := <-written:
			times = append(times, timestamp)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for paced packet")
		}
	}
	if delay := times[3].Sub(times[0]); delay < 70*time.Millisecond {
		t.Fatalf("fourth packet arrived after %v, want at least 70ms", delay)
	}
	if residence, ok := pacer.Stats()["pacerMaximumQueueDelayMilliseconds"].(float64); !ok || residence < 70 {
		t.Fatalf("maximum packet residence = %v, want at least 70ms", residence)
	}
	if residence, ok := pacer.Stats()["pacerMaximumPrimaryResidenceMilliseconds"].(float64); !ok || residence < 70 {
		t.Fatalf("maximum primary residence = %v, want at least 70ms", residence)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerBoundsItsQueue(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 2)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return header.MarshalSize() + len(payload), nil
	}))
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first packet")
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{2}, nil); err != nil {
		t.Fatalf("queue second packet: %v", err)
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{3}, nil); !errors.Is(err, errPacerQueueFull) {
		t.Fatalf("third write error = %v, want queue-full error", err)
	}
	close(release)
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerDropsWholeFramesUntilAKeyFrameCanResume(t *testing.T) {
	pacer := newTokenBucketPacer(80_000, 1, 16)
	setSyntheticQueuedBytes(pacer, 5_000)
	pacer.observeSustainedQueueDelay()
	if decision := admitMediaFrame(pacer, 1_000, false); decision.admitted || !decision.requestKeyFrame {
		t.Fatal("expected an over-budget delta frame to be dropped")
	}
	setSyntheticQueuedBytes(pacer, 0)
	if decision := admitMediaFrame(pacer, 1_000, false); decision.admitted || decision.requestKeyFrame {
		t.Fatal("expected suppressed delta frames to wait for the outstanding key-frame request")
	}
	if decision := admitMediaFrame(pacer, 1_000, true); !decision.admitted || !decision.recoveryComplete || decision.requestKeyFrame {
		t.Fatal("expected a key frame to resume an empty queue")
	}
	if decision := admitMediaFrame(pacer, 1_000, false); !decision.admitted || decision.requestKeyFrame {
		t.Fatal("expected delta frames after recovery to be admitted")
	}
	stats := pacer.Stats()
	if dropped, ok := stats["pacerMediaFramesDropped"].(uint64); !ok || dropped != 2 {
		t.Fatalf("dropped media frames = %v, want 2", stats["pacerMediaFramesDropped"])
	}
	if maximum, ok := stats["pacerMaximumSustainedDelayMilliseconds"].(float64); !ok || maximum < 300 {
		t.Fatalf("maximum sustained delay = %v, want at least 300ms", stats["pacerMaximumSustainedDelayMilliseconds"])
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerRecordsBoundedAdmittedBacklog(t *testing.T) {
	pacer := newTokenBucketPacer(80_000, 1, 16)
	setSyntheticQueuedBytes(pacer, 1_000)
	decision := admitMediaFrame(pacer, 1_000, false)
	if !decision.admitted || decision.requestKeyFrame {
		t.Fatalf("unexpected bounded-backlog decision: %+v", decision)
	}
	stats := pacer.Stats()
	maximum, ok := stats["pacerMaximumAdmittedSustainedDelayMilliseconds"].(float64)
	if !ok || maximum != 200 {
		t.Fatalf("maximum admitted delay = %v, want 200ms", maximum)
	}
	setSyntheticQueuedBytes(pacer, 0)
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerRequestsAnotherKeyFrameWhenRecoveryFrameDoesNotFit(t *testing.T) {
	pacer := newTokenBucketPacer(80_000, 1, 16)
	if decision := admitMediaFrame(pacer, 1_000, true); !decision.admitted {
		t.Fatalf("seed key-frame admission = %+v, want admitted", decision)
	}
	setSyntheticQueuedBytes(pacer, 5_000)
	if decision := admitMediaFrame(pacer, 1_000, false); decision.admitted || !decision.requestKeyFrame || decision.requestRetryAfter < 250*time.Millisecond {
		t.Fatalf("over-budget delta decision = %+v, want a deferred key-frame request", decision)
	}
	decision := admitMediaFrame(pacer, 1_000, true)
	if decision.admitted || !decision.requestKeyFrame || decision.requestRetryAfter <= 0 {
		t.Fatal("expected a key frame that still exceeds the budget to be dropped and requested again")
	}
	setSyntheticQueuedBytes(pacer, 0)
	if decision := admitMediaFrame(pacer, 1_000, true); !decision.admitted || !decision.recoveryComplete || decision.requestKeyFrame {
		t.Fatal("expected the next fitting key frame to finish recovery")
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerPrioritizesRepairWithoutReorderingQueuedMedia(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{})
	release := make(chan struct{})
	type writtenPacket struct {
		rtpSequence       uint16
		transportSequence uint16
	}
	written := make(chan writtenPacket, 3)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		var extension rtp.TransportCCExtension
		if err := extension.Unmarshal(header.GetExtension(1)); err != nil {
			return 0, err
		}
		written <- writtenPacket{
			rtpSequence:       header.SequenceNumber,
			transportSequence: extension.TransportSequence,
		}
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	pacer.setTransportCCExtension(10, 1, true)
	pacer.setTransportCCExtension(11, 1, true)
	header := func(ssrc uint32, rtpSequence, transportSequence uint16) *rtp.Header {
		result := &rtp.Header{SSRC: ssrc, SequenceNumber: rtpSequence, Marker: ssrc == 10}
		extension, err := (rtp.TransportCCExtension{TransportSequence: transportSequence}).Marshal()
		if err != nil {
			t.Fatalf("marshal transport sequence: %v", err)
		}
		if err := result.SetExtension(1, extension); err != nil {
			t.Fatalf("set transport sequence: %v", err)
		}
		return result
	}
	if _, err := pacer.Write(header(10, 1, 100), []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first packet")
	}
	if _, err := pacer.Write(header(10, 2, 101), []byte{2}, nil); err != nil {
		t.Fatalf("queue second primary packet: %v", err)
	}
	if _, err := pacer.Write(header(11, 3, 102), []byte{3}, nil); err != nil {
		t.Fatalf("queue repair packet: %v", err)
	}
	close(release)
	var order []writtenPacket
	for range 3 {
		select {
		case packet := <-written:
			order = append(order, packet)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced packets")
		}
	}
	if order[0].rtpSequence != 1 || order[1].rtpSequence != 3 || order[2].rtpSequence != 2 {
		t.Fatalf("RTP packet order = %v, want [1 3 2]", order)
	}
	for index, packet := range order {
		if packet.transportSequence != uint16(index) {
			t.Fatalf("transport sequence at index %d = %d, want %d", index, packet.transportSequence, index)
		}
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerCoalescesPendingAndSuppressesRecentRetransmissions(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 64)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint32, 3)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SSRC
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, Marker: true}, []byte{1}, nil); err != nil {
		t.Fatalf("queue primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the primary packet")
	}
	const writers = 32
	start := make(chan struct{})
	var done sync.WaitGroup
	done.Add(writers)
	for range writers {
		go func() {
			defer done.Done()
			<-start
			if _, err := pacer.Write(
				&rtp.Header{SSRC: 11},
				[]byte{0x12, 0x34, 1},
				nil,
			); err != nil {
				t.Errorf("queue duplicate retransmission: %v", err)
			}
		}()
	}
	close(start)
	done.Wait()
	if got := pacer.Stats()["pacerRetransmissionPacketsCoalesced"]; got != uint64(writers-1) {
		t.Fatalf("coalesced retransmissions = %v, want %d", got, writers-1)
	}
	close(release)
	for _, expected := range []uint32{10, 11} {
		select {
		case actual := <-written:
			if actual != expected {
				t.Fatalf("written SSRC = %d, want %d", actual, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for SSRC %d", expected)
		}
	}
	retransmission := retransmissionKey{ssrc: 11, originalSequence: 0x1234}
	deadline := time.Now().Add(time.Second)
	for {
		pacer.retransmissionMu.Lock()
		_, delivered := pacer.recentRetransmissions[retransmission]
		pacer.retransmissionMu.Unlock()
		if delivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("paced retransmission was not recorded as delivered")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := pacer.Write(
		&rtp.Header{SSRC: 11},
		[]byte{0x12, 0x34, 2},
		nil,
	); err != nil {
		t.Fatalf("suppress retransmission after prior delivery: %v", err)
	}
	select {
	case actual := <-written:
		t.Fatalf("recent duplicate retransmission was written for SSRC %d", actual)
	case <-time.After(25 * time.Millisecond):
	}
	if got := pacer.Stats()["pacerRetransmissionPacketsSuppressed"]; got != uint64(1) {
		t.Fatalf("suppressed retransmissions = %v, want 1", got)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerRetransmissionWindowFollowsObservedRTT(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	key := retransmissionKey{ssrc: 11, originalSequence: 0x1234}
	start := time.Unix(100, 0)
	pacer.observeRoundTripTime(60 * time.Millisecond)
	if got := pacer.reserveRetransmissionAt(key, start); got != retransmissionReserved {
		t.Fatalf("initial reservation = %v, want reserved", got)
	}
	pacer.markRetransmissionSent(key, start)
	if got := pacer.reserveRetransmissionAt(key, start.Add(65*time.Millisecond-time.Nanosecond)); got != retransmissionRecentlySent {
		t.Fatalf("reservation before retry window = %v, want recently sent", got)
	}
	if got := pacer.reserveRetransmissionAt(key, start.Add(65*time.Millisecond)); got != retransmissionReserved {
		t.Fatalf("reservation at retry window = %v, want reserved", got)
	}
	pacer.releaseRetransmission(key)
	if got := pacer.Stats()["pacerRetransmissionMinimumIntervalMilliseconds"]; got != float64(65) {
		t.Fatalf("minimum retransmission interval = %v, want 65ms", got)
	}
}

func TestTokenBucketPacerSmoothsAndBoundsObservedRTT(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	pacer.observeRoundTripTime(60 * time.Millisecond)
	pacer.observeRoundTripTime(140 * time.Millisecond)
	if got := pacer.retransmissionRTT(); got != 70*time.Millisecond {
		t.Fatalf("smoothed RTT = %v, want 70ms", got)
	}
	pacer.observeRoundTripTime(-time.Millisecond)
	pacer.observeRoundTripTime(maximumObservedRTT + time.Nanosecond)
	if got := pacer.retransmissionRTT(); got != 70*time.Millisecond {
		t.Fatalf("RTT after invalid observations = %v, want 70ms", got)
	}
}

func TestTokenBucketPacerDoesNotThrottleAnUnsentRetransmission(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	key := retransmissionKey{ssrc: 11, originalSequence: 0x1234}
	now := time.Unix(100, 0)
	if got := pacer.reserveRetransmissionAt(key, now); got != retransmissionReserved {
		t.Fatalf("initial reservation = %v, want reserved", got)
	}
	pacer.releaseRetransmission(key)
	if got := pacer.reserveRetransmissionAt(key, now); got != retransmissionReserved {
		t.Fatalf("reservation after unsent release = %v, want reserved", got)
	}
	pacer.releaseRetransmission(key)
}

func TestTokenBucketPacerPrunesCompletedRetransmissionWindows(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	t.Cleanup(func() {
		if err := pacer.Close(); err != nil {
			t.Errorf("close pacer: %v", err)
		}
	})
	start := time.Unix(100, 0)
	for sequence := range uint16(128) {
		key := retransmissionKey{ssrc: 11, originalSequence: sequence}
		if got := pacer.reserveRetransmissionAt(key, start); got != retransmissionReserved {
			t.Fatalf("reservation %d = %v, want reserved", sequence, got)
		}
		pacer.markRetransmissionSent(key, start)
	}
	trigger := retransmissionKey{ssrc: 11, originalSequence: 129}
	if got := pacer.reserveRetransmissionAt(trigger, start.Add(2*time.Second)); got != retransmissionReserved {
		t.Fatalf("prune-trigger reservation = %v, want reserved", got)
	}
	pacer.retransmissionMu.Lock()
	recent := len(pacer.recentRetransmissions)
	pacer.retransmissionMu.Unlock()
	if recent != 0 {
		t.Fatalf("retained completed retransmission windows = %d, want 0", recent)
	}
	pacer.releaseRetransmission(trigger)
}

func TestTokenBucketPacerRepairsLossBeforeAReceiverReorderWindowCanOverflow(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 3)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Timestamp: 100}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first primary packet")
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 2, Timestamp: 100, Marker: true}, []byte{2}, nil); err != nil {
		t.Fatalf("queue final primary packet: %v", err)
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 11, SequenceNumber: 10, Timestamp: 100}, []byte{3}, nil); err != nil {
		t.Fatalf("queue repair packet: %v", err)
	}
	close(release)
	order := make([]uint16, 0, 3)
	for range 3 {
		select {
		case sequence := <-written:
			order = append(order, sequence)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced packets")
		}
	}
	want := []uint16{1, 10, 2}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("packet order = %v, want %v", order, want)
		}
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerSendsFECImmediatelyAfterItsProtectedGroup(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 32)
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 1}
	pacer.configureForwardErrorCorrection(protection)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, protection.mediaPackets+2)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(12, writer)
	pacer.markForwardErrorCorrectionStream(12)
	if _, err := pacer.Write(&rtp.Header{
		SSRC:           10,
		SequenceNumber: 1,
		Timestamp:      100,
	}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first primary packet")
	}
	for sequence := uint16(2); sequence <= uint16(protection.mediaPackets)+1; sequence++ {
		if _, err := pacer.Write(&rtp.Header{
			SSRC:           10,
			SequenceNumber: sequence,
			Timestamp:      100,
			Marker:         sequence == uint16(protection.mediaPackets)+1,
		}, []byte{byte(sequence)}, nil); err != nil {
			t.Fatalf("queue primary packet %d: %v", sequence, err)
		}
	}
	if _, err := pacer.Write(&rtp.Header{
		SSRC:           12,
		SequenceNumber: 100,
		Timestamp:      100,
	}, []byte{100}, nil); err != nil {
		t.Fatalf("queue FEC packet: %v", err)
	}
	close(release)
	order := make([]uint16, 0, protection.mediaPackets+2)
	for range protection.mediaPackets + 2 {
		select {
		case sequence := <-written:
			order = append(order, sequence)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced packets")
		}
	}
	want := []uint16{1, 2, 3, 4, 5, 100, 6}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("packet order = %v, want %v", order, want)
		}
	}
	stats := pacer.Stats()
	if sent, _ := stats["pacerSentForwardErrorCorrection"].(uint64); sent != 1 {
		t.Fatalf("sent FEC packets = %d, want 1", sent)
	}
	if sent, _ := stats["pacerSentForwardErrorCorrectionBytes"].(uint64); sent == 0 {
		t.Fatal("sent FEC bytes = 0, want a positive wire byte count")
	}
	if sent, _ := stats["pacerSentRetransmission"].(uint64); sent != 0 {
		t.Fatalf("sent RTX packets = %d, want 0", sent)
	}
	if maximum, _ := stats["pacerMaximumForwardErrorCorrectionResidenceMilliseconds"].(float64); maximum <= 0 {
		t.Fatalf("maximum FEC residence = %.3fms, want positive", maximum)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerSendsEveryFECRepairBeforeTheNextPrimaryPacket(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 32)
	protection := flexFECProtection{mediaPackets: 5, repairPackets: 2}
	pacer.configureForwardErrorCorrection(protection)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 8)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(12, writer)
	pacer.markForwardErrorCorrectionStream(12)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Timestamp: 100}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first primary packet")
	}
	for sequence := uint16(2); sequence <= 6; sequence++ {
		if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: sequence, Timestamp: 100, Marker: sequence == 6}, []byte{byte(sequence)}, nil); err != nil {
			t.Fatalf("queue primary packet %d: %v", sequence, err)
		}
	}
	for _, sequence := range []uint16{100, 101} {
		if _, err := pacer.Write(&rtp.Header{SSRC: 12, SequenceNumber: sequence, Timestamp: 100}, []byte{byte(sequence)}, nil); err != nil {
			t.Fatalf("queue FEC packet %d: %v", sequence, err)
		}
	}
	close(release)
	order := make([]uint16, 0, 8)
	for range 8 {
		select {
		case sequence := <-written:
			order = append(order, sequence)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced packets")
		}
	}
	want := []uint16{1, 2, 3, 4, 5, 100, 101, 6}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("packet order = %v, want %v", order, want)
		}
	}
	if sent, _ := pacer.Stats()["pacerSentForwardErrorCorrection"].(uint64); sent != 2 {
		t.Fatalf("sent FEC packets = %d, want 2", sent)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerResetsFECGroupAfterRateDecreaseTrimsRepair(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 32)
	pacer.configureForwardErrorCorrection(flexFECProtection{mediaPackets: 5, repairPackets: 2})
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 7)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(12, writer)
	pacer.markForwardErrorCorrectionStream(12)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Timestamp: 100}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first primary packet")
	}
	for sequence := uint16(2); sequence <= 7; sequence++ {
		if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: sequence, Timestamp: 100, Marker: sequence == 7}, []byte{byte(sequence)}, nil); err != nil {
			t.Fatalf("queue primary packet %d: %v", sequence, err)
		}
	}
	for _, sequence := range []uint16{100, 101} {
		if _, err := pacer.Write(&rtp.Header{SSRC: 12, SequenceNumber: sequence, Timestamp: 100}, []byte{byte(sequence)}, nil); err != nil {
			t.Fatalf("queue FEC packet %d: %v", sequence, err)
		}
	}
	pacer.SetTargetBitrate(5_000_000)
	close(release)
	for index := range 7 {
		select {
		case sequence := <-written:
			if sequence != uint16(index+1) {
				t.Fatalf("written primary packet %d = %d, want %d", index, sequence, index+1)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for primary packet %d", index+1)
		}
	}
	select {
	case sequence := <-written:
		t.Fatalf("trimmed FEC packet %d was written", sequence)
	case <-time.After(10 * time.Millisecond):
	}
	if trimmed, _ := pacer.Stats()["pacerForwardErrorCorrectionPacketsTrimmed"].(uint64); trimmed != 2 {
		t.Fatalf("trimmed FEC packets = %d, want 2", trimmed)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerDoesNotStarvePrimaryDuringRepairBurst(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 7)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Marker: true}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first packet")
	}
	for _, sequence := range []uint16{2, 3} {
		if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: sequence, Marker: true}, []byte{1}, nil); err != nil {
			t.Fatalf("queue primary packet %d: %v", sequence, err)
		}
	}
	for _, sequence := range []uint16{10, 11, 12, 13} {
		if _, err := pacer.Write(&rtp.Header{SSRC: 11, SequenceNumber: sequence}, []byte{1}, nil); err != nil {
			t.Fatalf("queue repair packet %d: %v", sequence, err)
		}
	}
	close(release)
	order := make([]uint16, 0, 7)
	for range 7 {
		select {
		case sequence := <-written:
			order = append(order, sequence)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced packets")
		}
	}
	wantPrefix := []uint16{1, 10, 2, 11, 3}
	for index, want := range wantPrefix {
		if order[index] != want {
			t.Fatalf("packet order = %v, want prefix %v", order, wantPrefix)
		}
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerExpiresRepairThatCanNoLongerMeetLatencyBudget(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 3)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Marker: true}, []byte{1}, nil); err != nil {
		t.Fatalf("queue first primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the first packet")
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 11, SequenceNumber: 10}, []byte{1}, nil); err != nil {
		t.Fatalf("queue repair packet: %v", err)
	}
	repair := <-pacer.retransmissionQueue
	repair.enqueuedAt = time.Now().Add(-maximumRepairResidence)
	pacer.retransmissionQueue <- repair
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 2, Marker: true}, []byte{1}, nil); err != nil {
		t.Fatalf("queue second primary packet: %v", err)
	}
	close(release)
	for index, want := range []uint16{1, 2} {
		select {
		case actual := <-written:
			if actual != want {
				t.Fatalf("written packet %d = %d, want %d", index, actual, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for written packet %d", index)
		}
	}
	select {
	case unexpected := <-written:
		t.Fatalf("expired repair packet %d was written", unexpected)
	case <-time.After(10 * time.Millisecond):
	}
	if expired, ok := pacer.Stats()["pacerRepairPacketsExpired"].(uint64); !ok || expired != 1 {
		t.Fatalf("expired repair packets = %v, want 1", pacer.Stats()["pacerRepairPacketsExpired"])
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerTrimsQueuedRepairImmediatelyAfterRateDecrease(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	entered := make(chan struct{})
	release := make(chan struct{})
	written := make(chan uint16, 2)
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		written <- header.SequenceNumber
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, SequenceNumber: 1, Marker: true}, []byte{1}, nil); err != nil {
		t.Fatalf("queue primary packet: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the primary packet")
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 11, SequenceNumber: 10}, []byte{1}, nil); err != nil {
		t.Fatalf("queue repair packet: %v", err)
	}
	pacer.SetTargetBitrate(2_000_000)
	close(release)
	select {
	case sequence := <-written:
		if sequence != 1 {
			t.Fatalf("written sequence = %d, want primary sequence 1", sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("primary packet was not written")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if trimmed, _ := pacer.Stats()["pacerRepairPacketsTrimmed"].(uint64); trimmed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trimmed repair packets = %v, want 1", pacer.Stats()["pacerRepairPacketsTrimmed"])
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case sequence := <-written:
		t.Fatalf("trimmed repair packet %d was written", sequence)
	case <-time.After(10 * time.Millisecond):
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerRemovesTWCCFromUntrackedRepair(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	written := make(chan bool, 1)
	pacer.AddStream(11, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		written <- header.GetExtension(1) != nil
		return header.MarshalSize() + len(payload), nil
	}))
	pacer.markRetransmissionStream(11)
	pacer.setTransportCCExtension(11, 1, false)
	header := &rtp.Header{SSRC: 11}
	extension, err := (rtp.TransportCCExtension{TransportSequence: 42}).Marshal()
	if err != nil {
		t.Fatalf("marshal transport sequence: %v", err)
	}
	if err := header.SetExtension(1, extension); err != nil {
		t.Fatalf("set transport sequence: %v", err)
	}
	if _, err := pacer.Write(header, []byte{1}, nil); err != nil {
		t.Fatalf("queue untracked repair: %v", err)
	}
	select {
	case retained := <-written:
		if retained {
			t.Fatal("untracked repair retained a TWCC extension")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for untracked repair")
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerPropagatesAsynchronousWriteError(t *testing.T) {
	expected := errors.New("transport failed")
	pacer := newTokenBucketPacer(10_000_000, 1, 16)
	failed := make(chan struct{})
	var failedOnce sync.Once
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		_ *rtp.Header,
		_ []byte,
		_ interceptor.Attributes,
	) (int, error) {
		failedOnce.Do(func() { close(failed) })
		return 0, expected
	}))
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{1}, nil); err != nil {
		t.Fatalf("queue packet: %v", err)
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("writer did not report its failure")
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{2}, nil)
		if errors.Is(err, expected) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("asynchronous error was not propagated, last error: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if sent, ok := pacer.Stats()["pacerSentPrimary"].(uint64); !ok || sent != 0 {
		t.Fatalf("successfully sent primary packets = %v, want 0", sent)
	}
	if err := pacer.Close(); !errors.Is(err, expected) {
		t.Fatalf("close error = %v, want transport error", err)
	}
}

func TestTokenBucketPacerPreservesPacketizedFramesAtAdmissionRateAfterDecrease(t *testing.T) {
	pacer := newTokenBucketPacer(3_000_000, 1, 64)
	started := make(chan struct{})
	releaseWriter := make(chan struct{})
	var startOnce sync.Once
	var sentMu sync.Mutex
	var sentTimestamps []uint32
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		_ []byte,
		_ interceptor.Attributes,
	) (int, error) {
		startOnce.Do(func() {
			close(started)
			<-releaseWriter
		})
		sentMu.Lock()
		sentTimestamps = append(sentTimestamps, header.Timestamp)
		sentMu.Unlock()
		return header.MarshalSize() + 1200, nil
	}))
	for timestamp := uint32(1); timestamp <= 6; timestamp++ {
		decision := pacer.AdmitMediaFrame(8*1200, timestamp == 1)
		if !decision.admitted {
			t.Fatalf("frame %d was rejected before rate decrease", timestamp)
		}
		for sequence := uint16(0); sequence < 8; sequence++ {
			if _, err := pacer.Write(&rtp.Header{
				SSRC:           10,
				SequenceNumber: uint16(timestamp*8) + sequence,
				Timestamp:      timestamp,
				Marker:         sequence == 7,
			}, make([]byte, 1200), nil); err != nil {
				t.Fatalf("enqueue frame %d packet %d: %v", timestamp, sequence, err)
			}
		}
		decision.completePacketization()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pacer did not start the protected frame")
	}
	pacer.SetTargetBitrate(80_000)
	decision := pacer.AdmitMediaFrame(1000, false)
	decision.completePacketization()
	if decision.admitted || !decision.requestKeyFrame {
		t.Fatalf("post-decrease delta frame decision = %+v, want pre-packetization recovery", decision)
	}
	close(releaseWriter)
	deadline := time.Now().Add(2 * time.Second)
	for len(pacer.queueSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
	sentMu.Lock()
	defer sentMu.Unlock()
	if len(sentTimestamps) == 0 {
		t.Fatal("packetized frames were not sent")
	}
	seen := make(map[uint32]int)
	for _, timestamp := range sentTimestamps {
		seen[timestamp]++
	}
	for timestamp := uint32(1); timestamp <= 6; timestamp++ {
		if seen[timestamp] != 8 {
			t.Fatalf("sent packets for timestamp %d = %d, want 8", timestamp, seen[timestamp])
		}
	}
	if dropped, _ := pacer.Stats()["pacerMediaFramesDropped"].(uint64); dropped != 1 {
		t.Fatalf("pre-packetization frame drops = %d, want 1", dropped)
	}
	if maximum, _ := pacer.Stats()["pacerMaximumPrimaryResidenceMilliseconds"].(float64); maximum > 300 {
		t.Fatalf("maximum primary residence = %.1fms, want at most 300ms", maximum)
	}
}

func TestTokenBucketPacerPreservesAdmissionRateWhenTargetChangesDuringPacketization(t *testing.T) {
	pacer := newTokenBucketPacer(3_000_000, 1, 32)
	written := make(chan time.Time, 10)
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		written <- time.Now()
		return header.MarshalSize() + len(payload), nil
	}))
	decision := pacer.AdmitMediaFrame(10*1200, true)
	if !decision.admitted {
		t.Fatalf("frame admission = %+v, want admitted", decision)
	}
	pacer.SetTargetBitrate(80_000)
	for sequence := uint16(0); sequence < 10; sequence++ {
		if _, err := pacer.Write(&rtp.Header{
			SSRC:           10,
			SequenceNumber: sequence,
			Timestamp:      1,
			Marker:         sequence == 9,
		}, make([]byte, 1200), nil); err != nil {
			t.Fatalf("packetize packet %d: %v", sequence, err)
		}
	}
	decision.completePacketization()
	var first time.Time
	var last time.Time
	for index := range 10 {
		select {
		case sentAt := <-written:
			if index == 0 {
				first = sentAt
			}
			last = sentAt
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for packet %d", index)
		}
	}
	if elapsed := last.Sub(first); elapsed > 200*time.Millisecond {
		t.Fatalf("admitted frame took %v after target decrease, want at most 200ms", elapsed)
	}
	decision.completePacketization()
	if next := admitMediaFrame(pacer, 100, true); !next.admitted {
		t.Fatalf("admission lock remained held after idempotent completion: %+v", next)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerAdmitsAgainstScheduledMixedRateBacklog(t *testing.T) {
	pacer := newTokenBucketPacer(3_000_000, 1, 32)
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		return header.MarshalSize() + len(payload), nil
	}))
	decision := pacer.AdmitMediaFrame(8*1200, true)
	if !decision.admitted {
		t.Fatalf("old-rate frame admission = %+v, want admitted", decision)
	}
	for sequence := uint16(0); sequence < 8; sequence++ {
		if _, err := pacer.Write(&rtp.Header{
			SSRC:           10,
			SequenceNumber: sequence,
			Timestamp:      1,
			Marker:         sequence == 7,
		}, make([]byte, 1200), nil); err != nil {
			t.Fatalf("packetize old-rate packet %d: %v", sequence, err)
		}
	}
	decision.completePacketization()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the old-rate frame")
	}
	pacer.SetTargetBitrate(80_000)
	decision = pacer.AdmitMediaFrame(1_000, false)
	decision.completePacketization()
	if !decision.admitted || decision.requestKeyFrame {
		t.Fatalf("mixed-rate backlog decision = %+v, want admitted", decision)
	}
	close(release)
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerBoundsRepairWorkAheadOfNewMedia(t *testing.T) {
	pacer := newTokenBucketPacer(3_000_000, 1, 256)
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	writer := interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		return header.MarshalSize() + len(payload), nil
	})
	pacer.AddStream(10, writer)
	pacer.AddStream(11, writer)
	pacer.markRetransmissionStream(11)
	decision := pacer.AdmitMediaFrame(1_200, true)
	if !decision.admitted {
		t.Fatalf("leading media admission = %+v, want admitted", decision)
	}
	if _, err := pacer.Write(&rtp.Header{SSRC: 10, Timestamp: 1, Marker: true}, make([]byte, 1200), nil); err != nil {
		t.Fatalf("queue leading media: %v", err)
	}
	decision.completePacketization()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the leading media")
	}
	for sequence := uint16(0); sequence < 100; sequence++ {
		payload := make([]byte, 1200)
		payload[0] = byte(sequence >> 8)
		payload[1] = byte(sequence)
		if _, err := pacer.Write(&rtp.Header{SSRC: 11, SequenceNumber: sequence}, payload, nil); err != nil {
			t.Fatalf("queue repair packet %d: %v", sequence, err)
		}
	}
	if backlog := pacer.scheduledQueueDelay(); backlog <= maximumMediaAdmissionDelay {
		t.Fatalf("total scheduled backlog = %v, want above admission budget", backlog)
	}
	decision = pacer.AdmitMediaFrame(10_000, false)
	decision.completePacketization()
	if !decision.admitted || decision.requestKeyFrame {
		t.Fatalf("repair-bounded media decision = %+v, want admitted", decision)
	}
	close(release)
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
}

func TestTokenBucketPacerConcurrentWritesRatesAndClose(t *testing.T) {
	pacer := newTokenBucketPacer(10_000_000, 1.2, 4096)
	pacer.AddStream(10, interceptor.RTPWriterFunc(func(
		header *rtp.Header,
		payload []byte,
		_ interceptor.Attributes,
	) (int, error) {
		return header.MarshalSize() + len(payload), nil
	}))
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers + 1)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for range 100 {
				decision := pacer.AdmitMediaFrame(512, false)
				if !decision.admitted {
					decision.completePacketization()
					continue
				}
				_, err := pacer.Write(&rtp.Header{SSRC: 10}, make([]byte, 512), nil)
				decision.completePacketization()
				if err != nil && !errors.Is(err, errPacerClosed) && !errors.Is(err, errPacerQueueFull) {
					t.Errorf("concurrent write: %v", err)
					return
				}
			}
		}()
	}
	go func() {
		defer wait.Done()
		for bitrate := 100_000; bitrate <= 5_000_000; bitrate += 100_000 {
			pacer.SetTargetBitrate(bitrate)
		}
	}()
	time.Sleep(time.Millisecond)
	if err := pacer.Close(); err != nil {
		t.Fatalf("close pacer: %v", err)
	}
	wait.Wait()
	if _, err := pacer.Write(&rtp.Header{SSRC: 10}, []byte{1}, nil); !errors.Is(err, errPacerClosed) {
		t.Fatalf("write after close error = %v, want closed error", err)
	}
	if err := pacer.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func admitMediaFrame(
	pacer *tokenBucketPacer,
	size int,
	keyFrame bool,
) mediaFrameAdmission {
	decision := pacer.AdmitMediaFrame(size, keyFrame)
	decision.completePacketization()
	return decision
}

func setSyntheticQueuedBytes(pacer *tokenBucketPacer, bytes int64) {
	pacer.queuedPrimaryServiceNs.Store(
		queueDelayAtRate(bytes, pacer.sustainedBytesPerSecond()).Nanoseconds(),
	)
}
