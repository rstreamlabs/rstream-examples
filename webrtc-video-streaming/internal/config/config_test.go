package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected the default configuration to be valid, got %v", err)
	}
}

func TestRTXRequiresNACK(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.Interceptors.NACK = false
	cfg.WebRTC.Interceptors.RTX = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected RTX without NACK to fail validation")
	}
}

func TestAV1RequiresAV1Parse(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.Video.MimeType = "video/AV1"
	cfg.WebRTC.Video.PayloadType = 45
	cfg.WebRTC.Video.RTXPayloadType = 46
	cfg.WebRTC.Video.SDPFmtpLine = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected AV1 without av1parse to fail validation")
	}
}

func TestReferenceConfigsAreValid(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve the test file location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	configs := []string{
		"config.h264.yaml",
		"config.av1.yaml",
		"config.macos-webcam.h264.yaml",
		"config.macos-webcam.h264.twcc-gcc.yaml",
		"config.macos-webcam.av1.yaml",
		"config.macos-webcam.av1.twcc-gcc.yaml",
		"config.raspberry-pi-camera.h264.yaml",
		"config.raspberry-pi-camera.h264.twcc-gcc.yaml",
		"config.raspberry-pi-camera.av1.yaml",
		"config.raspberry-pi-camera.av1.twcc-gcc.yaml",
	}
	for _, name := range configs {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(filepath.Join(root, name)); err != nil {
				t.Fatalf("expected %s to load cleanly, got %v", name, err)
			}
		})
	}
}

func TestAdaptiveBackendRequiresTWCC(t *testing.T) {
	cfg := Default()
	cfg.Media.Mode = MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
	cfg.WebRTC.Interceptors.TWCC = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected adaptive backend without TWCC to fail validation")
	}
}

func TestAdaptiveBackendRequiresInitialBitrateWithinBounds(t *testing.T) {
	cfg := Default()
	cfg.Media.Mode = MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
	cfg.WebRTC.InitialBitrateKbps = 1000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected initial bitrate below adaptive minimum to fail validation")
	}
}

func TestAdaptiveBackendEnforcesBitrateBounds(t *testing.T) {
	cfg := Default()
	cfg.Media.Mode = MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
	cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps = 1000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected minimum bitrate below supported range to fail validation")
	}
	cfg = Default()
	cfg.Media.Mode = MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
	cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps = 9000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected maximum bitrate above supported range to fail validation")
	}
}
