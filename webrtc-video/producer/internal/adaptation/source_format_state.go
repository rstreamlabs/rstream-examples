package adaptation

import (
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

// QualityFormatState groups encoder observations by configured profile. Its
// size is bounded by the profile count, independent of the number of viewers.
// Requested counts never stand in for observations of encoded key frames.
type QualityFormatState struct {
	DefaultProfile      string                 `json:"defaultProfile"`
	Adaptive            bool                   `json:"adaptive"`
	ActiveEncoders      int                    `json:"activeEncoders"`
	PendingEncoders     int                    `json:"pendingEncoders"`
	UnconfirmedEncoders int                    `json:"unconfirmedEncoders"`
	FailedUpdates       uint64                 `json:"failedUpdates"`
	Profiles            []QualityFormatProfile `json:"profiles"`
}

type QualityFormatProfile struct {
	config.SourceFormatProfile
	RequestedEncoders int `json:"requestedEncoders"`
	ObservedEncoders  int `json:"observedEncoders"`
}

func NewQualityFormatState(cfg *config.SourceFormatConfig) *QualityFormatState {
	if cfg == nil {
		return nil
	}
	state := &QualityFormatState{DefaultProfile: cfg.Default, Adaptive: cfg.Adaptive.Enabled, Profiles: make([]QualityFormatProfile, 0, len(cfg.Profiles))}
	for _, profile := range cfg.Profiles {
		normalized, _ := cfg.Profile(profile.ID)
		state.Profiles = append(state.Profiles, QualityFormatProfile{SourceFormatProfile: normalized})
	}
	return state
}

func (s *QualityFormatState) Add(observation media.SourceFormatState) {
	if s == nil || !observation.Running {
		return
	}
	s.ActiveEncoders++
	if observation.Pending {
		s.PendingEncoders++
	}
	s.FailedUpdates += observation.FailedUpdates
	observed := false
	for index := range s.Profiles {
		profile := &s.Profiles[index]
		if profile.Format == observation.Requested {
			profile.RequestedEncoders++
		}
		if profile.Format == observation.Observed {
			profile.ObservedEncoders++
			observed = true
		}
	}
	if !observed {
		s.UnconfirmedEncoders++
	}
}
