package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/videoformat"
	"gopkg.in/yaml.v3"
)

func sourceFormatTestConfig() Config {
	cfg := qualityTestConfig()
	cfg.Media.Format = &SourceFormatConfig{CapsFilter: "source_format", Default: "large", Profiles: []SourceFormatProfile{
		{ID: "small", Format: videoformat.Format{Width: 320, Height: 180, FrameRate: videoformat.FrameRate{Numerator: 15, Denominator: 1}}, MinBitrateKbps: 500},
		{ID: "large", Format: videoformat.Format{Width: 1280, Height: 720, FrameRate: videoformat.FrameRate{Numerator: 30, Denominator: 1}}, MinBitrateKbps: 2000},
	}, Adaptive: SourceFormatAdaptive{Enabled: true}}
	cfg.Quality.Presets[0].SourceProfile = "small"
	return cfg
}

func TestSourceFormatConfigRoundTripAndOptionalAutomaticOnlyMode(t *testing.T) {
	cfg := sourceFormatTestConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "formats.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	small, ok := loaded.Media.Format.Profile("small")
	if !ok || small.Width != 320 || small.FrameRate.Numerator != 15 || loaded.Quality.Presets[0].SourceProfile != "small" {
		t.Fatalf("lost YAML fields: %+v", loaded.Media.Format)
	}
	timings, err := loaded.Media.Format.Timings()
	if err != nil || timings.Transition != 3*time.Second || timings.DownHold != 3*time.Second || timings.UpHold != 15*time.Second || timings.MinDwell != 10*time.Second || timings.Headroom != 30 {
		t.Fatalf("defaults: %+v %v", timings, err)
	}
	loaded.Quality = QualityConfig{} // An automatic ladder needs no UI presets.
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
	loaded.Media.Format.Adaptive.Enabled = false
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSourceFormatConfigRejectsUnsupportedAndAmbiguousProfiles(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"missing control point": func(c *Config) { c.Media.Format.CapsFilter = "" },
		"invalid control point": func(c *Config) { c.Media.Format.CapsFilter = "source!fake" },
		"no profiles":           func(c *Config) { c.Media.Format.Profiles = nil },
		"too many profiles":     func(c *Config) { c.Media.Format.Profiles = make([]SourceFormatProfile, 17) },
		"missing default":       func(c *Config) { c.Media.Format.Default = "missing" },
		"reserved ID":           func(c *Config) { c.Media.Format.Profiles[0].ID = "auto" },
		"duplicate ID":          func(c *Config) { c.Media.Format.Profiles[0].ID = "large" },
		"duplicate normalized format": func(c *Config) {
			c.Media.Format.Profiles[0].Format = c.Media.Format.Profiles[1].Format
			c.Media.Format.Profiles[0].FrameRate = videoformat.FrameRate{Numerator: 60, Denominator: 2}
		},
		"odd width":                            func(c *Config) { c.Media.Format.Profiles[0].Width = 321 },
		"invalid rate":                         func(c *Config) { c.Media.Format.Profiles[0].FrameRate.Denominator = 0 },
		"unknown preset profile":               func(c *Config) { c.Quality.Presets[0].SourceProfile = "missing" },
		"profile without format configuration": func(c *Config) { c.Media.Format = nil },
		"native receiver":                      func(c *Config) { c.Web.WHEP.AllowMediaMTXNativeOffer = true },
		"non-strict transport":                 func(c *Config) { c.Web.WHEP.RequireConfiguredFeatures = false },
		"no adaptation":                        func(c *Config) { c.WebRTC.Adaptive.Enabled = false },
		"missing fmtp":                         func(c *Config) { c.WebRTC.Video.SDPFmtpLine = nil },
		"AV1 format control":                   func(c *Config) { c.WebRTC.Video.MimeType = "video/AV1" },
		"level exceeded":                       func(c *Config) { c.Media.Format.Profiles[1].Width = 1920; c.Media.Format.Profiles[1].Height = 1080 },
		"bitrate exceeded":                     func(c *Config) { c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps = 15000 },
		"threshold below minimum":              func(c *Config) { c.Media.Format.Profiles[0].MinBitrateKbps = 499 },
		"threshold above maximum":              func(c *Config) { c.Media.Format.Profiles[1].MinBitrateKbps = 10001 },
		"threshold order":                      func(c *Config) { c.Media.Format.Profiles[1].MinBitrateKbps = 500 },
		"one automatic profile":                func(c *Config) { c.Media.Format.Profiles = c.Media.Format.Profiles[1:] },
		"unbounded transition":                 func(c *Config) { c.Media.Format.TransitionTimeout = "1h" },
		"invalid duration":                     func(c *Config) { c.Media.Format.Adaptive.MinDwell = "soon" },
		"too little dwell":                     func(c *Config) { c.Media.Format.Adaptive.MinDwell = "1ms" },
		"too little hysteresis":                func(c *Config) { n := 0; c.Media.Format.Adaptive.UpHeadroomPct = &n },
		"reversed holds":                       func(c *Config) { c.Media.Format.Adaptive.DownHold = "20s" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sourceFormatTestConfig()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid format configuration accepted")
			}
		})
	}
}
