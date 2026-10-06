package config

import (
	"errors"
	"fmt"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/videoformat"
)

// MaximumVideoBitrateKbps is the largest target the configured control policy
// can request. Receiver admission must not depend on the preset selected at the
// instant of negotiation: that source-wide selection can change later.
func (c Config) MaximumVideoBitrateKbps() int {
	maximum := c.InitialBitrateKbps()
	if c.AdaptiveBackend() == AdaptiveBackendTWCCGCC {
		maximum = max(maximum, c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps)
	}
	return maximum
}

func (c Config) validateH264Envelope() error {
	if c.VideoCodec() != VideoCodecH264 {
		return nil
	}
	if c.WebRTC.Video.SDPFmtpLine == nil {
		return errors.New("H264 requires explicit webrtc.video.sdpFmtpLine parameters")
	}
	limits, err := videoformat.H264Bounds(*c.WebRTC.Video.SDPFmtpLine, false)
	if err != nil {
		return fmt.Errorf("invalid H264 sender parameters: %w", err)
	}
	if int64(c.MaximumVideoBitrateKbps()) > limits.BitrateKbps {
		return errors.New("configured encoder bitrate exceeds the H264 sender level")
	}
	return nil
}
