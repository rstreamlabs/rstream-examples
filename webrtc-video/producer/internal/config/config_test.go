package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected the default configuration to be valid, got %v", err)
	}
	if got := cfg.TunnelTransportMode(); got != "auto" {
		t.Fatalf("default tunnel transport = %q, want auto", got)
	}
}

func TestLoadLegacyTunnelTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.yaml")
	data := []byte("tunnel:\n  transport:\n    useQuic: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() legacy config error = %v", err)
	}
	if got := cfg.TunnelTransportMode(); got != "quic" {
		t.Fatalf("legacy tunnel transport = %q, want quic", got)
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
	t.Setenv("API_URL", "https://video.example.com")
	t.Setenv("DEVICE_SECRET", "dev_test_secret")
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve the test file location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	configs := []string{
		"config.h264.yaml",
		"config.provisioning.h264.yaml",
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

func TestRemoteProvisioningDoesNotRequireLocalTunnelAuth(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Provisioning.Mode = TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = "https://video.example.com"
	cfg.Tunnel.Provisioning.Secret = "dev_test_secret"
	cfg.Tunnel.Auth = TunnelAuthConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected remote provisioning without local tunnel auth to be valid, got %v", err)
	}
}

func TestRemoteProvisioningRejectsLocalTunnelAuthPolicy(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Provisioning.Mode = TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = "https://video.example.com"
	cfg.Tunnel.Provisioning.Secret = "dev_test_secret"
	cfg.Tunnel.Auth.Token = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected remote provisioning with local tunnel auth to fail validation")
	}
}

func TestLocalPublishedViewerCanBePublicByDefault(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Auth = TunnelAuthConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected the published local viewer to allow public access by default, got %v", err)
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
