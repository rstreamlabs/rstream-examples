package adaptation

import (
	"math"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

type TWCCGCCBackend struct {
	minBitrateKbps        int
	maxBitrateKbps        int
	changeThresholdPct    int
	decreaseThresholdPct  int
	maxIncreasePct        int
	maxIncreaseStepKbps   int
	maxIncreaseLoss       float64
	increaseHoldAfterLoss time.Duration
	now                   func() time.Time
	mu                    sync.Mutex
	increaseBlockedUntil  time.Time
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
		maxIncreasePct:        cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreasePct,
		maxIncreaseStepKbps:   cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreaseStepKbps,
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
	increaseAllowed := b.allowsIncrease(observation.AverageLoss)
	if target < current {
		if withinThreshold(current, target, b.decreaseThresholdPct) {
			return Decision{}, false
		}
		return Decision{TargetBitrateKbps: target}, true
	}
	if target == current || !increaseAllowed {
		return Decision{}, false
	}
	if withinThreshold(current, target, b.changeThresholdPct) {
		return Decision{}, false
	}
	if target-current <= b.maxIncreaseStepKbps {
		return Decision{TargetBitrateKbps: target}, true
	}
	step := current * b.maxIncreasePct / 100
	if step > b.maxIncreaseStepKbps {
		step = b.maxIncreaseStepKbps
	}
	if step <= 0 {
		step = 1
	}
	next := current + step
	if next > target {
		next = target
	}
	if next == current {
		return Decision{}, false
	}
	return Decision{TargetBitrateKbps: clampKbps(next, b.minBitrateKbps, b.maxBitrateKbps)}, true
}

func (b *TWCCGCCBackend) allowsIncrease(averageLoss float64) bool {
	now := b.now()
	lossIsHigh := math.IsNaN(averageLoss) || math.IsInf(averageLoss, 0) ||
		averageLoss < 0 || averageLoss > b.maxIncreaseLoss
	b.mu.Lock()
	defer b.mu.Unlock()
	if lossIsHigh {
		blockedUntil := now.Add(b.increaseHoldAfterLoss)
		if blockedUntil.After(b.increaseBlockedUntil) {
			b.increaseBlockedUntil = blockedUntil
		}
	}
	return !lossIsHigh && !now.Before(b.increaseBlockedUntil)
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
