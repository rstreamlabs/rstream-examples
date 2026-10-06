package config

import "testing"

func qualityTestConfig() Config {
	cfg := Default()
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.WebRTC.MaxViewers = 1
	cfg.Tunnel.Auth.Token = true
	cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps = 500
	cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps = 10000
	cfg.Quality.Presets = []QualityPreset{{ID: "low", Label: "Low", BitrateKbps: 1000}, {ID: "high", Label: "High", BitrateKbps: 10000}}
	return cfg
}

func TestQualityConfigurationRequiresBoundedPresetsAndProtectedAdaptiveControl(t *testing.T) {
	if err := qualityTestConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing authentication": func(c *Config) { c.Tunnel.Auth.Token = false },
		"native receiver":        func(c *Config) { c.Web.WHEP.AllowMediaMTXNativeOffer = true },
		"disabled adaptation":    func(c *Config) { c.WebRTC.Adaptive.Enabled = false },
		"below minimum":          func(c *Config) { c.Quality.Presets[0].BitrateKbps = 499 },
		"above maximum":          func(c *Config) { c.Quality.Presets[1].BitrateKbps = 10001 },
		"reserved auto":          func(c *Config) { c.Quality.Presets[0].ID = "auto" },
		"duplicate":              func(c *Config) { c.Quality.Presets[1].ID = "low" },
		"invalid default":        func(c *Config) { c.Quality.Default = "missing" },
		"log injection":          func(c *Config) { c.Quality.Presets[0].ID = "low\ncontrol" },
		"label control":          func(c *Config) { c.Quality.Presets[0].Label = "Low\n" },
		"public local listener":  func(c *Config) { c.Tunnel.Enabled = false; c.Server.Listen = "0.0.0.0:8080" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := qualityTestConfig()
			mutate(&cfg)
			if cfg.Validate() == nil {
				t.Fatal("unsafe quality configuration accepted")
			}
		})
	}
	cfg := qualityTestConfig()
	cfg.Tunnel.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
