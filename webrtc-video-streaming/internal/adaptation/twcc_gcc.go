package adaptation

import "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"

type TWCCGCCBackend struct {
	minBitrateKbps      int
	maxBitrateKbps      int
	changeThresholdPct  int
	maxIncreasePct      int
	maxIncreaseStepKbps int
}

func NewTWCCGCCBackend(cfg config.Config) *TWCCGCCBackend {
	return &TWCCGCCBackend{
		minBitrateKbps:      cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps,
		maxBitrateKbps:      cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps,
		changeThresholdPct:  cfg.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct,
		maxIncreasePct:      cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreasePct,
		maxIncreaseStepKbps: cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreaseStepKbps,
	}
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
	if target > current && target-current <= b.maxIncreaseStepKbps {
		return Decision{TargetBitrateKbps: target}, true
	}
	if withinThreshold(current, target, b.changeThresholdPct) {
		return Decision{}, false
	}
	if target < current {
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
