package repair

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/pion/rtp"
)

const sequenceSpace = uint64(1 << 16)

type Config struct {
	MinNACKDelay time.Duration
	MaxNACKDelay time.Duration
	NACKRetry    time.Duration
	PacketExpiry time.Duration
	ReorderWait  time.Duration
	MaxMissing   int
	MaxPending   int
	MaxNACKs     uint16
	MaxNACKBatch int
}

type Packet struct {
	RTP          *rtp.Packet
	ReceivedAt   time.Time
	RecoveredRTX bool
	RecoveredFEC bool
	repair       repairClass
}

type repairClass uint8

const (
	repairNone repairClass = iota
	repairRTX
	repairFEC
)

type Stats struct {
	Received            uint64
	RTXReceived         uint64
	FECCandidates       uint64
	NACKCandidates      uint64
	ReorderedBeforeNACK uint64
	LateAfterNACK       uint64
	RepairedRTX         uint64
	RepairedFEC         uint64
	DuplicateRTX        uint64
	DuplicateFEC        uint64
	Duplicates          uint64
	NACKRequests        uint64
	Expired             uint64
	Discontinuities     uint64
	ReorderSkipped      uint64
	ReorderDiscarded    uint64
	ReorderLate         uint64
	LateRTX             uint64
	LateFEC             uint64
	ReorderPeak         int
	NACKDelay           time.Duration
}

type Emit func(*rtp.Packet) error

type Feedback func([]uint16) error

type ObserverOptions struct {
	Interval time.Duration
	Observe  func(Stats)
}

type processor struct {
	config  Config
	tracker tracker
	reorder reorderBuffer
	stats   Stats
}

type missingPacket struct {
	firstSeen   time.Time
	lastRequest time.Time
	requests    uint16
}

type tracker struct {
	config      Config
	initialized bool
	highest     uint64
	missing     map[uint64]missingPacket
	delay       delayEstimator
}

type delayEstimator struct {
	initialized bool
	mean        float64
	deviation   float64
}

type reorderBuffer struct {
	config      Config
	initialized bool
	expected    uint64
	pending     map[uint64]Packet
	gapStarted  time.Time
}

func DefaultConfig() Config {
	return Config{
		MinNACKDelay: 20 * time.Millisecond,
		MaxNACKDelay: 100 * time.Millisecond,
		NACKRetry:    50 * time.Millisecond,
		PacketExpiry: time.Second,
		ReorderWait:  300 * time.Millisecond,
		MaxMissing:   4096,
		MaxPending:   8192,
		MaxNACKs:     10,
		MaxNACKBatch: 200,
	}
}

func Process(ctx context.Context, config Config, input <-chan Packet, emit Emit, feedback Feedback) (Stats, error) {
	return ProcessObserved(ctx, config, input, emit, feedback, ObserverOptions{})
}

func ProcessObserved(ctx context.Context, config Config, input <-chan Packet, emit Emit, feedback Feedback, observer ObserverOptions) (Stats, error) {
	if err := config.validate(); err != nil {
		return Stats{}, err
	}
	if input == nil || emit == nil || feedback == nil {
		return Stats{}, errors.New("repair input, emit, and feedback are required")
	}
	if (observer.Observe == nil) != (observer.Interval == 0) || observer.Interval < 0 {
		return Stats{}, errors.New("repair observer and positive interval must be configured together")
	}
	instance := processor{
		config:  config,
		tracker: tracker{config: config},
		reorder: reorderBuffer{config: config},
	}
	return instance.run(ctx, input, emit, feedback, observer)
}

func (c Config) validate() error {
	if c.MinNACKDelay <= 0 || c.MaxNACKDelay < c.MinNACKDelay || c.NACKRetry <= 0 || c.PacketExpiry <= c.MinNACKDelay || c.ReorderWait <= 0 {
		return errors.New("repair durations are invalid")
	}
	if c.MaxMissing <= 0 || c.MaxMissing >= 1<<15 || c.MaxPending <= 0 || c.MaxNACKs == 0 || c.MaxNACKBatch <= 0 || c.MaxNACKBatch > c.MaxMissing {
		return errors.New("repair bounds are invalid")
	}
	return nil
}

func (p *processor) run(ctx context.Context, input <-chan Packet, emit Emit, feedback Feedback, observer ObserverOptions) (Stats, error) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var observationTicker *time.Ticker
	var observationC <-chan time.Time
	if observer.Observe != nil {
		observationTicker = time.NewTicker(observer.Interval)
		observationC = observationTicker.C
		defer observationTicker.Stop()
	}
	var timerC <-chan time.Time
	var timerDeadline time.Time
	for {
		deadline, scheduled := p.nextDeadline()
		if scheduled {
			if timerC == nil || deadline.Before(timerDeadline) {
				delay := time.Until(deadline)
				if delay < 0 {
					delay = 0
				}
				resetTimer(timer, delay)
				timerC = timer.C
				timerDeadline = deadline
			}
		} else if timerC != nil {
			stopTimer(timer)
			timerC = nil
			timerDeadline = time.Time{}
		}
		select {
		case <-ctx.Done():
			p.finishStats(observer.Observe)
			return p.stats, ctx.Err()
		case packet, ok := <-input:
			if !ok {
				p.finishStats(observer.Observe)
				return p.stats, nil
			}
			if err := p.handlePacket(packet, emit); err != nil {
				p.finishStats(observer.Observe)
				return p.stats, err
			}
		case now := <-timerC:
			timerC = nil
			timerDeadline = time.Time{}
			if err := p.handleDeadline(now, emit, feedback); err != nil {
				p.finishStats(observer.Observe)
				return p.stats, err
			}
		case <-observationC:
			observer.Observe(p.statsSnapshot())
		}
	}
}

func (p *processor) handlePacket(packet Packet, emit Emit) error {
	if packet.RTP == nil {
		return errors.New("received a nil RTP packet")
	}
	if packet.ReceivedAt.IsZero() {
		packet.ReceivedAt = time.Now()
	}
	p.stats.Received++
	if packet.RecoveredRTX {
		p.stats.RTXReceived++
	}
	if packet.RecoveredFEC {
		p.stats.FECCandidates++
	}
	extended, reset, repair, duplicate := p.tracker.observe(packet, &p.stats)
	if duplicate {
		return nil
	}
	packet.repair = repair
	if reset {
		p.reorder.reset(extended, &p.stats)
	}
	return p.reorder.push(extended, packet, &p.stats, emit)
}

func (p *processor) handleDeadline(now time.Time, emit Emit, feedback Feedback) error {
	sequences := p.tracker.due(now, &p.stats)
	if len(sequences) > 0 {
		if err := feedback(sequences); err != nil {
			return fmt.Errorf("send RTP feedback: %w", err)
		}
	}
	return p.reorder.flushExpired(now, &p.stats, emit)
}

func (p *processor) nextDeadline() (time.Time, bool) {
	trackerDeadline, trackerScheduled := p.tracker.nextDeadline()
	reorderDeadline, reorderScheduled := p.reorder.nextDeadline()
	if !trackerScheduled {
		return reorderDeadline, reorderScheduled
	}
	if !reorderScheduled || trackerDeadline.Before(reorderDeadline) {
		return trackerDeadline, true
	}
	return reorderDeadline, true
}

func (p *processor) finishStats(observe func(Stats)) {
	p.stats.NACKDelay = p.tracker.nackDelay()
	p.stats.ReorderPeak = max(p.stats.ReorderPeak, len(p.reorder.pending))
	p.stats.ReorderDiscarded += uint64(len(p.reorder.pending))
	if observe != nil {
		observe(p.stats)
	}
}

func (p *processor) statsSnapshot() Stats {
	stats := p.stats
	stats.NACKDelay = p.tracker.nackDelay()
	stats.ReorderPeak = max(stats.ReorderPeak, len(p.reorder.pending))
	return stats
}

func (t *tracker) observe(packet Packet, stats *Stats) (uint64, bool, repairClass, bool) {
	sequence := packet.RTP.SequenceNumber
	if !t.initialized {
		t.initialized = true
		t.highest = uint64(sequence)
		return t.highest, false, repairNone, false
	}
	extended := extendSequence(sequence, t.highest)
	reset := false
	advanced := false
	if extended > t.highest {
		advanced = true
		gap := extended - t.highest - 1
		if gap > uint64(t.config.MaxMissing) || len(t.missing)+int(gap) > t.config.MaxMissing {
			clear(t.missing)
			stats.Discontinuities++
			reset = true
		} else {
			if t.missing == nil {
				t.missing = make(map[uint64]missingPacket)
			}
			for missing := t.highest + 1; missing < extended; missing++ {
				if _, exists := t.missing[missing]; !exists {
					t.missing[missing] = missingPacket{firstSeen: packet.ReceivedAt}
					stats.NACKCandidates++
				}
			}
		}
		t.highest = extended
	}
	state, missing := t.missing[extended]
	repair := repairNone
	if missing {
		delete(t.missing, extended)
		if packet.RecoveredFEC {
			repair = repairFEC
		} else if packet.RecoveredRTX {
			repair = repairRTX
		} else {
			delay := packet.ReceivedAt.Sub(state.firstSeen)
			t.delay.observe(delay)
			if state.requests == 0 {
				stats.ReorderedBeforeNACK++
			} else {
				stats.LateAfterNACK++
			}
		}
	} else if !advanced {
		if packet.RecoveredFEC {
			stats.DuplicateFEC++
		} else if packet.RecoveredRTX {
			stats.DuplicateRTX++
		} else {
			stats.Duplicates++
		}
		return extended, reset, repairNone, true
	}
	return extended, reset, repair, false
}

func (t *tracker) due(now time.Time, stats *Stats) []uint16 {
	due := make([]uint64, 0, min(len(t.missing), t.config.MaxNACKBatch))
	delay := t.nackDelay()
	for sequence, state := range t.missing {
		if now.Sub(state.firstSeen) >= t.config.PacketExpiry || state.requests >= t.config.MaxNACKs {
			delete(t.missing, sequence)
			stats.Expired++
			continue
		}
		deadline := state.firstSeen.Add(delay)
		if state.requests > 0 {
			deadline = state.lastRequest.Add(t.config.NACKRetry)
		}
		if !now.Before(deadline) {
			due = append(due, sequence)
		}
	}
	sort.Slice(due, func(left, right int) bool { return due[left] < due[right] })
	if len(due) > t.config.MaxNACKBatch {
		due = due[:t.config.MaxNACKBatch]
	}
	sequences := make([]uint16, len(due))
	for index, extended := range due {
		state := t.missing[extended]
		state.lastRequest = now
		state.requests++
		t.missing[extended] = state
		sequences[index] = uint16(extended)
		stats.NACKRequests++
	}
	return sequences
}

func (t *tracker) nextDeadline() (time.Time, bool) {
	var earliest time.Time
	delay := t.nackDelay()
	for _, state := range t.missing {
		deadline := state.firstSeen.Add(delay)
		if state.requests > 0 {
			deadline = state.lastRequest.Add(t.config.NACKRetry)
		}
		expiry := state.firstSeen.Add(t.config.PacketExpiry)
		if expiry.Before(deadline) {
			deadline = expiry
		}
		if earliest.IsZero() || deadline.Before(earliest) {
			earliest = deadline
		}
	}
	return earliest, !earliest.IsZero()
}

func (t *tracker) nackDelay() time.Duration {
	return t.delay.value(t.config.MinNACKDelay, t.config.MaxNACKDelay)
}

func (d *delayEstimator) observe(sample time.Duration) {
	if sample <= 0 {
		return
	}
	value := float64(sample)
	if !d.initialized {
		d.initialized = true
		d.mean = value
		d.deviation = value / 2
		return
	}
	errorValue := math.Abs(value - d.mean)
	d.deviation = d.deviation*0.75 + errorValue*0.25
	d.mean = d.mean*0.875 + value*0.125
}

func (d *delayEstimator) value(minimum, maximum time.Duration) time.Duration {
	if !d.initialized {
		return minimum
	}
	value := time.Duration(d.mean + 4*d.deviation)
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (b *reorderBuffer) push(sequence uint64, packet Packet, stats *Stats, emit Emit) error {
	if !b.initialized {
		b.initialized = true
		b.expected = sequence
	}
	if sequence < b.expected {
		stats.ReorderLate++
		if packet.RecoveredRTX {
			stats.LateRTX++
		} else if packet.RecoveredFEC {
			stats.LateFEC++
		}
		return nil
	}
	if sequence == b.expected {
		if err := emitPacket(packet, stats, emit); err != nil {
			return err
		}
		b.expected++
		return b.drain(packet.ReceivedAt, stats, emit)
	}
	if _, exists := b.pending[sequence]; exists {
		stats.Duplicates++
		return nil
	}
	if len(b.pending) >= b.config.MaxPending {
		return fmt.Errorf("RTP reorder buffer reached its %d-packet limit", b.config.MaxPending)
	}
	if b.pending == nil {
		b.pending = make(map[uint64]Packet)
	}
	b.pending[sequence] = packet
	stats.ReorderPeak = max(stats.ReorderPeak, len(b.pending))
	if b.gapStarted.IsZero() {
		b.gapStarted = packet.ReceivedAt
	}
	return nil
}

func (b *reorderBuffer) flushExpired(now time.Time, stats *Stats, emit Emit) error {
	if b.gapStarted.IsZero() || now.Sub(b.gapStarted) < b.config.ReorderWait {
		return nil
	}
	next, found := b.nearest()
	if !found {
		b.gapStarted = time.Time{}
		return nil
	}
	stats.ReorderSkipped += next - b.expected
	b.expected = next
	b.gapStarted = time.Time{}
	return b.drain(now, stats, emit)
}

func (b *reorderBuffer) nextDeadline() (time.Time, bool) {
	if b.gapStarted.IsZero() {
		return time.Time{}, false
	}
	return b.gapStarted.Add(b.config.ReorderWait), true
}

func (b *reorderBuffer) reset(sequence uint64, stats *Stats) {
	stats.ReorderDiscarded += uint64(len(b.pending))
	clear(b.pending)
	b.initialized = true
	b.expected = sequence
	b.gapStarted = time.Time{}
}

func (b *reorderBuffer) drain(now time.Time, stats *Stats, emit Emit) error {
	for {
		packet, exists := b.pending[b.expected]
		if !exists {
			break
		}
		delete(b.pending, b.expected)
		if err := emitPacket(packet, stats, emit); err != nil {
			return err
		}
		b.expected++
	}
	if len(b.pending) == 0 {
		b.gapStarted = time.Time{}
	} else if b.gapStarted.IsZero() {
		b.gapStarted = now
	}
	return nil
}

func (b *reorderBuffer) nearest() (uint64, bool) {
	var nearest uint64
	found := false
	for sequence := range b.pending {
		if sequence < b.expected {
			continue
		}
		if !found || sequence < nearest {
			nearest = sequence
			found = true
		}
	}
	return nearest, found
}

func extendSequence(sequence uint16, reference uint64) uint64 {
	base := reference &^ (sequenceSpace - 1)
	candidate := base | uint64(sequence)
	if candidate+sequenceSpace/2 < reference {
		return candidate + sequenceSpace
	}
	if candidate > reference+sequenceSpace/2 && candidate >= sequenceSpace {
		return candidate - sequenceSpace
	}
	return candidate
}

func emitPacket(packet Packet, stats *Stats, emit Emit) error {
	if err := emit(packet.RTP); err != nil {
		return fmt.Errorf("emit RTP packet: %w", err)
	}
	if packet.repair == repairRTX {
		stats.RepairedRTX++
	} else if packet.repair == repairFEC {
		stats.RepairedFEC++
	}
	return nil
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	stopTimer(timer)
	timer.Reset(delay)
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
