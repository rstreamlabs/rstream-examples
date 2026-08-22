package adaptation

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

type Snapshot struct {
	Backend                  config.AdaptiveBackend `json:"backend"`
	Active                   bool                   `json:"active"`
	EstimatedBitrateBps      int                    `json:"estimatedBitrateBps"`
	EncoderTargetBitrateKbps int                    `json:"encoderTargetBitrateKbps"`
	LastAppliedBitrateKbps   int                    `json:"lastAppliedBitrateKbps"`
	AppliedUpdates           uint64                 `json:"appliedUpdates"`
	FailedUpdates            uint64                 `json:"failedUpdates"`
}

type Observation struct {
	EstimatedBitrateBps      int
	EncoderTargetBitrateKbps int
	AverageLoss              float64
	LossGuardActive          bool
}

type Decision struct {
	TargetBitrateKbps int
}

type LossState struct {
	Average     float64
	GuardActive bool
}

type Backend interface {
	Name() config.AdaptiveBackend
	Decide(Observation) (Decision, bool)
}

type recoveryKeyFrameBackend interface {
	ConsumeRecoveryKeyFrame() bool
}

const guardedRecoveryKeyFrameQuietPeriod = 250 * time.Millisecond

type Controller struct {
	logger                  *logs.Logger
	encoder                 media.EncoderController
	backend                 Backend
	interval                time.Duration
	estimateSource          func() int
	lossSource              func() LossState
	requestRecoveryKeyFrame func()
	recoveryKeyFrameDelay   time.Duration
	recoveryKeyFrameTimer   *time.Timer
	recoveryKeyFrameTimerC  <-chan time.Time
	lossGuardActive         bool
	latestEstimate          atomic.Int64
	active                  atomic.Bool
	updates                 chan struct{}
	close                   chan struct{}
	done                    chan struct{}
	start                   sync.Once
	stop                    sync.Once
	mu                      sync.RWMutex
	snapshot                Snapshot
}

func NewController(
	logger *logs.Logger,
	encoder media.EncoderController,
	backend Backend,
	interval time.Duration,
	estimateSource func() int,
	lossSource func() LossState,
	requestRecoveryKeyFrame func(),
) *Controller {
	info := encoder.Info()
	return &Controller{
		logger:                  logger,
		encoder:                 encoder,
		backend:                 backend,
		interval:                interval,
		estimateSource:          estimateSource,
		lossSource:              lossSource,
		requestRecoveryKeyFrame: requestRecoveryKeyFrame,
		recoveryKeyFrameDelay:   guardedRecoveryKeyFrameQuietPeriod,
		updates:                 make(chan struct{}, 1),
		close:                   make(chan struct{}),
		done:                    make(chan struct{}),
		snapshot: Snapshot{
			Backend:                  backend.Name(),
			EncoderTargetBitrateKbps: info.TargetBitrateKbps,
			LastAppliedBitrateKbps:   info.TargetBitrateKbps,
		},
	}
}

func (c *Controller) Start() {
	c.start.Do(func() {
		c.active.Store(true)
		go c.run()
	})
}

func (c *Controller) UpdateEstimatedBitrate(bps int) {
	c.latestEstimate.Store(int64(bps))
	select {
	case <-c.close:
		return
	default:
	}
	select {
	case c.updates <- struct{}{}:
		return
	case <-c.close:
		return
	default:
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := c.snapshot
	snapshot.Active = c.active.Load()
	return snapshot
}

func (c *Controller) Close() {
	c.stop.Do(func() {
		close(c.close)
	})
	c.start.Do(func() {
		close(c.done)
	})
	<-c.done
}

func (c *Controller) run() {
	defer func() {
		c.stopRecoveryKeyFrameTimer()
		c.active.Store(false)
		close(c.done)
	}()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	var lastUpdateAttempt time.Time
	for {
		select {
		case <-c.updates:
			estimate := c.currentEstimate()
			c.recordEstimate(estimate)
			if c.applyEstimate(estimate, false) {
				lastUpdateAttempt = time.Now()
			}
		case now := <-ticker.C:
			estimate := c.currentEstimate()
			c.recordEstimate(estimate)
			if estimate <= 0 {
				continue
			}
			if !lastUpdateAttempt.IsZero() && now.Sub(lastUpdateAttempt) < c.interval {
				continue
			}
			if c.applyEstimate(estimate, true) {
				lastUpdateAttempt = now
			}
		case <-c.recoveryKeyFrameTimerC:
			c.recoveryKeyFrameTimerC = nil
			c.requestPendingRecoveryKeyFrame()
		case <-c.close:
			return
		}
	}
}

func (c *Controller) currentEstimate() int {
	if c.estimateSource != nil {
		estimate := c.estimateSource()
		if estimate > 0 {
			c.latestEstimate.Store(int64(estimate))
		}
	}
	return int(c.latestEstimate.Load())
}

func (c *Controller) recordEstimate(estimate int) {
	c.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.EstimatedBitrateBps = estimate
	})
}

func (c *Controller) applyEstimate(estimate int, allowIncrease bool) bool {
	encoderInfo := c.encoder.Info()
	observation := Observation{
		EstimatedBitrateBps:      estimate,
		EncoderTargetBitrateKbps: encoderInfo.TargetBitrateKbps,
	}
	if c.lossSource != nil {
		loss := c.lossSource()
		observation.AverageLoss = loss.Average
		observation.LossGuardActive = loss.GuardActive
	}
	lossGuardStarted := observation.LossGuardActive && !c.lossGuardActive
	c.lossGuardActive = observation.LossGuardActive
	decision, ok := c.backend.Decide(observation)
	c.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.EncoderTargetBitrateKbps = encoderInfo.TargetBitrateKbps
	})
	if !ok || (!allowIncrease && decision.TargetBitrateKbps >= encoderInfo.TargetBitrateKbps) {
		if lossGuardStarted {
			c.schedulePendingRecoveryKeyFrame()
		}
		return false
	}
	if err := c.encoder.SetTargetBitrateKbps(decision.TargetBitrateKbps); err != nil {
		c.logger.Warn("Adaptive bitrate update failed: %v", err)
		c.updateSnapshot(func(snapshot *Snapshot) {
			snapshot.FailedUpdates++
		})
		return true
	}
	if decision.TargetBitrateKbps > encoderInfo.TargetBitrateKbps {
		if c.requestPendingRecoveryKeyFrame() {
			c.stopRecoveryKeyFrameTimer()
		}
	} else if observation.LossGuardActive {
		c.schedulePendingRecoveryKeyFrame()
	}
	c.logger.Debug("Adaptive bitrate applied: %d kbit/s", decision.TargetBitrateKbps)
	c.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.EncoderTargetBitrateKbps = decision.TargetBitrateKbps
		snapshot.LastAppliedBitrateKbps = decision.TargetBitrateKbps
		snapshot.AppliedUpdates++
	})
	return true
}

func (c *Controller) requestPendingRecoveryKeyFrame() bool {
	recovery, ok := c.backend.(recoveryKeyFrameBackend)
	if !ok || c.requestRecoveryKeyFrame == nil || !recovery.ConsumeRecoveryKeyFrame() {
		return false
	}
	c.requestRecoveryKeyFrame()
	return true
}

func (c *Controller) schedulePendingRecoveryKeyFrame() {
	if c.recoveryKeyFrameDelay <= 0 {
		c.requestPendingRecoveryKeyFrame()
		return
	}
	if c.recoveryKeyFrameTimer == nil {
		c.recoveryKeyFrameTimer = time.NewTimer(c.recoveryKeyFrameDelay)
		c.recoveryKeyFrameTimerC = c.recoveryKeyFrameTimer.C
		return
	}
	if !c.recoveryKeyFrameTimer.Stop() {
		select {
		case <-c.recoveryKeyFrameTimer.C:
		default:
		}
	}
	c.recoveryKeyFrameTimer.Reset(c.recoveryKeyFrameDelay)
	c.recoveryKeyFrameTimerC = c.recoveryKeyFrameTimer.C
}

func (c *Controller) stopRecoveryKeyFrameTimer() {
	if c.recoveryKeyFrameTimer == nil {
		return
	}
	if !c.recoveryKeyFrameTimer.Stop() {
		select {
		case <-c.recoveryKeyFrameTimer.C:
		default:
		}
	}
	c.recoveryKeyFrameTimer = nil
	c.recoveryKeyFrameTimerC = nil
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
		backend, err := NewTWCCGCCBackend(cfg)
		if err != nil {
			return nil, 0, err
		}
		return backend, interval, nil
	default:
		return nil, 0, fmt.Errorf("unsupported adaptive backend %q", cfg.AdaptiveBackend())
	}
}
