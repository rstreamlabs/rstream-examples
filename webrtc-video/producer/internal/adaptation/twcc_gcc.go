package adaptation

import (
	"math"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

type TWCCGCCBackend struct {
	minBitrateKbps          int
	maxBitrateKbps          int
	changeThresholdPct      int
	decreaseThresholdPct    int
	maxIncreaseLoss         float64
	increaseHoldAfterLoss   time.Duration
	now                     func() time.Time
	mu                      sync.Mutex
	increaseBlockedUntil    time.Time
	lossEpisodeActive       bool
	recoveryKeyFramePending bool
}

func NewTWCCGCCBackend(cfg config.Config) (*TWCCGCCBackend, error) {
	increaseHoldAfterLoss, err := cfg.AdaptiveIncreaseHoldAfterLoss()
	if err != nil {
		return nil, err
	}
	return &TWCCGCCBackend{
		minBitrateKbps:        cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps,
		maxBitrateKbps:        cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps,
		changeThresholdPct:    cfg.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct,
		decreaseThresholdPct:  cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct,
		maxIncreaseLoss:       cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct / 100,
		increaseHoldAfterLoss: increaseHoldAfterLoss,
		now:                   time.Now,
	}, nil
}

func (b *TWCCGCCBackend) Name() config.AdaptiveBackend {
	return config.AdaptiveBackendTWCCGCC
}

func (b *TWCCGCCBackend) Decide(observation Observation) (Decision, bool) {
	if observation.EstimatedBitrateBps <= 0 {
		return Decision{}, false
	}
	current := observation.EncoderTargetBitrateKbps
	if current <= 0 {
		current = b.maxBitrateKbps
	}
	target := clampKbps(observation.EstimatedBitrateBps/1000, b.minBitrateKbps, b.maxBitrateKbps)
	if target < current {
		b.updateIncreaseState(observation.AverageLoss)
		if withinThreshold(current, target, b.decreaseThresholdPct) {
			return Decision{}, false
		}
		return Decision{TargetBitrateKbps: target}, true
	}
	if target == current {
		b.updateIncreaseState(observation.AverageLoss)
		return Decision{}, false
	}
	if withinThreshold(current, target, b.changeThresholdPct) {
		b.updateIncreaseState(observation.AverageLoss)
		return Decision{}, false
	}
	increaseAllowed := b.updateIncreaseState(observation.AverageLoss)
	if !increaseAllowed {
		return Decision{}, false
	}
	// GCC already bounds its increase from measured receive rate and delay.
	// Adding another encoder ramp makes the sender application-limited: GCC
	// cannot observe the capacity it has just granted and eventually collapses
	// its estimate to the artificially low source rate.
	return Decision{TargetBitrateKbps: target}, true
}

func (b *TWCCGCCBackend) updateIncreaseState(averageLoss float64) bool {
	now := b.now()
	lossIsHigh := math.IsNaN(averageLoss) || math.IsInf(averageLoss, 0) ||
		averageLoss < 0 || averageLoss > b.maxIncreaseLoss
	b.mu.Lock()
	defer b.mu.Unlock()
	if lossIsHigh {
		if !b.lossEpisodeActive {
			b.lossEpisodeActive = true
			b.recoveryKeyFramePending = true
		}
		blockedUntil := now.Add(b.increaseHoldAfterLoss)
		if blockedUntil.After(b.increaseBlockedUntil) {
			b.increaseBlockedUntil = blockedUntil
		}
		return false
	}
	if now.Before(b.increaseBlockedUntil) {
		return false
	}
	b.lossEpisodeActive = false
	return true
}

func (b *TWCCGCCBackend) ConsumeRecoveryKeyFrame() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := b.recoveryKeyFramePending
	b.recoveryKeyFramePending = false
	return pending
}

func clampKbps(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func withinThreshold(current, target, thresholdPct int) bool {
	if current <= 0 || thresholdPct <= 0 {
		return current == target
	}
	diff := current - target
	if diff < 0 {
		diff = -diff
	}
	return diff*100 <= current*thresholdPct
}
