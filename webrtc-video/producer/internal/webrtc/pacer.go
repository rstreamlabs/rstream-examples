package webrtc

import (
	"math"
	"sync"

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
	protection flexFECProtection,
) float64 {
	if math.IsNaN(mediaPacingFactor) || math.IsInf(mediaPacingFactor, 0) || mediaPacingFactor < 1 {
		mediaPacingFactor = 1
	}
	if !protection.enabled() {
		return mediaPacingFactor
	}
	protectedWireFactor := (float64(protection.mediaPackets) +
		float64(protection.repairPackets)) / float64(protection.mediaPackets)
	return math.Max(1, mediaPacingFactor/protectedWireFactor)
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

func (p *minimumBitratePacer) SetTargetBitrate(bitrate int) {
	if bitrate < p.minimumBitrate {
		bitrate = p.minimumBitrate
	}
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
