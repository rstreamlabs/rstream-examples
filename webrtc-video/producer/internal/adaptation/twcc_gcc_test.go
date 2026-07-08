package adaptation

import (
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestTWCCGCCBackendDropsImmediatelyWhenEstimateFalls(t *testing.T) {
	backend := NewTWCCGCCBackend(config.Default())
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

func TestTWCCGCCBackendRampsUpGradually(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.Backend = config.AdaptiveBackendTWCCGCC
	backend := NewTWCCGCCBackend(cfg)
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
	backend := NewTWCCGCCBackend(cfg)
	decision, ok := backend.Decide(Observation{
		EstimatedBitrateBps:      8_000_000,
		EncoderTargetBitrateKbps: 7700,
	})
	if !ok {
		t.Fatal("expected a final ramp-up step to reach the target")
	}
	if decision.TargetBitrateKbps != 8000 {
		t.Fatalf("expected target bitrate 8000 kbit/s, got %d", decision.TargetBitrateKbps)
	}
}
