package config

import (
	"strings"
	"testing"
)

func discoveredLabelsConfig() Config {
	cfg := Default()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.Auth = TunnelAuthConfig{Token: true}
	cfg.Tunnel.Labels = map[string]string{
		"app": "webrtc-video-platform", "inventory": "discovered",
		"device": "85a6703e-04de-42b6-93ac-c3b70c4cab51", "device-name": "Front camera",
	}
	return cfg
}

func TestDiscoveredTunnelLabels(t *testing.T) {
	for name, modify := range map[string]func(*Config){
		"invalid identity":           func(c *Config) { c.Tunnel.Labels["device"] = "camera" },
		"foreign app":                func(c *Config) { c.Tunnel.Labels["app"] = "webtty" },
		"missing authentication":     func(c *Config) { c.Tunnel.Auth.Token = false },
		"interactive authentication": func(c *Config) { c.Tunnel.Auth.Rstream = true },
		"disabled tunnel":            func(c *Config) { c.Tunnel.Enabled = false },
		"remote provisioning":        func(c *Config) { c.Tunnel.Provisioning.Mode = TunnelProvisioningModeRemote },
		"oversized name":             func(c *Config) { c.Tunnel.Labels["device-name"] = strings.Repeat("é", 41) },
		"control name":               func(c *Config) { c.Tunnel.Labels["device-name"] = "front\nback" },
		"format name":                func(c *Config) { c.Tunnel.Labels["device-name"] = "front\u202eback" },
		"empty name":                 func(c *Config) { c.Tunnel.Labels["device-name"] = "" },
		"invalid key":                func(c *Config) { c.Tunnel.Labels[" "] = "value" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := discoveredLabelsConfig()
			modify(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid discovery configuration was accepted")
			}
		})
	}
	cfg := discoveredLabelsConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	delete(cfg.Tunnel.Labels, "device-name")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveredReferenceProfile(t *testing.T) {
	t.Setenv("VIDEO_DEVICE_ID", "85a6703e-04de-42b6-93ac-c3b70c4cab51")
	t.Setenv("VIDEO_DEVICE_NAME", "Front camera")
	cfg, err := Load("../../config.discovery.h264.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TunnelProvisioningMode() != TunnelProvisioningModeLocal || cfg.Tunnel.Labels["device-name"] != "Front camera" {
		t.Fatal("discovered source must use local credentials and the configured display name")
	}
}
