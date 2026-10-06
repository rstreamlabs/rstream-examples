package adaptation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

var (
	ErrQualityDisabled = errors.New("quality presets are not configured")
	ErrQualityVersion  = errors.New("quality changed; refresh before selecting a mode")
	ErrQualityMode     = errors.New("unknown quality mode")
)

type QualityState struct {
	Modes                 []config.QualityPreset `json:"modes"`
	Selected              string                 `json:"selected"`
	Version               string                 `json:"version"`
	ActiveEncoders        int                    `json:"activeEncoders"`
	MinAppliedBitrateKbps int                    `json:"minAppliedBitrateKbps"`
	MaxAppliedBitrateKbps int                    `json:"maxAppliedBitrateKbps"`
	FailedUpdates         uint64                 `json:"failedUpdates"`
	SourceFormat          *QualityFormatState    `json:"sourceFormat,omitempty"`
}

// QualityPolicy is device-wide and survives on-demand source lifecycles.
// Its lock serializes selection with encoder updates, never network I/O.
// The epoch prevents delayed requests from a previous process overwriting state.
type QualityPolicy struct {
	mu        sync.RWMutex
	modes     []config.QualityPreset
	selected  string
	epoch     string
	version   uint64
	autoLimit int
}

func NewQualityPolicy(cfg config.Config) (*QualityPolicy, error) {
	if len(cfg.Quality.Presets) == 0 {
		return nil, nil
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return nil, err
	}
	selected := cfg.Quality.Default
	if selected == "" {
		selected = "auto"
	}
	return &QualityPolicy{modes: append([]config.QualityPreset{{ID: "auto", Label: "Auto"}}, cfg.Quality.Presets...), selected: selected, epoch: hex.EncodeToString(epoch[:]), autoLimit: cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps}, nil
}

func (p *QualityPolicy) Snapshot() (QualityState, error) {
	if p == nil {
		return QualityState{}, ErrQualityDisabled
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return QualityState{Modes: append([]config.QualityPreset(nil), p.modes...), Selected: p.selected, Version: p.versionString()}, nil
}

func (p *QualityPolicy) Select(mode, version string) error {
	if p == nil {
		return ErrQualityDisabled
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if version != p.versionString() {
		return ErrQualityVersion
	}
	for _, preset := range p.modes {
		if preset.ID == mode {
			if p.selected != mode {
				p.selected = mode
				p.version++
			}
			return nil
		}
	}
	return ErrQualityMode
}

func (p *QualityPolicy) versionString() string {
	return p.epoch + ":" + strconv.FormatUint(p.version, 10)
}

func (p *QualityPolicy) limitLocked() int {
	for _, preset := range p.modes {
		if preset.ID == p.selected && preset.BitrateKbps > 0 {
			return preset.BitrateKbps
		}
	}
	return p.autoLimit
}

func (p *QualityPolicy) Limit() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.limitLocked()
}

// SourceProfile separates the source-format decision from the bitrate lock.
// Manual bitrate-only presets retain the configured default source format.
func (p *QualityPolicy) SourceProfile(fallback string) (profile string, manual bool) {
	if p == nil {
		return "", false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.selected == "auto" {
		return "", false
	}
	for _, mode := range p.modes {
		if mode.ID == p.selected {
			if mode.SourceProfile != "" {
				return mode.SourceProfile, true
			}
			return fallback, true
		}
	}
	return fallback, true
}
