package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/videoformat"
)

type SourceFormatProfile struct {
	ID                 string `yaml:"id" json:"id"`
	videoformat.Format `yaml:",inline"`
	MinBitrateKbps     int `yaml:"minBitrateKbps" json:"-"`
}

type SourceFormatConfig struct {
	CapsFilter        string                `yaml:"capsFilter"`
	TransitionTimeout string                `yaml:"transitionTimeout"`
	Default           string                `yaml:"default"`
	Profiles          []SourceFormatProfile `yaml:"profiles"`
	Adaptive          SourceFormatAdaptive  `yaml:"adaptive"`
}

type SourceFormatAdaptive struct {
	Enabled       bool   `yaml:"enabled"`
	DownHold      string `yaml:"downHold"`
	UpHold        string `yaml:"upHold"`
	MinDwell      string `yaml:"minDwell"`
	UpHeadroomPct *int   `yaml:"upHeadroomPct"`
}

type SourceFormatTimings struct {
	Transition time.Duration
	DownHold   time.Duration
	UpHold     time.Duration
	MinDwell   time.Duration
	Headroom   int
}

func (c SourceFormatConfig) Timings() (SourceFormatTimings, error) {
	timings := SourceFormatTimings{Transition: 3 * time.Second, DownHold: 3 * time.Second, UpHold: 15 * time.Second, MinDwell: 10 * time.Second, Headroom: 30}
	for _, field := range []struct {
		name, value string
		target      *time.Duration
		min, max    time.Duration
	}{
		{"transitionTimeout", c.TransitionTimeout, &timings.Transition, 100 * time.Millisecond, 10 * time.Second},
		{"adaptive.downHold", c.Adaptive.DownHold, &timings.DownHold, time.Second, time.Minute},
		{"adaptive.upHold", c.Adaptive.UpHold, &timings.UpHold, time.Second, 5 * time.Minute},
		{"adaptive.minDwell", c.Adaptive.MinDwell, &timings.MinDwell, time.Second, 5 * time.Minute},
	} {
		if field.value == "" {
			continue
		}
		value, err := time.ParseDuration(field.value)
		if err != nil || value < field.min || value > field.max {
			return timings, fmt.Errorf("media.format.%s must be between %s and %s", field.name, field.min, field.max)
		}
		*field.target = value
	}
	if c.Adaptive.UpHeadroomPct != nil {
		timings.Headroom = *c.Adaptive.UpHeadroomPct
	}
	if timings.Headroom < 10 || timings.Headroom > 100 {
		return timings, errors.New("media.format.adaptive.upHeadroomPct must be between 10 and 100")
	}
	if timings.UpHold < timings.DownHold {
		return timings, errors.New("source format upHold must be at least downHold")
	}
	return timings, nil
}

func (c SourceFormatConfig) Profile(id string) (SourceFormatProfile, bool) {
	for _, profile := range c.Profiles {
		if profile.ID == id {
			profile.Format, _ = profile.Format.Normalize() // Validated at load/startup.
			return profile, true
		}
	}
	return SourceFormatProfile{}, false
}

var capsFilterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

func (c Config) validateSourceFormat() error {
	format := c.Media.Format
	if format == nil {
		for _, preset := range c.Quality.Presets {
			if preset.SourceProfile != "" {
				return errors.New("quality sourceProfile requires media.format configuration")
			}
		}
		return nil
	}
	if !capsFilterName.MatchString(format.CapsFilter) || len(format.Profiles) < 1 || len(format.Profiles) > 16 {
		return errors.New("media.format requires a capsfilter name and 1–16 format profiles")
	}
	if c.AdaptiveBackend() != AdaptiveBackendTWCCGCC || c.Web.WHEP.AllowMediaMTXNativeOffer || !c.Web.WHEP.RequireConfiguredFeatures {
		return errors.New("source format control requires strict adaptive WHEP negotiation (twcc-gcc, requireConfiguredFeatures, no native MediaMTX offer)")
	}
	if _, err := format.Timings(); err != nil {
		return err
	}
	if c.VideoCodec() != VideoCodecH264 || c.WebRTC.Video.SDPFmtpLine == nil {
		return errors.New("configured source format profiles currently require H264; other codecs retain bitrate-only control")
	}
	seen := make(map[string]bool, len(format.Profiles))
	formats := make(map[videoformat.Format]bool, len(format.Profiles))
	previousThreshold := 0
	for _, profile := range format.Profiles {
		if !qualityID.MatchString(profile.ID) || profile.ID == "auto" || seen[profile.ID] {
			return errors.New("source format profile IDs must be unique and cannot be auto")
		}
		normalized, err := profile.Format.Normalize()
		if err != nil {
			return fmt.Errorf("source format profile %q: %w", profile.ID, err)
		}
		if formats[normalized] {
			return errors.New("source format profiles must describe distinct formats")
		}
		if format.Adaptive.Enabled {
			if profile.MinBitrateKbps < c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps || profile.MinBitrateKbps > c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps || profile.MinBitrateKbps <= previousThreshold {
				return errors.New("automatic source format thresholds must increase within the adaptive bitrate bounds")
			}
			previousThreshold = profile.MinBitrateKbps
		} else if profile.MinBitrateKbps < 0 || profile.MinBitrateKbps > MaxBitrateKbps {
			return errors.New("source format minBitrateKbps is outside supported bounds")
		}
		seen[profile.ID], formats[normalized] = true, true
	}
	if !seen[format.Default] {
		return errors.New("media.format.default must identify a configured profile")
	}
	if format.Adaptive.Enabled && len(format.Profiles) < 2 {
		return errors.New("automatic source format selection requires at least two profiles")
	}
	for _, preset := range c.Quality.Presets {
		if preset.SourceProfile != "" && !seen[preset.SourceProfile] {
			return fmt.Errorf("quality preset %q references an unknown sourceProfile", preset.ID)
		}
	}
	return ValidateSourceFormatBounds(*format, c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps, *c.WebRTC.Video.SDPFmtpLine, false)
}

func ValidateSourceFormatBounds(cfg SourceFormatConfig, bitrateKbps int, fmtp string, receiver bool) error {
	limits, err := videoformat.H264Bounds(fmtp, receiver)
	if err != nil {
		return err
	}
	for _, profile := range cfg.Profiles {
		if !limits.Allows(profile.Format, bitrateKbps) {
			return fmt.Errorf("source format profile %q exceeds H264 frame-size, frame-rate or bitrate limits", profile.ID)
		}
	}
	return nil
}
