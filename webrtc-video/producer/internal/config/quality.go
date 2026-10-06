package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

type QualityConfig struct {
	Default string          `yaml:"default"`
	Presets []QualityPreset `yaml:"presets"`
}

type QualityPreset struct {
	ID            string `yaml:"id" json:"id"`
	Label         string `yaml:"label" json:"label"`
	BitrateKbps   int    `yaml:"bitrateKbps" json:"bitrateKbps"`
	SourceProfile string `yaml:"sourceProfile" json:"sourceProfile,omitempty"`
}

var qualityID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func (c Config) validateQuality() error {
	if len(c.Quality.Presets) == 0 {
		if c.Quality.Default != "" && c.Quality.Default != "auto" {
			return errors.New("quality.default requires a configured preset")
		}
		return nil
	}
	if len(c.Quality.Presets) > 16 {
		return errors.New("quality supports at most 16 presets")
	}
	if c.AdaptiveBackend() != AdaptiveBackendTWCCGCC {
		return errors.New("quality presets require adaptive twcc-gcc")
	}
	if c.Web.WHEP.AllowMediaMTXNativeOffer {
		return errors.New("quality presets require the adaptive WHEP profile; MediaMTX native pull cannot provide it")
	}
	if c.Tunnel.Enabled && c.TunnelProvisioningMode() != TunnelProvisioningModeRemote && !c.HasLocalTunnelAuthPolicy() {
		return errors.New("quality control requires authenticated tunnel access")
	}
	if !c.Tunnel.Enabled {
		host, _, err := net.SplitHostPort(c.Server.Listen)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			return errors.New("local quality control requires a loopback server.listen address")
		}
	}
	seen := map[string]bool{"auto": true}
	for _, preset := range c.Quality.Presets {
		if !qualityID.MatchString(preset.ID) || seen[preset.ID] {
			return fmt.Errorf("invalid or duplicate quality preset id %q", preset.ID)
		}
		if strings.TrimSpace(preset.Label) == "" || !utf8.ValidString(preset.Label) || len(preset.Label) > 80 || strings.IndexFunc(preset.Label, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return errors.New("quality labels must contain 1–80 printable UTF-8 bytes")
		}
		if preset.BitrateKbps < c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps || preset.BitrateKbps > c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps {
			return fmt.Errorf("quality preset %q must be within the adaptive bitrate bounds", preset.ID)
		}
		seen[preset.ID] = true
	}
	if c.Quality.Default != "" && !seen[c.Quality.Default] {
		return errors.New("quality.default must be auto or a configured preset")
	}
	return nil
}
