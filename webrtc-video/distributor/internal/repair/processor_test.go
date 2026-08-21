package repair

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestTrackerHandlesSequenceWrapAndReorderingWithoutNACK(t *testing.T) {
	config := DefaultConfig()
	tracker := tracker{config: config, missing: make(map[uint64]missingPacket)}
	stats := Stats{}
	started := time.Unix(100, 0)
	for _, packet := range []Packet{
		packetAt(65534, started),
		packetAt(0, started.Add(time.Millisecond)),
		packetAt(65535, started.Add(2*time.Millisecond)),
	} {
		tracker.observe(packet, &stats)
	}
	if len(tracker.missing) != 0 || stats.NACKCandidates != 1 || stats.ReorderedBeforeNACK != 1 || stats.NACKRequests != 0 {
		t.Fatalf("unexpected wraparound stats: %+v, pending=%d", stats, len(tracker.missing))
	}
	if tracker.highest != sequenceSpace {
		t.Fatalf("extended highest = %d, want %d", tracker.highest, sequenceSpace)
	}
}

func TestTrackerBoundsNACKRetriesAndClassifiesRTX(t *testing.T) {
	config := DefaultConfig()
	config.MinNACKDelay = 10 * time.Millisecond
	config.MaxNACKDelay = 20 * time.Millisecond
	config.NACKRetry = 10 * time.Millisecond
	config.PacketExpiry = 100 * time.Millisecond
	config.MaxNACKs = 2
	tracker := tracker{config: config, missing: make(map[uint64]missingPacket)}
	stats := Stats{}
	started := time.Unix(100, 0)
	tracker.observe(packetAt(10, started), &stats)
	tracker.observe(packetAt(12, started.Add(time.Millisecond)), &stats)
	if got := tracker.due(started.Add(9*time.Millisecond), &stats); len(got) != 0 {
		t.Fatalf("NACK sent before reorder delay: %v", got)
	}
	if got := tracker.due(started.Add(12*time.Millisecond), &stats); len(got) != 1 || got[0] != 11 {
		t.Fatalf("first NACK = %v, want [11]", got)
	}
	if got := tracker.due(started.Add(23*time.Millisecond), &stats); len(got) != 1 || got[0] != 11 {
		t.Fatalf("second NACK = %v, want [11]", got)
	}
	_, _, repair, duplicate := tracker.observe(Packet{RTP: rtpPacket(11), ReceivedAt: started.Add(25 * time.Millisecond), RecoveredRTX: true}, &stats)
	if len(tracker.missing) != 0 || repair != repairRTX || duplicate || stats.RepairedRTX != 0 || stats.NACKRequests != 2 {
		t.Fatalf("unexpected RTX repair stats: %+v, pending=%d", stats, len(tracker.missing))
	}
	tracker.observe(Packet{RTP: rtpPacket(11), ReceivedAt: started.Add(26 * time.Millisecond), RecoveredRTX: true}, &stats)
	if stats.DuplicateRTX != 1 {
		t.Fatalf("duplicate RTX count = %d, want 1", stats.DuplicateRTX)
	}
}

func TestTrackerClassifiesFlexFECRecoveryAndDuplicate(t *testing.T) {
	config := DefaultConfig()
	tracker := tracker{config: config, missing: make(map[uint64]missingPacket)}
	stats := Stats{}
	started := time.Unix(100, 0)
	tracker.observe(packetAt(20, started), &stats)
	tracker.observe(packetAt(22, started.Add(time.Millisecond)), &stats)
	_, _, repair, duplicate := tracker.observe(Packet{RTP: rtpPacket(21), ReceivedAt: started.Add(2 * time.Millisecond), RecoveredFEC: true}, &stats)
	if len(tracker.missing) != 0 || repair != repairFEC || duplicate || stats.RepairedFEC != 0 || stats.RepairedRTX != 0 {
		t.Fatalf("unexpected FlexFEC repair stats: %+v, pending=%d", stats, len(tracker.missing))
	}
	tracker.observe(Packet{RTP: rtpPacket(21), ReceivedAt: started.Add(3 * time.Millisecond), RecoveredFEC: true}, &stats)
	if stats.DuplicateFEC != 1 {
		t.Fatalf("duplicate FlexFEC count = %d, want 1", stats.DuplicateFEC)
	}
}

func TestProcessorDropsDuplicateMediaAndRepairCandidatesBeforeReordering(t *testing.T) {
	config := DefaultConfig()
	instance := processor{config: config, tracker: tracker{config: config}, reorder: reorderBuffer{config: config}}
	emitted := make([]uint16, 0, 1)
	emit := func(packet *rtp.Packet) error {
		emitted = append(emitted, packet.SequenceNumber)
		return nil
	}
	started := time.Unix(100, 0)
	for _, packet := range []Packet{
		packetAt(10, started),
		packetAt(10, started.Add(time.Millisecond)),
		{RTP: rtpPacket(10), ReceivedAt: started.Add(2 * time.Millisecond), RecoveredFEC: true},
		{RTP: rtpPacket(10), ReceivedAt: started.Add(3 * time.Millisecond), RecoveredRTX: true},
	} {
		if err := instance.handlePacket(packet, emit); err != nil {
			t.Fatalf("handle packet: %v", err)
		}
	}
	if !slices.Equal(emitted, []uint16{10}) {
		t.Fatalf("emitted packets = %v, want [10]", emitted)
	}
	if instance.stats.Duplicates != 1 || instance.stats.DuplicateFEC != 1 || instance.stats.DuplicateRTX != 1 || instance.stats.ReorderLate != 0 {
		t.Fatalf("duplicate stats = %+v", instance.stats)
	}
}

func TestProcessorCountsOnlyRepairsDeliveredInsideTheReorderWindow(t *testing.T) {
	config := DefaultConfig()
	instance := processor{config: config, tracker: tracker{config: config}, reorder: reorderBuffer{config: config}}
	emitted := make([]uint16, 0, 3)
	emit := func(packet *rtp.Packet) error {
		emitted = append(emitted, packet.SequenceNumber)
		return nil
	}
	started := time.Unix(100, 0)
	for _, packet := range []Packet{
		packetAt(10, started),
		packetAt(12, started.Add(time.Millisecond)),
		{RTP: rtpPacket(11), ReceivedAt: started.Add(2 * time.Millisecond), RecoveredRTX: true},
	} {
		if err := instance.handlePacket(packet, emit); err != nil {
			t.Fatalf("handle packet: %v", err)
		}
	}
	if instance.stats.RepairedRTX != 1 || instance.stats.LateRTX != 0 || !slices.Equal(emitted, []uint16{10, 11, 12}) {
		t.Fatalf("in-window repair stats = %+v, emitted=%v", instance.stats, emitted)
	}
	late := processor{config: config, tracker: tracker{config: config}, reorder: reorderBuffer{config: config}}
	emitted = emitted[:0]
	if err := late.handlePacket(packetAt(20, started), emit); err != nil {
		t.Fatalf("handle first packet: %v", err)
	}
	if err := late.handlePacket(packetAt(22, started.Add(time.Millisecond)), emit); err != nil {
		t.Fatalf("handle post-gap packet: %v", err)
	}
	if err := late.reorder.flushExpired(started.Add(config.ReorderWait+time.Millisecond), &late.stats, emit); err != nil {
		t.Fatalf("flush reorder window: %v", err)
	}
	if err := late.handlePacket(Packet{RTP: rtpPacket(21), ReceivedAt: started.Add(config.ReorderWait + 2*time.Millisecond), RecoveredFEC: true}, emit); err != nil {
		t.Fatalf("handle late repair: %v", err)
	}
	if late.stats.RepairedFEC != 0 || late.stats.LateFEC != 1 || late.stats.ReorderLate != 1 || !slices.Equal(emitted, []uint16{20, 22}) {
		t.Fatalf("late repair stats = %+v, emitted=%v", late.stats, emitted)
	}
}

func TestTrackerAdaptsNACKDelayToObservedReorderingWithinBounds(t *testing.T) {
	config := DefaultConfig()
	tracker := tracker{config: config, missing: make(map[uint64]missingPacket)}
	stats := Stats{}
	started := time.Unix(100, 0)
	tracker.observe(packetAt(1, started), &stats)
	tracker.observe(packetAt(3, started.Add(time.Millisecond)), &stats)
	tracker.observe(packetAt(2, started.Add(31*time.Millisecond)), &stats)
	if delay := tracker.nackDelay(); delay <= config.MinNACKDelay || delay > config.MaxNACKDelay {
		t.Fatalf("adaptive NACK delay = %s, want (%s, %s]", delay, config.MinNACKDelay, config.MaxNACKDelay)
	}
	tracker.observe(packetAt(5, started.Add(40*time.Millisecond)), &stats)
	if got := tracker.due(started.Add(40*time.Millisecond+config.MinNACKDelay), &stats); len(got) != 0 {
		t.Fatalf("adaptive delay sent an early NACK: %v", got)
	}
}

func TestProcessEmitsOrderedPacketsAndSkipsUnrepairedGapAfterBound(t *testing.T) {
	config := DefaultConfig()
	config.MinNACKDelay = 5 * time.Millisecond
	config.MaxNACKDelay = 10 * time.Millisecond
	config.NACKRetry = 5 * time.Millisecond
	config.PacketExpiry = 100 * time.Millisecond
	config.ReorderWait = 20 * time.Millisecond
	input := make(chan Packet, 3)
	emitted := make(chan uint16, 3)
	feedback := make(chan []uint16, 8)
	result := make(chan struct {
		stats Stats
		err   error
	}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		stats, err := Process(ctx, config, input, func(packet *rtp.Packet) error {
			emitted <- packet.SequenceNumber
			return nil
		}, func(sequences []uint16) error {
			copyOfSequences := append([]uint16(nil), sequences...)
			feedback <- copyOfSequences
			return nil
		})
		result <- struct {
			stats Stats
			err   error
		}{stats: stats, err: err}
	}()
	now := time.Now()
	input <- packetAt(20, now)
	input <- packetAt(22, now.Add(time.Millisecond))
	select {
	case first := <-emitted:
		if first != 20 {
			t.Fatalf("first packet = %d, want 20", first)
		}
	case <-time.After(time.Second):
		t.Fatal("ordered packet was not emitted")
	}
	select {
	case sequence := <-emitted:
		if sequence != 22 {
			t.Fatalf("post-gap packet = %d, want 22", sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("post-gap packet remained blocked after reorder bound")
	}
	close(input)
	completed := <-result
	if completed.err != nil {
		t.Fatalf("process packets: %v", completed.err)
	}
	if completed.stats.ReorderSkipped != 1 || completed.stats.NACKRequests == 0 {
		t.Fatalf("unexpected processor stats: %+v", completed.stats)
	}
	if len(feedback) == 0 {
		t.Fatal("missing packet did not produce feedback")
	}
}

func TestProcessDoesNotPostponeExpiredReorderDeadlineUnderSaturatedInput(t *testing.T) {
	config := DefaultConfig()
	config.MinNACKDelay = time.Nanosecond
	config.MaxNACKDelay = time.Nanosecond
	config.NACKRetry = time.Millisecond
	config.PacketExpiry = time.Second
	config.ReorderWait = time.Nanosecond
	config.MaxPending = 32
	input := make(chan Packet, 128)
	started := time.Now().Add(-time.Second)
	input <- packetAt(1, started)
	for sequence := uint16(3); sequence <= 100; sequence++ {
		input <- packetAt(sequence, started)
	}
	ctx, cancel := context.WithCancel(context.Background())
	emitted := make(chan uint16, 128)
	done := make(chan error, 1)
	go func() {
		_, err := Process(ctx, config, input, func(packet *rtp.Packet) error {
			emitted <- packet.SequenceNumber
			return nil
		}, func([]uint16) error { return nil })
		done <- err
	}()
	for {
		select {
		case sequence := <-emitted:
			if sequence == 100 {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("process error = %v, want cancellation", err)
				}
				return
			}
		case err := <-done:
			cancel()
			t.Fatalf("process stopped before draining saturated input: %v", err)
		case <-time.After(time.Second):
			cancel()
			t.Fatal("expired reorder deadline was postponed by saturated input")
		}
	}
}

func TestProcessCancellationAndFeedbackFailureTerminateOnce(t *testing.T) {
	config := DefaultConfig()
	config.MinNACKDelay = time.Millisecond
	config.MaxNACKDelay = time.Millisecond
	config.NACKRetry = time.Millisecond
	config.PacketExpiry = 20 * time.Millisecond
	config.ReorderWait = 20 * time.Millisecond
	input := make(chan Packet, 2)
	feedbackErr := errors.New("feedback unavailable")
	done := make(chan error, 1)
	go func() {
		_, err := Process(context.Background(), config, input, func(*rtp.Packet) error { return nil }, func([]uint16) error { return feedbackErr })
		done <- err
	}()
	now := time.Now()
	input <- packetAt(1, now)
	input <- packetAt(3, now.Add(time.Millisecond))
	select {
	case err := <-done:
		if !errors.Is(err, feedbackErr) {
			t.Fatalf("process error = %v, want feedback error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("feedback failure did not stop processor")
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelInput := make(chan Packet)
	cancelDone := make(chan error, 1)
	go func() {
		_, err := Process(cancelCtx, DefaultConfig(), cancelInput, func(*rtp.Packet) error { return nil }, func([]uint16) error { return nil })
		cancelDone <- err
	}()
	cancel()
	select {
	case err := <-cancelDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop processor")
	}
}

func TestProcessSupportsConcurrentCancellationAndInputClose(t *testing.T) {
	const iterations = 100
	for iteration := 0; iteration < iterations; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		input := make(chan Packet)
		done := make(chan struct{})
		go func() {
			_, _ = Process(ctx, DefaultConfig(), input, func(*rtp.Packet) error { return nil }, func([]uint16) error { return nil })
			close(done)
		}()
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			cancel()
		}()
		go func() {
			defer workers.Done()
			close(input)
		}()
		workers.Wait()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d did not terminate", iteration)
		}
	}
}

func TestProcessObservedPublishesLiveAndFinalSnapshots(t *testing.T) {
	input := make(chan Packet, 1)
	observed := make(chan Stats, 64)
	done := make(chan struct {
		stats Stats
		err   error
	}, 1)
	go func() {
		stats, err := ProcessObserved(
			context.Background(),
			DefaultConfig(),
			input,
			func(*rtp.Packet) error { return nil },
			func([]uint16) error { return nil },
			ObserverOptions{Interval: 5 * time.Millisecond, Observe: func(stats Stats) { observed <- stats }},
		)
		done <- struct {
			stats Stats
			err   error
		}{stats: stats, err: err}
	}()
	input <- packetAt(1, time.Now())
	select {
	case snapshot := <-observed:
		if snapshot.Received != 1 || snapshot.NACKDelay != DefaultConfig().MinNACKDelay {
			t.Fatalf("live snapshot = %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("live repair snapshot was not observed")
	}
	close(input)
	completed := <-done
	if completed.err != nil || completed.stats.Received != 1 {
		t.Fatalf("completed repair = stats %+v error %v", completed.stats, completed.err)
	}
	last := Stats{}
	for len(observed) > 0 {
		last = <-observed
	}
	if last != completed.stats {
		t.Fatalf("final snapshot = %+v, want %+v", last, completed.stats)
	}
}

func TestProcessObservedRejectsPartialObserverConfiguration(t *testing.T) {
	input := make(chan Packet)
	for _, options := range []ObserverOptions{
		{Interval: time.Second},
		{Observe: func(Stats) {}},
		{Interval: -time.Second, Observe: func(Stats) {}},
	} {
		_, err := ProcessObserved(
			context.Background(),
			DefaultConfig(),
			input,
			func(*rtp.Packet) error { return nil },
			func([]uint16) error { return nil },
			options,
		)
		if err == nil {
			t.Fatalf("observer configuration %+v was accepted", options)
		}
	}
}

func TestSequentialHotPathDoesNotAllocatePerPacket(t *testing.T) {
	config := DefaultConfig()
	instance := processor{config: config, tracker: tracker{config: config}, reorder: reorderBuffer{config: config}}
	packet := packetAt(0, time.Now())
	emit := func(*rtp.Packet) error { return nil }
	allocations := testing.AllocsPerRun(1000, func() {
		packet.RTP.SequenceNumber++
		packet.ReceivedAt = packet.ReceivedAt.Add(time.Microsecond)
		if err := instance.handlePacket(packet, emit); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("sequential packet allocations = %.2f, want 0", allocations)
	}
}

func packetAt(sequence uint16, receivedAt time.Time) Packet {
	return Packet{RTP: rtpPacket(sequence), ReceivedAt: receivedAt}
}

func rtpPacket(sequence uint16) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: sequence}, Payload: []byte{1}}
}
