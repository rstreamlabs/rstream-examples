package adaptation

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func newTestTWCCGCCBackend(t *testing.T, cfg config.Config) *TWCCGCCBackend {
	t.Helper()
	backend, err := NewTWCCGCCBackend(cfg)
	if err != nil {
		t.Fatalf("create TWCC GCC backend: %v", err)
	}
	return backend
}

func TestTWCCGCCBackendDropsImmediatelyWhenEstimateFalls(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_000_000,
		EncoderTargetBitrateKbps: 5000,
	})
	if !ok {
		t.Fatal("expected a bitrate reduction decision")
	}
	if decision.TargetBitrateKbps != 2000 {
		t.Fatalf("expected target bitrate 2000 kbit/s, got %d", decision.TargetBitrateKbps)
	}
}

func TestTWCCGCCBackendUsesStricterThresholdForDecreases(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct = 10
	cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct = 5
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_000_000,
		EncoderTargetBitrateKbps: 2200,
	})
	if !ok || decision.TargetBitrateKbps != 2000 {
		t.Fatalf("reduction decision = %+v, %v, want 2000 kbit/s", decision, ok)
	}
}

func TestTWCCGCCBackendIgnoresDecreaseWithinDedicatedThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct = 10
	cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct = 5
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_000_000,
		EncoderTargetBitrateKbps: 2100,
	})
	if ok {
		t.Fatalf("reduction decision = %+v, want no change within the decrease threshold", decision)
	}
}

func TestTWCCGCCBackendIgnoresIncreaseWithinChangeThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct = 10
	cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct = 5
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_100_000,
		EncoderTargetBitrateKbps: 2000,
	})
	if ok {
		t.Fatalf("increase decision = %+v, want no change within the increase threshold", decision)
	}
}

func TestTWCCGCCBackendRampsUpGradually(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.Backend = config.AdaptiveBackendTWCCGCC
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      5_000_000,
		EncoderTargetBitrateKbps: 2000,
	})
	if !ok {
		t.Fatal("expected a bitrate increase decision")
	}
	if decision.TargetBitrateKbps != 2300 {
		t.Fatalf("expected gradual ramp-up to 2300 kbit/s, got %d", decision.TargetBitrateKbps)
	}
}

func TestTWCCGCCBackendReachesTargetWhenFinalStepFitsCap(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.Backend = config.AdaptiveBackendTWCCGCC
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_300_000,
		EncoderTargetBitrateKbps: 2000,
	})
	if !ok {
		t.Fatal("expected a final ramp-up step to reach the target")
	}
	if decision.TargetBitrateKbps != 2300 {
		t.Fatalf("expected target bitrate 2300 kbit/s, got %d", decision.TargetBitrateKbps)
	}
}

func TestTWCCGCCBackendDoesNotIncreaseDuringMeasuredLoss(t *testing.T) {
	cfg := config.Default()
	backend := newTestTWCCGCCBackend(t, cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      3_000_000,
		EncoderTargetBitrateKbps: 2000,
		AverageLoss:              0.02,
	})
	if ok {
		t.Fatalf("lossy-link decision = %+v, want no encoder increase", decision)
	}
}

func TestTWCCGCCBackendHoldsIncreasesAfterLossSubsides(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	now := time.Unix(100, 0)
	backend.now = func() time.Time { return now }
	observation := Observation{
		EstimatedBitrateBps:      3_000_000,
		EncoderTargetBitrateKbps: 2000,
		AverageLoss:              0.02,
	}
	if decision, ok := backend.Decide(observation); ok {
		t.Fatalf("lossy-link decision = %+v, want no encoder increase", decision)
	}
	observation.AverageLoss = 0
	now = now.Add(4 * time.Second)
	if decision, ok := backend.Decide(observation); ok {
		t.Fatalf("early recovery decision = %+v, want the increase hold to remain active", decision)
	}
	now = now.Add(time.Second)
	decision, ok := backend.Decide(observation)
	if !ok || decision.TargetBitrateKbps != 2300 {
		t.Fatalf("post-hold decision = %+v, %v, want a 2300 kbit/s increase", decision, ok)
	}
	if !backend.ConsumeRecoveryKeyFrame() {
		t.Fatal("first post-loss increase did not request a recovery key frame")
	}
	decision, ok = backend.Decide(Observation{
		EstimatedBitrateBps:      4_000_000,
		EncoderTargetBitrateKbps: 2300,
	})
	if !ok {
		t.Fatal("expected the recovery ramp to continue")
	}
	if backend.ConsumeRecoveryKeyFrame() {
		t.Fatal("one loss episode requested more than one recovery key frame")
	}
}

func TestTWCCGCCBackendDoesNotRequestKeyFrameForHealthyRamp(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	_, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      3_000_000,
		EncoderTargetBitrateKbps: 2000,
	})
	if !ok {
		t.Fatal("expected a healthy bitrate increase")
	}
	if backend.ConsumeRecoveryKeyFrame() {
		t.Fatal("a healthy bitrate ramp requested an unnecessary key frame")
	}
}

func TestTWCCGCCBackendStillReducesDuringIncreaseHold(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	now := time.Unix(100, 0)
	backend.now = func() time.Time { return now }
	_, _ = backend.Decide(Observation{
		EstimatedBitrateBps:      3_000_000,
		EncoderTargetBitrateKbps: 2000,
		AverageLoss:              0.02,
	})
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      2_000_000,
		EncoderTargetBitrateKbps: 3000,
	})
	if !ok || decision.TargetBitrateKbps != 2000 {
		t.Fatalf("reduction during hold = %+v, %v, want 2000 kbit/s", decision, ok)
	}
}

func TestTWCCGCCBackendRejectsIncreaseWithInvalidLoss(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      3_000_000,
		EncoderTargetBitrateKbps: 2000,
		AverageLoss:              math.NaN(),
	})
	if ok {
		t.Fatalf("invalid-loss decision = %+v, want no encoder increase", decision)
	}
}

func TestTWCCGCCBackendSerializesConcurrentLossState(t *testing.T) {
	backend := newTestTWCCGCCBackend(t, config.Default())
	var wait sync.WaitGroup
	for index := range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = backend.Decide(Observation{
				EstimatedBitrateBps:      3_000_000,
				EncoderTargetBitrateKbps: 2000,
				AverageLoss:              float64(index%2) * 0.02,
			})
		}()
	}
	wait.Wait()
}

func TestNewTWCCGCCBackendRejectsInvalidIncreaseHold(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss = "later"
	if _, err := NewTWCCGCCBackend(cfg); err == nil {
		t.Fatal("expected an invalid increase hold to fail construction")
	}
}
