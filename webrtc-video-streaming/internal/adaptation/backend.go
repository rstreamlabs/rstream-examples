package adaptation

import (
	"fmt"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/media"
)

type Snapshot struct {
	Backend                  config.AdaptiveBackend `json:"backend"`
	Active                   bool                   `json:"active"`
	EstimatedBitrateBps      int                    `json:"estimatedBitrateBps"`
	EncoderTargetBitrateKbps int                    `json:"encoderTargetBitrateKbps"`
	LastAppliedBitrateKbps   int                    `json:"lastAppliedBitrateKbps"`
}

type Observation struct {
	EstimatedBitrateBps      int
	EncoderTargetBitrateKbps int
}

type Decision struct {
	TargetBitrateKbps int
}

type Backend interface {
	Name() config.AdaptiveBackend
	Decide(Observation) (Decision, bool)
}

type Controller struct {
	logger   *logs.Logger
	encoder  media.EncoderController
	backend  Backend
	interval time.Duration
	updates  chan int
	close    chan struct{}
	done     chan struct{}

	mu       sync.RWMutex
	snapshot Snapshot
}

func NewController(
	logger *logs.Logger,
	encoder media.EncoderController,
	backend Backend,
	interval time.Duration,
) *Controller {
	info := encoder.Info()
	return &Controller{
		logger:   logger,
		encoder:  encoder,
		backend:  backend,
		interval: interval,
		updates:  make(chan int, 1),
		close:    make(chan struct{}),
		done:     make(chan struct{}),
		snapshot: Snapshot{
			Backend:                  backend.Name(),
			Active:                   true,
			EncoderTargetBitrateKbps: info.TargetBitrateKbps,
			LastAppliedBitrateKbps:   info.TargetBitrateKbps,
		},
	}
}

func (c *Controller) Start() {
	go c.run()
}

func (c *Controller) UpdateEstimatedBitrate(bps int) {
	select {
	case c.updates <- bps:
	default:
		select {
		case <-c.updates:
		default:
		}
		c.updates <- bps
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *Controller) Close() {
	select {
	case <-c.close:
		return
	default:
		close(c.close)
		<-c.done
	}
}

func (c *Controller) run() {
	defer close(c.done)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	lastEstimate := c.snapshot.EstimatedBitrateBps
	for {
		select {
		case estimate := <-c.updates:
			lastEstimate = estimate
			c.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.EstimatedBitrateBps = estimate
			})
		case <-ticker.C:
			if lastEstimate <= 0 {
				continue
			}
			encoderInfo := c.encoder.Info()
			observation := Observation{
				EstimatedBitrateBps:      lastEstimate,
				EncoderTargetBitrateKbps: encoderInfo.TargetBitrateKbps,
			}
			decision, ok := c.backend.Decide(observation)
			c.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.EncoderTargetBitrateKbps = encoderInfo.TargetBitrateKbps
			})
			if !ok {
				continue
			}
			if err := c.encoder.SetTargetBitrateKbps(decision.TargetBitrateKbps); err != nil {
				c.logger.Warn("Adaptive bitrate update failed: %v", err)
				continue
			}
			c.logger.Debug("Adaptive bitrate applied: %d kbit/s", decision.TargetBitrateKbps)
			c.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.EncoderTargetBitrateKbps = decision.TargetBitrateKbps
				snapshot.LastAppliedBitrateKbps = decision.TargetBitrateKbps
			})
		case <-c.close:
			return
		}
	}
}

func (c *Controller) updateSnapshot(update func(*Snapshot)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	update(&c.snapshot)
}

func NewBackend(cfg config.Config) (Backend, time.Duration, error) {
	switch cfg.AdaptiveBackend() {
	case config.AdaptiveBackendOff:
		return nil, 0, nil
	case config.AdaptiveBackendTWCCGCC:
		interval, err := cfg.AdaptiveUpdateInterval()
		if err != nil {
			return nil, 0, err
		}
		return NewTWCCGCCBackend(cfg), interval, nil
	default:
		return nil, 0, fmt.Errorf("unsupported adaptive backend %q", cfg.AdaptiveBackend())
	}
}
