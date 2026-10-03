package webrtc

import (
	"context"
	"errors"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/adaptation"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

func (b *Broadcaster) QualityState() (adaptation.QualityState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.qualityStateLocked()
}

func (b *Broadcaster) qualityStateLocked() (adaptation.QualityState, error) {
	state, err := b.quality.Snapshot()
	if err != nil {
		return state, err
	}
	for _, session := range b.sessions {
		if session.adaptive == nil {
			continue
		}
		snapshot := session.adaptive.Snapshot()
		if !snapshot.Active {
			continue
		}
		bitrate := snapshot.EncoderTargetBitrateKbps
		if state.ActiveEncoders == 0 || bitrate < state.MinAppliedBitrateKbps {
			state.MinAppliedBitrateKbps = bitrate
		}
		if bitrate > state.MaxAppliedBitrateKbps {
			state.MaxAppliedBitrateKbps = bitrate
		}
		state.ActiveEncoders++
		state.FailedUpdates += snapshot.FailedUpdates
	}
	return state, nil
}

func (b *Broadcaster) SelectQuality(ctx context.Context, mode, version string) (adaptation.QualityState, error) {
	if err := ctx.Err(); err != nil {
		return adaptation.QualityState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return adaptation.QualityState{}, errors.New("producer is shutting down")
	}
	if err := ctx.Err(); err != nil {
		return adaptation.QualityState{}, err
	}
	if err := b.quality.Select(mode, version); err != nil {
		return adaptation.QualityState{}, err
	}
	for _, session := range b.sessions {
		if session.adaptive != nil {
			session.adaptive.QualityChanged()
		}
	}
	b.logger.Info("Source quality selected: %s", mode)
	return b.qualityStateLocked()
}

func (b *Broadcaster) newSource() (media.Source, error) {
	source, err := b.sourceFactory.New()
	if err != nil {
		return nil, err
	}
	limit := b.quality.Limit()
	if limit > 0 {
		controller, ok := sourceEncoderController(source)
		if !ok {
			_ = source.Close()
			return nil, errors.New("quality presets require a controllable encoder")
		}
		if controller.Info().TargetBitrateKbps > limit {
			if err := controller.SetTargetBitrateKbps(limit); err != nil {
				_ = source.Close()
				return nil, err
			}
		}
	}
	return source, nil
}
