package webrtc

import (
	"math"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtp"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

const realTimePacingFactor = config.RealTimePacingFactor

type minimumBitratePacer struct {
	delegate       gcc.Pacer
	minimumBitrate int
	protection     flexFECProtection
	targetMu       sync.Mutex
	fixedMediaRate int
	writersMu      sync.Mutex
	writers        map[uint32]interceptor.RTPWriter
}

var _ gcc.Pacer = (*minimumBitratePacer)(nil)

func newMinimumBitratePacer(initialBitrate, minimumBitrate int) *minimumBitratePacer {
	return newMinimumBitratePacerWithProtection(
		initialBitrate,
		minimumBitrate,
		flexFECProtection{},
	)
}

func newMinimumBitratePacerWithProtection(
	initialBitrate int,
	minimumBitrate int,
	protection flexFECProtection,
) *minimumBitratePacer {
	if initialBitrate < minimumBitrate {
		initialBitrate = minimumBitrate
	}
	pacer := newTokenBucketPacer(
		wireBitrate(initialBitrate, protection),
		pacingFactorForProtection(realTimePacingFactor, protection),
		4096,
	)
	pacer.configureForwardErrorCorrection(protection)
	return wrapMinimumBitratePacerWithProtection(
		pacer,
		minimumBitrate,
		protection,
	)
}

func pacingFactorForProtection(
	mediaPacingFactor float64,
	_ flexFECProtection,
) float64 {
	// wireBitrate already adds the sustained repair budget. Reducing the
	// pacing factor by the same ratio would count that overhead twice and can
	// leave a protected stream with no burst headroom at all.
	if math.IsNaN(mediaPacingFactor) || math.IsInf(mediaPacingFactor, 0) || mediaPacingFactor < 1 {
		mediaPacingFactor = 1
	}
	return mediaPacingFactor
}

func wrapMinimumBitratePacer(delegate gcc.Pacer, minimumBitrate int) *minimumBitratePacer {
	return wrapMinimumBitratePacerWithProtection(
		delegate,
		minimumBitrate,
		flexFECProtection{},
	)
}

func wrapMinimumBitratePacerWithProtection(
	delegate gcc.Pacer,
	minimumBitrate int,
	protection flexFECProtection,
) *minimumBitratePacer {
	return &minimumBitratePacer{
		delegate:       delegate,
		minimumBitrate: minimumBitrate,
		protection:     protection,
		writers:        make(map[uint32]interceptor.RTPWriter),
	}
}

func (p *minimumBitratePacer) AddStream(ssrc uint32, writer interceptor.RTPWriter) {
	p.writersMu.Lock()
	p.writers[ssrc] = writer
	p.writersMu.Unlock()
	p.delegate.AddStream(ssrc, writer)
	if marker, ok := p.delegate.(interface{ markPrimaryStream(uint32) }); ok {
		marker.markPrimaryStream(ssrc)
	}
}

func (p *minimumBitratePacer) addAssociatedStreams(primarySSRC uint32, associatedSSRCs ...uint32) {
	p.writersMu.Lock()
	writer, ok := p.writers[primarySSRC]
	if !ok {
		p.writersMu.Unlock()
		return
	}
	for _, associatedSSRC := range associatedSSRCs {
		if associatedSSRC == 0 || associatedSSRC == primarySSRC {
			continue
		}
		p.writers[associatedSSRC] = writer
		p.delegate.AddStream(associatedSSRC, writer)
		if marker, ok := p.delegate.(interface{ markRetransmissionStream(uint32) }); ok {
			marker.markRetransmissionStream(associatedSSRC)
		}
	}
	p.writersMu.Unlock()
}

func (p *minimumBitratePacer) addUntrackedRepairStream(
	ssrc uint32,
	writer interceptor.RTPWriter,
) {
	if ssrc == 0 || writer == nil {
		return
	}
	p.writersMu.Lock()
	p.writers[ssrc] = writer
	p.delegate.AddStream(ssrc, writer)
	if marker, ok := p.delegate.(interface{ markForwardErrorCorrectionStream(uint32) }); ok {
		marker.markForwardErrorCorrectionStream(ssrc)
	}
	p.writersMu.Unlock()
}

func (p *minimumBitratePacer) setTransportCCExtension(
	ssrc uint32,
	extensionID uint8,
	tracked bool,
) {
	setter, ok := p.delegate.(interface {
		setTransportCCExtension(uint32, uint8, bool)
	})
	if ok {
		setter.setTransportCCExtension(ssrc, extensionID, tracked)
	}
}

func (p *minimumBitratePacer) observeRoundTripTime(roundTripTime time.Duration) {
	observer, ok := p.delegate.(interface{ observeRoundTripTime(time.Duration) })
	if ok {
		observer.observeRoundTripTime(roundTripTime)
	}
}

func (p *minimumBitratePacer) recoveryKeyFrameDelay() time.Duration {
	delayer, ok := p.delegate.(interface{ recoveryKeyFrameDelay() time.Duration })
	if !ok {
		return 0
	}
	return delayer.recoveryKeyFrameDelay()
}

func (p *minimumBitratePacer) SetTargetBitrate(bitrate int) {
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.fixedMediaRate > 0 {
		p.delegate.SetTargetBitrate(wireBitrate(p.fixedMediaRate, p.protection))
		return
	}
	// GCC estimates the complete paced traffic envelope. Forward that wire
	// budget unchanged so repair traffic is not counted a second time.
	minimumWireBitrate := wireBitrate(p.minimumBitrate, p.protection)
	if bitrate < minimumWireBitrate {
		bitrate = minimumWireBitrate
	}
	p.delegate.SetTargetBitrate(bitrate)
}

func (p *minimumBitratePacer) SetMediaTargetBitrate(bitrate int) {
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.fixedMediaRate > 0 {
		bitrate = p.fixedMediaRate
	}
	// Local controls, including the media floor and loss guard, operate on
	// encoder bitrate. Reserve the configured repair share before pacing it.
	if bitrate < p.minimumBitrate {
		bitrate = p.minimumBitrate
	}
	p.delegate.SetTargetBitrate(wireBitrate(bitrate, p.protection))
}

func (p *minimumBitratePacer) UseFixedMediaTargetBitrate(bitrate int) {
	if bitrate <= 0 {
		return
	}
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	p.fixedMediaRate = bitrate
	p.delegate.SetTargetBitrate(wireBitrate(bitrate, p.protection))
}

func (p *minimumBitratePacer) Write(
	header *rtp.Header,
	payload []byte,
	attributes interceptor.Attributes,
) (int, error) {
	return p.delegate.Write(header, payload, attributes)
}

func (p *minimumBitratePacer) Close() error {
	return p.delegate.Close()
}
