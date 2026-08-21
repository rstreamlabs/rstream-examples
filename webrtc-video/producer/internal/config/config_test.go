package config

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected the default configuration to be valid, got %v", err)
	}
	if got := cfg.TunnelTransportMode(); got != "auto" {
		t.Fatalf("default tunnel transport = %q, want auto", got)
	}
	if cfg.Metrics.Enabled {
		t.Fatal("metrics must be disabled by default")
	}
	if cfg.Metrics.Listen != DefaultMetricsListen {
		t.Fatalf("default metrics listen = %q, want %q", cfg.Metrics.Listen, DefaultMetricsListen)
	}
	if !cfg.Web.WHEP.RequireConfiguredFeatures {
		t.Fatal("WHEP must reject transport feature downgrades by default")
	}
	if cfg.Web.WHEP.AllowMediaMTXNativeOffer {
		t.Fatal("WHEP must remain strict by default")
	}
}

func TestMetricsListenValidation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		listen string
	}{
		{name: "empty", listen: ""},
		{name: "missing port", listen: "127.0.0.1"},
		{name: "invalid port", listen: "127.0.0.1:not-a-port"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			cfg.Metrics.Enabled = true
			cfg.Metrics.Listen = testCase.listen
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid metrics listener to fail validation")
			}
		})
	}
	cfg := Default()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Listen = "127.0.0.1:0"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid metrics listener: %v", err)
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

func TestFlexFECProtectionBounds(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		mediaPackets  uint32
		repairPackets uint32
	}{
		{name: "media exceeds protocol maximum", mediaPackets: 111, repairPackets: 1},
		{name: "repair exceeds protocol maximum", mediaPackets: 110, repairPackets: 111},
		{name: "repair exceeds media group", mediaPackets: 4, repairPackets: 5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			cfg.WebRTC.Interceptors.FlexFEC = true
			cfg.WebRTC.Interceptors.FlexFECMediaPackets = testCase.mediaPackets
			cfg.WebRTC.Interceptors.FlexFECRepairPackets = testCase.repairPackets
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid FlexFEC protection to fail validation")
			}
		})
	}
}

func TestFlexFECProtectionDefaultsRemainBounded(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.Interceptors.FlexFEC = true
	cfg.WebRTC.Interceptors.FlexFECMediaPackets = 0
	cfg.WebRTC.Interceptors.FlexFECRepairPackets = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default FlexFEC protection: %v", err)
	}
	if mediaPackets := cfg.FlexFECMediaPackets(); mediaPackets != 5 {
		t.Fatalf("default protected media packets = %d, want 5", mediaPackets)
	}
	if repairPackets := cfg.FlexFECRepairPackets(); repairPackets != 1 {
		t.Fatalf("default repair packets = %d, want 1", repairPackets)
	}
}

func TestAdaptiveIncreaseHoldAfterLoss(t *testing.T) {
	cfg := Default()
	hold, err := cfg.AdaptiveIncreaseHoldAfterLoss()
	if err != nil {
		t.Fatalf("default increase hold: %v", err)
	}
	if hold != 5*time.Second {
		t.Fatalf("default increase hold = %v, want 5s", hold)
	}
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss = "-1s"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected a negative increase hold to fail validation")
	}
	cfg.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss = "eventually"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an invalid increase hold to fail validation")
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
		"config.test-pattern.h264.twcc-gcc.yaml",
		"config.test-pattern.h264.twcc-gcc-flexfec.yaml",
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

func TestProvisioningReferenceKeepsQualifiedMediaProfile(t *testing.T) {
	t.Setenv("API_URL", "https://video.example.com")
	t.Setenv("DEVICE_SECRET", "dev_test_secret")
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve the test file location")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "config.provisioning.h264.yaml"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load provisioning reference: %v", err)
	}
	if cfg.Web.Viewer.Enabled {
		t.Fatal("provisioning reference must not embed the standalone viewer")
	}
	if !cfg.Web.WHEP.RequireConfiguredFeatures {
		t.Fatal("provisioning reference must enforce its WHEP feature contract")
	}
	if cfg.Web.WHEP.AllowMediaMTXNativeOffer {
		t.Fatal("provisioning reference must keep strict WHEP offer validation")
	}
	if cfg.WebRTC.MaxViewers != 1 || cfg.MediaMode() != MediaModeShared {
		t.Fatalf("provisioning feedback ownership = maxViewers %d, media mode %q; want one shared session", cfg.WebRTC.MaxViewers, cfg.MediaMode())
	}
	interceptors := cfg.WebRTC.Interceptors
	if !interceptors.TWCC || !interceptors.NACK || !interceptors.RTX || !interceptors.FlexFEC {
		t.Fatalf("provisioning repair profile = TWCC %t, NACK %t, RTX %t, FlexFEC %t; want all enabled", interceptors.TWCC, interceptors.NACK, interceptors.RTX, interceptors.FlexFEC)
	}
	if interceptors.FlexFECMediaPackets != 5 || interceptors.FlexFECRepairPackets != 1 {
		t.Fatalf("provisioning FlexFEC ratio = %d/%d, want 1 repair per 5 media", interceptors.FlexFECRepairPackets, interceptors.FlexFECMediaPackets)
	}
	if cfg.AdaptiveBackend() != AdaptiveBackendTWCCGCC {
		t.Fatalf("provisioning adaptive backend = %q, want %q", cfg.AdaptiveBackend(), AdaptiveBackendTWCCGCC)
	}
	for _, fragment := range []string{"vbv-buf-capacity=100", "option-string=scenecut=0", "bframes=0", "alignment=au"} {
		if !strings.Contains(cfg.Media.Pipeline, fragment) {
			t.Fatalf("provisioning media pipeline is missing %q", fragment)
		}
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

func TestRemoteProvisioningAcceptsLoopbackHTTP(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Provisioning.Mode = TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = "http://127.0.0.1:3000/control"
	cfg.Tunnel.Provisioning.Secret = "dev_test_secret"
	cfg.Tunnel.Auth = TunnelAuthConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected loopback HTTP provisioning to be valid, got %v", err)
	}
}

func TestRemoteProvisioningRejectsUnsafeEndpointAndSecret(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		secret   string
	}{
		{name: "remote HTTP", endpoint: "http://video.example.com", secret: "secret"},
		{name: "unsupported scheme", endpoint: "ftp://video.example.com", secret: "secret"},
		{name: "user information", endpoint: "https://user@video.example.com", secret: "secret"},
		{name: "query", endpoint: "https://video.example.com?target=other", secret: "secret"},
		{name: "fragment", endpoint: "https://video.example.com#secret", secret: "secret"},
		{name: "header injection", endpoint: "https://video.example.com", secret: "secret\r\ninjected"},
		{name: "oversized secret", endpoint: "https://video.example.com", secret: strings.Repeat("x", MaxProvisioningSecretBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Tunnel.Provisioning.Mode = TunnelProvisioningModeRemote
			cfg.Tunnel.Provisioning.Endpoint = test.endpoint
			cfg.Tunnel.Provisioning.Secret = test.secret
			cfg.Tunnel.Auth = TunnelAuthConfig{}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected unsafe remote provisioning configuration to fail validation")
			}
		})
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
	cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps = MinBitrateKbps - 1
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

func TestAdaptiveBackendRejectsInvalidIncreaseLossThreshold(t *testing.T) {
	for _, threshold := range []float64{-1, 101, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg := Default()
		cfg.Media.Mode = MediaModePerViewer
		cfg.WebRTC.Adaptive.Enabled = true
		cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
		cfg.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct = threshold
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected loss threshold %v to fail validation", threshold)
		}
	}
}

func TestAdaptiveBackendRejectsInvalidChangeThresholds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		apply func(*WebRTCTWCCGCCBackendConfig)
	}{
		{name: "negative increase threshold", apply: func(cfg *WebRTCTWCCGCCBackendConfig) { cfg.ChangeThresholdPct = -1 }},
		{name: "increase threshold above 100", apply: func(cfg *WebRTCTWCCGCCBackendConfig) { cfg.ChangeThresholdPct = 101 }},
		{name: "negative decrease threshold", apply: func(cfg *WebRTCTWCCGCCBackendConfig) { cfg.DecreaseThresholdPct = -1 }},
		{name: "decrease threshold above 100", apply: func(cfg *WebRTCTWCCGCCBackendConfig) { cfg.DecreaseThresholdPct = 101 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			cfg.Media.Mode = MediaModePerViewer
			cfg.WebRTC.Adaptive.Enabled = true
			cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
			testCase.apply(&cfg.WebRTC.Adaptive.TWCCGCC)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid change threshold to fail validation")
			}
		})
	}
}

func TestMaximumSafeAdaptiveDecreaseThresholdPreservesBurstHeadroom(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		flexFEC       bool
		mediaPackets  uint32
		repairPackets uint32
		want          int
	}{
		{name: "without proactive repair", want: 33},
		{name: "one repair per five media packets", flexFEC: true, mediaPackets: 5, repairPackets: 1, want: 33},
		{name: "two repairs per five media packets", flexFEC: true, mediaPackets: 5, repairPackets: 2, want: 33},
		{name: "two repairs per four media packets", flexFEC: true, mediaPackets: 4, repairPackets: 2, want: 33},
		{name: "repair exceeds media headroom", flexFEC: true, mediaPackets: 4, repairPackets: 3, want: 33},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			cfg.WebRTC.Interceptors.FlexFEC = testCase.flexFEC
			cfg.WebRTC.Interceptors.FlexFECMediaPackets = testCase.mediaPackets
			cfg.WebRTC.Interceptors.FlexFECRepairPackets = testCase.repairPackets
			if got := cfg.MaxSafeAdaptiveDecreaseThresholdPct(); got != testCase.want {
				t.Fatalf("maximum safe decrease threshold = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestAdaptiveBackendRejectsDecreaseThresholdAboveBurstHeadroom(t *testing.T) {
	cfg := Default()
	cfg.Media.Mode = MediaModePerViewer
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.Adaptive.Backend = AdaptiveBackendTWCCGCC
	cfg.WebRTC.Interceptors.FlexFEC = true
	cfg.WebRTC.Interceptors.FlexFECMediaPackets = 4
	cfg.WebRTC.Interceptors.FlexFECRepairPackets = 2
	cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct = 34
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be at most 33") {
		t.Fatalf("expected pacing-headroom validation error, got %v", err)
	}
	cfg.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct = 33
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected immediate decreases to fit the pacing envelope, got %v", err)
	}
}

func TestTURNTransportsNormalizeAndDeduplicate(t *testing.T) {
	cfg := Default()
	cfg.TURN.Transports = []string{" UDP ", "tls", "udp", "DTLS"}
	transports, err := cfg.TURNTransports()
	if err != nil {
		t.Fatalf("normalize TURN transports: %v", err)
	}
	want := []string{"udp", "tls", "dtls"}
	if !reflect.DeepEqual(transports, want) {
		t.Fatalf("TURN transports = %v, want %v", transports, want)
	}
}

func TestTURNTransportsRejectUnknownValue(t *testing.T) {
	cfg := Default()
	cfg.TURN.Transports = []string{"quic"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an unsupported TURN transport to fail validation")
	}
}

func TestTURNTTLRejectsNonPositiveAndUnboundedValues(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "24h0m0.000000001s"} {
		cfg := Default()
		cfg.TURN.TTL = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("TURN TTL %q passed validation", value)
		}
	}
	cfg := Default()
	cfg.TURN.TTL = MaxTURNTTL.String()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("maximum TURN TTL failed validation: %v", err)
	}
}

func TestICETransportPolicyDefaultsToAll(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.ICETransportPolicy = ""
	if got := cfg.ICETransportPolicy(); got != ICETransportPolicyAll {
		t.Fatalf("ICE transport policy = %q, want %q", got, ICETransportPolicyAll)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default ICE transport policy should validate: %v", err)
	}
}

func TestICETransportPolicyRelayRequiresTURN(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.ICETransportPolicy = ICETransportPolicyRelay
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires useTurn") {
		t.Fatalf("expected relay policy validation error, got %v", err)
	}
}

func TestICETransportPolicyRejectsUnknownValue(t *testing.T) {
	cfg := Default()
	cfg.WebRTC.ICETransportPolicy = "sometimes"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "iceTransportPolicy") {
		t.Fatalf("expected invalid ICE transport policy error, got %v", err)
	}
}
