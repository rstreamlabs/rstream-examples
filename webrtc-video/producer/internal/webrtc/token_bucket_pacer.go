package webrtc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtp"
)

var (
	errPacerClosed        = errors.New("rtp pacer is closed")
	errPacerNilHeader     = errors.New("rtp pacer received a nil header")
	errPacerQueueFull     = errors.New("rtp pacer queue is full")
	errPacerUnknownStream = errors.New("rtp pacer has no writer for the SSRC")
)

type pacedPacket struct {
	admittedBitrate int
	attributes      interceptor.Attributes
	enqueuedAt      time.Time
	header          rtp.Header
	payload         *[]byte
	serviceNs       int64
	size            int
	writer          interceptor.RTPWriter
	repair          repairKind
	retransmission  retransmissionKey
	tracksRTX       bool
	trackTWCC       bool
	twccID          uint8
}

type retransmissionKey struct {
	ssrc             uint32
	originalSequence uint16
}

type pacedStream struct {
	writer    interceptor.RTPWriter
	repair    repairKind
	trackTWCC bool
	twccID    uint8
}

type repairKind uint8

const (
	repairKindNone repairKind = iota
	repairKindRetransmission
	repairKindForwardErrorCorrection
)

type mediaFrameAdmission struct {
	admitted          bool
	complete          func()
	recoveryComplete  bool
	requestKeyFrame   bool
	requestRetryAfter time.Duration
}

func (a mediaFrameAdmission) completePacketization() {
	if a.complete != nil {
		a.complete()
	}
}

type tokenBucketPacer struct {
	admissionMu                              sync.Mutex
	closed                                   chan struct{}
	closeOnce                                sync.Once
	done                                     sync.WaitGroup
	errorMu                                  sync.RWMutex
	firstError                               error
	pacingFactor                             float64
	payloadPool                              sync.Pool
	regularQueue                             chan *pacedPacket
	retransmissionQueue                      chan *pacedPacket
	forwardErrorCorrectionQueue              chan *pacedPacket
	forwardErrorCorrectionMediaPackets       atomic.Int64
	forwardErrorCorrectionRepairPackets      atomic.Int64
	rateChanged                              chan struct{}
	targetBitrate                            atomic.Int64
	stateMu                                  sync.RWMutex
	isClosed                                 bool
	queueSlots                               chan struct{}
	queueDrops                               atomic.Uint64
	queuedPrimaryFrames                      atomic.Int64
	queuedPrimaryServiceNs                   atomic.Int64
	queuedRetransmissionServiceNs            atomic.Int64
	queuedForwardErrorCorrectionServiceNs    atomic.Int64
	maximumQueueDelayNanoseconds             atomic.Int64
	maximumPrimaryResidenceNs                atomic.Int64
	maximumRepairResidenceNs                 atomic.Int64
	maximumRetransmissionResidenceNs         atomic.Int64
	maximumForwardErrorCorrectionResidenceNs atomic.Int64
	maximumSustainedDelayNs                  atomic.Int64
	maximumAdmittedSustainedNs               atomic.Int64
	keyFrameReserveBytes                     atomic.Int64
	mediaFramesDropped                       atomic.Uint64
	mediaBytesDropped                        atomic.Uint64
	repairPacketsExpired                     atomic.Uint64
	repairPacketsTrimmed                     atomic.Uint64
	retransmissionPacketsExpired             atomic.Uint64
	retransmissionPacketsCoalesced           atomic.Uint64
	forwardErrorCorrectionPacketsExpired     atomic.Uint64
	retransmissionPacketsTrimmed             atomic.Uint64
	forwardErrorCorrectionPacketsTrimmed     atomic.Uint64
	droppingUntilKeyFrame                    bool
	packetizationMu                          sync.Mutex
	packetizationBitrate                     atomic.Int64
	rateDecreasePending                      atomic.Bool
	sentPrimary                              atomic.Uint64
	sentPrimaryBytes                         atomic.Uint64
	sentRepair                               atomic.Uint64
	sentRetransmission                       atomic.Uint64
	sentRetransmissionBytes                  atomic.Uint64
	sentForwardErrorCorrection               atomic.Uint64
	sentForwardErrorCorrectionBytes          atomic.Uint64
	transportSequence                        atomic.Uint32
	writersMu                                sync.RWMutex
	writers                                  map[uint32]pacedStream
	retransmissionMu                         sync.Mutex
	pendingRetransmissions                   map[retransmissionKey]struct{}
}

const (
	maximumMediaAdmissionDelay = 225 * time.Millisecond
	maximumRepairResidence     = maximumMediaAdmissionDelay
	// Pion packetizes outbound media below its 1200-byte MTU. The additional
	// headroom covers RTP extensions and repair encapsulation when bounding the
	// single repair packet that the scheduler may place before each media frame.
	maximumRepairPacketBytes = 1500
)

var _ gcc.Pacer = (*tokenBucketPacer)(nil)

func newTokenBucketPacer(
	initialBitrate int,
	pacingFactor float64,
	queueSize int,
) *tokenBucketPacer {
	if initialBitrate <= 0 {
		initialBitrate = 1
	}
	if pacingFactor < 1 {
		pacingFactor = 1
	}
	if queueSize <= 0 {
		queueSize = 1
	}
	pacer := &tokenBucketPacer{
		closed:                      make(chan struct{}),
		pacingFactor:                pacingFactor,
		regularQueue:                make(chan *pacedPacket, queueSize),
		retransmissionQueue:         make(chan *pacedPacket, queueSize),
		forwardErrorCorrectionQueue: make(chan *pacedPacket, queueSize),
		rateChanged:                 make(chan struct{}, 1),
		queueSlots:                  make(chan struct{}, queueSize),
		writers:                     make(map[uint32]pacedStream),
		pendingRetransmissions:      make(map[retransmissionKey]struct{}),
	}
	pacer.targetBitrate.Store(int64(initialBitrate))
	pacer.payloadPool.New = func() any {
		payload := make([]byte, 0, 1500)
		return &payload
	}
	pacer.done.Add(1)
	go pacer.run()
	return pacer
}

func (p *tokenBucketPacer) AddStream(ssrc uint32, writer interceptor.RTPWriter) {
	p.writersMu.Lock()
	p.writers[ssrc] = pacedStream{writer: writer}
	p.writersMu.Unlock()
}

func (p *tokenBucketPacer) markRetransmissionStream(ssrc uint32) {
	p.writersMu.Lock()
	stream, ok := p.writers[ssrc]
	if ok {
		stream.repair = repairKindRetransmission
		p.writers[ssrc] = stream
	}
	p.writersMu.Unlock()
}

func (p *tokenBucketPacer) markForwardErrorCorrectionStream(ssrc uint32) {
	p.writersMu.Lock()
	stream, ok := p.writers[ssrc]
	if ok {
		stream.repair = repairKindForwardErrorCorrection
		p.writers[ssrc] = stream
	}
	p.writersMu.Unlock()
}

func (p *tokenBucketPacer) configureForwardErrorCorrection(protection flexFECProtection) {
	p.forwardErrorCorrectionMediaPackets.Store(int64(protection.mediaPackets))
	p.forwardErrorCorrectionRepairPackets.Store(int64(protection.repairPackets))
}

func (p *tokenBucketPacer) forwardErrorCorrectionProtection() (int, int) {
	return int(p.forwardErrorCorrectionMediaPackets.Load()),
		int(p.forwardErrorCorrectionRepairPackets.Load())
}

func (p *tokenBucketPacer) setTransportCCExtension(
	ssrc uint32,
	extensionID uint8,
	tracked bool,
) {
	p.writersMu.Lock()
	stream, ok := p.writers[ssrc]
	if ok {
		stream.twccID = extensionID
		stream.trackTWCC = tracked
		p.writers[ssrc] = stream
	}
	p.writersMu.Unlock()
}

func (p *tokenBucketPacer) SetTargetBitrate(bitrate int) {
	if bitrate <= 0 {
		bitrate = 1
	}
	previous := p.targetBitrate.Swap(int64(bitrate))
	if int64(bitrate) < previous {
		p.rateDecreasePending.Store(true)
	}
	p.observeSustainedQueueDelay()
	p.signalRateChanged()
}

func (p *tokenBucketPacer) Write(
	header *rtp.Header,
	payload []byte,
	attributes interceptor.Attributes,
) (int, error) {
	if header == nil {
		return 0, errPacerNilHeader
	}
	if err := p.asyncError(); err != nil {
		return 0, err
	}
	p.writersMu.RLock()
	stream, ok := p.writers[header.SSRC]
	p.writersMu.RUnlock()
	if !ok || stream.writer == nil {
		return 0, fmt.Errorf("%w: %d", errPacerUnknownStream, header.SSRC)
	}
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	if p.isClosed {
		return 0, errPacerClosed
	}
	select {
	case p.queueSlots <- struct{}{}:
	default:
		p.queueDrops.Add(1)
		return 0, errPacerQueueFull
	}
	packetSize := header.MarshalSize() + len(payload)
	retransmission, tracksRTX := retransmissionIdentity(header, payload, stream.repair)
	if tracksRTX && !p.reserveRetransmission(retransmission) {
		<-p.queueSlots
		p.retransmissionPacketsCoalesced.Add(1)
		return packetSize, nil
	}
	buffer := p.payloadPool.Get().(*[]byte)
	if cap(*buffer) < len(payload) {
		*buffer = make([]byte, len(payload))
	} else {
		*buffer = (*buffer)[:len(payload)]
	}
	copy(*buffer, payload)
	admittedBitrate := 0
	if stream.repair == repairKindNone {
		admittedBitrate = int(p.packetizationBitrate.Load())
	}
	packet := &pacedPacket{
		admittedBitrate: admittedBitrate,
		attributes:      maps.Clone(attributes),
		enqueuedAt:      time.Now(),
		header:          header.Clone(),
		payload:         buffer,
		size:            packetSize,
		writer:          stream.writer,
		repair:          stream.repair,
		retransmission:  retransmission,
		tracksRTX:       tracksRTX,
		trackTWCC:       stream.trackTWCC,
		twccID:          stream.twccID,
	}
	packet.serviceNs = queueDelayAtRate(
		int64(packet.size),
		p.bytesPerSecondAtBitrate(max(p.targetBitrateValue(), admittedBitrate)),
	).Nanoseconds()
	switch packet.repair {
	case repairKindRetransmission:
		p.queuedRetransmissionServiceNs.Add(packet.serviceNs)
	case repairKindForwardErrorCorrection:
		p.queuedForwardErrorCorrectionServiceNs.Add(packet.serviceNs)
	default:
		p.queuedPrimaryServiceNs.Add(packet.serviceNs)
		if packet.header.Marker {
			p.queuedPrimaryFrames.Add(1)
		}
	}
	p.observeSustainedQueueDelay()
	switch packet.repair {
	case repairKindRetransmission:
		p.retransmissionQueue <- packet
	case repairKindForwardErrorCorrection:
		p.forwardErrorCorrectionQueue <- packet
	default:
		p.regularQueue <- packet
	}
	return packet.size, nil
}

func (p *tokenBucketPacer) AdmitMediaFrame(size int, keyFrame bool) (decision mediaFrameAdmission) {
	if size <= 0 {
		return mediaFrameAdmission{admitted: true}
	}
	p.packetizationMu.Lock()
	defer func() {
		if decision.admitted {
			p.packetizationBitrate.Store(int64(p.targetBitrateValue()))
			var complete sync.Once
			decision.complete = func() {
				complete.Do(func() {
					p.packetizationBitrate.Store(0)
					p.packetizationMu.Unlock()
				})
			}
			return
		}
		p.packetizationMu.Unlock()
	}()
	p.admissionMu.Lock()
	defer p.admissionMu.Unlock()
	projectedDelay := p.admissionQueueDelay() + queueDelayAtRate(
		int64(size),
		p.sustainedBytesPerSecond(),
	)
	if p.droppingUntilKeyFrame {
		if keyFrame && projectedDelay <= maximumMediaAdmissionDelay {
			p.droppingUntilKeyFrame = false
			p.keyFrameReserveBytes.Store(int64(size))
			recordMaximum(&p.maximumAdmittedSustainedNs, projectedDelay.Nanoseconds())
			return mediaFrameAdmission{admitted: true, recoveryComplete: true}
		}
		p.recordMediaFrameDrop(size)
		if keyFrame {
			return mediaFrameAdmission{
				requestKeyFrame:   true,
				requestRetryAfter: p.recoveryKeyFrameDelay(),
			}
		}
		return mediaFrameAdmission{}
	}
	if projectedDelay <= maximumMediaAdmissionDelay {
		if keyFrame {
			p.keyFrameReserveBytes.Store(int64(size))
		}
		recordMaximum(&p.maximumAdmittedSustainedNs, projectedDelay.Nanoseconds())
		return mediaFrameAdmission{admitted: true}
	}
	p.droppingUntilKeyFrame = true
	p.recordMediaFrameDrop(size)
	return mediaFrameAdmission{
		requestKeyFrame:   true,
		requestRetryAfter: p.recoveryKeyFrameDelay(),
	}
}

func (p *tokenBucketPacer) Close() error {
	p.closeOnce.Do(func() {
		p.stateMu.Lock()
		p.isClosed = true
		close(p.closed)
		p.stateMu.Unlock()
	})
	p.done.Wait()
	return p.asyncError()
}

func (p *tokenBucketPacer) run() {
	defer p.done.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	lastUpdate := time.Now()
	availableBytes := p.maximumBurstBytes()
	primaryFrameActive := false
	var pending *pacedPacket
	consecutiveRetransmissions := 0
	primaryPacketsSinceFEC := 0
	forwardErrorCorrectionPacketsDue := 0
	applyRateChange := func(packet *pacedPacket) *pacedPacket {
		packet, changed := p.applyRateChange(packet)
		if changed {
			primaryPacketsSinceFEC = 0
			forwardErrorCorrectionPacketsDue = 0
		}
		return packet
	}
	for {
		if pending == nil {
			select {
			case <-p.rateChanged:
				availableBytes, lastUpdate = p.refill(availableBytes, lastUpdate, time.Now(), 0, p.bytesPerSecond())
				applyRateChange(pending)
			default:
			}
			pending = p.nextPacket(
				consecutiveRetransmissions,
				forwardErrorCorrectionPacketsDue,
				primaryFrameActive,
			)
			if pending == nil {
				if primaryFrameActive {
					if forwardErrorCorrectionPacketsDue > 0 {
						select {
						case <-p.closed:
							p.discardQueuedPackets()
							return
						case <-p.rateChanged:
							availableBytes, lastUpdate = p.refill(availableBytes, lastUpdate, time.Now(), 0, p.bytesPerSecond())
							pending = applyRateChange(pending)
						case pending = <-p.forwardErrorCorrectionQueue:
						case pending = <-p.regularQueue:
						}
					} else {
						select {
						case <-p.closed:
							p.discardQueuedPackets()
							return
						case <-p.rateChanged:
							availableBytes, lastUpdate = p.refill(availableBytes, lastUpdate, time.Now(), 0, p.bytesPerSecond())
							pending = applyRateChange(pending)
						case pending = <-p.regularQueue:
						}
					}
				} else {
					select {
					case <-p.closed:
						p.discardQueuedPackets()
						return
					case <-p.rateChanged:
						availableBytes, lastUpdate = p.refill(availableBytes, lastUpdate, time.Now(), 0, p.bytesPerSecond())
						pending = applyRateChange(pending)
					case pending = <-p.forwardErrorCorrectionQueue:
					case pending = <-p.retransmissionQueue:
					case pending = <-p.regularQueue:
					}
				}
			}
			continue
		}
		if p.asyncError() != nil {
			p.release(pending)
			pending = nil
			p.discardQueuedPackets()
			continue
		}
		now := time.Now()
		if pending.repair != repairKindNone && now.Sub(pending.enqueuedAt) >= maximumRepairResidence {
			p.recordRepairExpiration(pending.repair)
			if pending.repair == repairKindForwardErrorCorrection && forwardErrorCorrectionPacketsDue > 0 {
				forwardErrorCorrectionPacketsDue--
				if forwardErrorCorrectionPacketsDue == 0 {
					primaryPacketsSinceFEC = 0
				}
			}
			p.release(pending)
			pending = nil
			continue
		}
		availableBytes, lastUpdate = p.refill(
			availableBytes,
			lastUpdate,
			now,
			pending.size,
			p.packetBytesPerSecond(pending),
		)
		if availableBytes >= float64(pending.size) {
			availableBytes -= float64(pending.size)
			p.observeDeliveryQueueDelay(pending)
			if err := p.prepareTransportSequence(pending); err != nil {
				p.recordError(fmt.Errorf("prepare paced RTP transport sequence: %w", err))
				p.release(pending)
				pending = nil
				p.discardQueuedPackets()
				continue
			}
			written, err := pending.writer.Write(
				&pending.header,
				(*pending.payload)[:len(*pending.payload)],
				pending.attributes,
			)
			if err != nil {
				p.recordError(fmt.Errorf("paced RTP write failed: %w", err))
				p.discardQueuedPackets()
			} else if pending.repair != repairKindNone {
				p.recordRepairSent(pending.repair, written)
			} else {
				p.sentPrimary.Add(1)
				p.sentPrimaryBytes.Add(uint64(max(0, written)))
			}
			switch pending.repair {
			case repairKindNone:
				mediaPackets, repairPackets := p.forwardErrorCorrectionProtection()
				if mediaPackets > 0 && repairPackets > 0 && forwardErrorCorrectionPacketsDue == 0 {
					primaryPacketsSinceFEC = min(mediaPackets, primaryPacketsSinceFEC+1)
					if primaryPacketsSinceFEC == mediaPackets {
						forwardErrorCorrectionPacketsDue = repairPackets
					}
				}
				if pending.header.Marker {
					primaryFrameActive = false
					consecutiveRetransmissions = 0
				} else {
					primaryFrameActive = true
				}
			case repairKindRetransmission:
				consecutiveRetransmissions++
			case repairKindForwardErrorCorrection:
				if forwardErrorCorrectionPacketsDue > 0 {
					forwardErrorCorrectionPacketsDue--
					if forwardErrorCorrectionPacketsDue == 0 {
						primaryPacketsSinceFEC = 0
					}
				}
			}
			p.release(pending)
			pending = nil
			continue
		}
		wait := p.waitDurationAtRate(
			float64(pending.size)-availableBytes,
			p.packetBytesPerSecond(pending),
		)
		resetTimer(timer, wait)
		select {
		case <-p.closed:
			p.release(pending)
			p.discardQueuedPackets()
			return
		case <-p.rateChanged:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			pending = applyRateChange(pending)
		case <-timer.C:
		}
	}
}

func (p *tokenBucketPacer) prepareTransportSequence(packet *pacedPacket) error {
	if packet.twccID == 0 {
		return nil
	}
	extension := packet.header.GetExtension(packet.twccID)
	if !packet.trackTWCC {
		if extension == nil {
			return nil
		}
		return packet.header.DelExtension(packet.twccID)
	}
	sequence := uint16(p.transportSequence.Add(1) - 1)
	if len(extension) >= 2 {
		binary.BigEndian.PutUint16(extension, sequence)
		return nil
	}
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], sequence)
	return packet.header.SetExtension(packet.twccID, encoded[:])
}

func (p *tokenBucketPacer) refill(
	availableBytes float64,
	lastUpdate time.Time,
	now time.Time,
	minimumBurst int,
	bytesPerSecond float64,
) (float64, time.Time) {
	availableBytes += now.Sub(lastUpdate).Seconds() * bytesPerSecond
	maximum := math.Max(p.maximumBurstBytesAtRate(bytesPerSecond), float64(minimumBurst))
	return math.Min(availableBytes, maximum), now
}

func (p *tokenBucketPacer) waitDurationAtRate(missingBytes, bytesPerSecond float64) time.Duration {
	wait := time.Duration(math.Ceil(missingBytes / bytesPerSecond * float64(time.Second)))
	if wait < 100*time.Microsecond {
		return 100 * time.Microsecond
	}
	return wait
}

func (p *tokenBucketPacer) bytesPerSecond() float64 {
	return p.sustainedBytesPerSecond()
}

func (p *tokenBucketPacer) sustainedBytesPerSecond() float64 {
	bitrate := p.targetBitrateValue()
	return math.Max(1, float64(bitrate)*p.pacingFactor/8)
}

func (p *tokenBucketPacer) maximumBurstBytes() float64 {
	return p.maximumBurstBytesAtRate(p.bytesPerSecond())
}

func (p *tokenBucketPacer) maximumBurstBytesAtRate(bytesPerSecond float64) float64 {
	return math.Max(3000, bytesPerSecond*0.01)
}

func (p *tokenBucketPacer) release(packet *pacedPacket) {
	if packet.tracksRTX {
		p.releaseRetransmission(packet.retransmission)
	}
	switch packet.repair {
	case repairKindRetransmission:
		p.queuedRetransmissionServiceNs.Add(-packet.serviceNs)
	case repairKindForwardErrorCorrection:
		p.queuedForwardErrorCorrectionServiceNs.Add(-packet.serviceNs)
	default:
		p.queuedPrimaryServiceNs.Add(-packet.serviceNs)
		if packet.header.Marker {
			p.queuedPrimaryFrames.Add(-1)
		}
	}
	*packet.payload = (*packet.payload)[:0]
	p.payloadPool.Put(packet.payload)
	<-p.queueSlots
}

func (p *tokenBucketPacer) discardQueuedPackets() {
	for {
		released := false
		select {
		case packet := <-p.forwardErrorCorrectionQueue:
			p.release(packet)
			released = true
		default:
		}
		select {
		case packet := <-p.retransmissionQueue:
			p.release(packet)
			released = true
		default:
		}
		select {
		case packet := <-p.regularQueue:
			p.release(packet)
			released = true
		default:
		}
		if !released {
			return
		}
	}
}

func (p *tokenBucketPacer) nextPacket(
	consecutiveRetransmissions int,
	forwardErrorCorrectionPacketsDue int,
	primaryFrameActive bool,
) *pacedPacket {
	if forwardErrorCorrectionPacketsDue > 0 {
		select {
		case packet := <-p.forwardErrorCorrectionQueue:
			return packet
		default:
		}
	}
	if primaryFrameActive {
		select {
		case packet := <-p.regularQueue:
			return packet
		default:
			return nil
		}
	}
	const maximumRetransmissionBurst = 1
	if consecutiveRetransmissions < maximumRetransmissionBurst {
		select {
		case packet := <-p.retransmissionQueue:
			return packet
		default:
		}
	}
	select {
	case packet := <-p.regularQueue:
		return packet
	default:
	}
	select {
	case packet := <-p.forwardErrorCorrectionQueue:
		return packet
	default:
	}
	select {
	case packet := <-p.retransmissionQueue:
		return packet
	default:
		return nil
	}
}

func (p *tokenBucketPacer) applyRateChange(pending *pacedPacket) (*pacedPacket, bool) {
	if !p.rateDecreasePending.Swap(false) {
		return pending, false
	}
	if pending != nil && pending.repair != repairKindNone {
		p.recordRepairTrim(pending.repair)
		p.release(pending)
		pending = nil
	}
	for _, queue := range []chan *pacedPacket{
		p.forwardErrorCorrectionQueue,
		p.retransmissionQueue,
	} {
		for _, packet := range drainPacketQueue(queue) {
			p.recordRepairTrim(packet.repair)
			p.release(packet)
		}
	}
	return pending, true
}

func drainPacketQueue(queue chan *pacedPacket) []*pacedPacket {
	packets := make([]*pacedPacket, 0, len(queue))
	for {
		select {
		case packet := <-queue:
			packets = append(packets, packet)
		default:
			return packets
		}
	}
}

func (p *tokenBucketPacer) Stats() map[string]any {
	targetBitrate := p.targetBitrateValue()
	return map[string]any{
		"pacerTargetBitrateBps":                                   targetBitrate,
		"pacerPacingBitrateBps":                                   int(math.Ceil(p.bytesPerSecondAtBitrate(targetBitrate) * 8)),
		"pacerQueuePackets":                                       len(p.queueSlots),
		"pacerQueueDrops":                                         p.queueDrops.Load(),
		"pacerQueueDelayMilliseconds":                             float64(p.scheduledQueueDelay()) / float64(time.Millisecond),
		"pacerMaximumQueueDelayMilliseconds":                      float64(time.Duration(p.maximumQueueDelayNanoseconds.Load())) / float64(time.Millisecond),
		"pacerMaximumPrimaryResidenceMilliseconds":                float64(time.Duration(p.maximumPrimaryResidenceNs.Load())) / float64(time.Millisecond),
		"pacerMaximumRepairResidenceMilliseconds":                 float64(time.Duration(p.maximumRepairResidenceNs.Load())) / float64(time.Millisecond),
		"pacerMaximumRetransmissionResidenceMilliseconds":         float64(time.Duration(p.maximumRetransmissionResidenceNs.Load())) / float64(time.Millisecond),
		"pacerMaximumForwardErrorCorrectionResidenceMilliseconds": float64(time.Duration(p.maximumForwardErrorCorrectionResidenceNs.Load())) / float64(time.Millisecond),
		"pacerMaximumSustainedDelayMilliseconds":                  float64(time.Duration(p.maximumSustainedDelayNs.Load())) / float64(time.Millisecond),
		"pacerMaximumAdmittedSustainedDelayMilliseconds":          float64(time.Duration(p.maximumAdmittedSustainedNs.Load())) / float64(time.Millisecond),
		"pacerKeyFrameReserveBytes":                               p.keyFrameReserveBytes.Load(),
		"pacerMediaFramesDropped":                                 p.mediaFramesDropped.Load(),
		"pacerMediaBytesDropped":                                  p.mediaBytesDropped.Load(),
		"pacerRepairPacketsExpired":                               p.repairPacketsExpired.Load(),
		"pacerRepairPacketsTrimmed":                               p.repairPacketsTrimmed.Load(),
		"pacerRetransmissionPacketsExpired":                       p.retransmissionPacketsExpired.Load(),
		"pacerRetransmissionPacketsCoalesced":                     p.retransmissionPacketsCoalesced.Load(),
		"pacerForwardErrorCorrectionPacketsExpired":               p.forwardErrorCorrectionPacketsExpired.Load(),
		"pacerRetransmissionPacketsTrimmed":                       p.retransmissionPacketsTrimmed.Load(),
		"pacerForwardErrorCorrectionPacketsTrimmed":               p.forwardErrorCorrectionPacketsTrimmed.Load(),
		"pacerSentPrimary":                                        p.sentPrimary.Load(),
		"pacerSentPrimaryBytes":                                   p.sentPrimaryBytes.Load(),
		"pacerSentRepair":                                         p.sentRepair.Load(),
		"pacerSentRetransmission":                                 p.sentRetransmission.Load(),
		"pacerSentRetransmissionBytes":                            p.sentRetransmissionBytes.Load(),
		"pacerSentForwardErrorCorrection":                         p.sentForwardErrorCorrection.Load(),
		"pacerSentForwardErrorCorrectionBytes":                    p.sentForwardErrorCorrectionBytes.Load(),
	}
}

func retransmissionIdentity(
	header *rtp.Header,
	payload []byte,
	kind repairKind,
) (retransmissionKey, bool) {
	if kind != repairKindRetransmission || len(payload) < 2 {
		return retransmissionKey{}, false
	}
	return retransmissionKey{
		ssrc:             header.SSRC,
		originalSequence: binary.BigEndian.Uint16(payload[:2]),
	}, true
}

func (p *tokenBucketPacer) reserveRetransmission(key retransmissionKey) bool {
	p.retransmissionMu.Lock()
	defer p.retransmissionMu.Unlock()
	if _, exists := p.pendingRetransmissions[key]; exists {
		return false
	}
	p.pendingRetransmissions[key] = struct{}{}
	return true
}

func (p *tokenBucketPacer) releaseRetransmission(key retransmissionKey) {
	p.retransmissionMu.Lock()
	delete(p.pendingRetransmissions, key)
	p.retransmissionMu.Unlock()
}

func (p *tokenBucketPacer) scheduledQueueDelay() time.Duration {
	primary := max(int64(0), p.queuedPrimaryServiceNs.Load())
	retransmission := max(int64(0), p.queuedRetransmissionServiceNs.Load())
	forwardErrorCorrection := max(
		int64(0),
		p.queuedForwardErrorCorrectionServiceNs.Load(),
	)
	return time.Duration(primary + retransmission + forwardErrorCorrection)
}

func (p *tokenBucketPacer) admissionQueueDelay() time.Duration {
	primary := max(int64(0), p.queuedPrimaryServiceNs.Load())
	retransmission := max(int64(0), p.queuedRetransmissionServiceNs.Load())
	forwardErrorCorrection := max(
		int64(0),
		p.queuedForwardErrorCorrectionServiceNs.Load(),
	)
	frameBound := max(int64(0), p.queuedPrimaryFrames.Load()) + 1
	maximumRetransmissionAhead := frameBound * queueDelayAtRate(
		maximumRepairPacketBytes,
		p.sustainedBytesPerSecond(),
	).Nanoseconds()
	return time.Duration(
		primary +
			forwardErrorCorrection +
			min(retransmission, maximumRetransmissionAhead),
	)
}

func queueDelayAtRate(bytes int64, bytesPerSecond float64) time.Duration {
	if bytes <= 0 {
		return 0
	}
	seconds := float64(bytes) / math.Max(1, bytesPerSecond)
	return time.Duration(seconds * float64(time.Second))
}

func (p *tokenBucketPacer) observeDeliveryQueueDelay(packet *pacedPacket) {
	residenceNanoseconds := time.Since(packet.enqueuedAt).Nanoseconds()
	recordMaximum(&p.maximumQueueDelayNanoseconds, residenceNanoseconds)
	if packet.repair != repairKindNone {
		recordMaximum(&p.maximumRepairResidenceNs, residenceNanoseconds)
		switch packet.repair {
		case repairKindRetransmission:
			recordMaximum(&p.maximumRetransmissionResidenceNs, residenceNanoseconds)
		case repairKindForwardErrorCorrection:
			recordMaximum(&p.maximumForwardErrorCorrectionResidenceNs, residenceNanoseconds)
		}
		return
	}
	recordMaximum(&p.maximumPrimaryResidenceNs, residenceNanoseconds)
}

func (p *tokenBucketPacer) observeSustainedQueueDelay() {
	recordMaximum(&p.maximumSustainedDelayNs, p.scheduledQueueDelay().Nanoseconds())
}

func recordMaximum(maximum *atomic.Int64, value int64) {
	for {
		current := maximum.Load()
		if value <= current || maximum.CompareAndSwap(current, value) {
			return
		}
	}
}

func (p *tokenBucketPacer) signalRateChanged() {
	select {
	case p.rateChanged <- struct{}{}:
	default:
	}
}

func (p *tokenBucketPacer) recoveryKeyFrameDelay() time.Duration {
	reserveBytes := p.keyFrameReserveBytes.Load()
	if reserveBytes <= 0 {
		return 0
	}
	bytesPerSecond := p.sustainedBytesPerSecond()
	reservedDuration := queueDelayAtRate(reserveBytes+reserveBytes/4, bytesPerSecond)
	targetQueueDelay := max(time.Duration(0), maximumMediaAdmissionDelay-reservedDuration)
	return max(time.Duration(0), p.admissionQueueDelay()-targetQueueDelay)
}

func (p *tokenBucketPacer) recordMediaFrameDrop(size int) {
	p.mediaFramesDropped.Add(1)
	p.mediaBytesDropped.Add(uint64(size))
}

func (p *tokenBucketPacer) recordRepairExpiration(kind repairKind) {
	p.repairPacketsExpired.Add(1)
	switch kind {
	case repairKindRetransmission:
		p.retransmissionPacketsExpired.Add(1)
	case repairKindForwardErrorCorrection:
		p.forwardErrorCorrectionPacketsExpired.Add(1)
	}
}

func (p *tokenBucketPacer) recordRepairTrim(kind repairKind) {
	p.repairPacketsTrimmed.Add(1)
	switch kind {
	case repairKindRetransmission:
		p.retransmissionPacketsTrimmed.Add(1)
	case repairKindForwardErrorCorrection:
		p.forwardErrorCorrectionPacketsTrimmed.Add(1)
	}
}

func (p *tokenBucketPacer) recordRepairSent(kind repairKind, bytes int) {
	p.sentRepair.Add(1)
	switch kind {
	case repairKindRetransmission:
		p.sentRetransmission.Add(1)
		p.sentRetransmissionBytes.Add(uint64(max(0, bytes)))
	case repairKindForwardErrorCorrection:
		p.sentForwardErrorCorrection.Add(1)
		p.sentForwardErrorCorrectionBytes.Add(uint64(max(0, bytes)))
	}
}

func (p *tokenBucketPacer) packetBytesPerSecond(packet *pacedPacket) float64 {
	bitrate := p.targetBitrateValue()
	if packet != nil && packet.repair == repairKindNone && packet.admittedBitrate > bitrate {
		bitrate = packet.admittedBitrate
	}
	return p.bytesPerSecondAtBitrate(bitrate)
}

func (p *tokenBucketPacer) bytesPerSecondAtBitrate(bitrate int) float64 {
	return math.Max(1, float64(bitrate)*p.pacingFactor/8)
}

func (p *tokenBucketPacer) targetBitrateValue() int {
	return int(p.targetBitrate.Load())
}

func (p *tokenBucketPacer) recordError(err error) {
	p.errorMu.Lock()
	if p.firstError == nil {
		p.firstError = err
	}
	p.errorMu.Unlock()
}

func (p *tokenBucketPacer) asyncError() error {
	p.errorMu.RLock()
	defer p.errorMu.RUnlock()
	return p.firstError
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
