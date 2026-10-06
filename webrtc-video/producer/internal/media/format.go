package media

import (
	"context"
	"errors"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/videoformat"
)

// FrameRate represents a rational frame rate, including rates such as 30000/1001.
type FrameRate = videoformat.FrameRate

// SourceFormat describes raw video presented to the encoder. It does not change
// the codec or authorize exceeding a receiver's negotiated decoding limits.
type SourceFormat = videoformat.Format

type SourceFormatState struct {
	Running       bool         `json:"running"`
	Requested     SourceFormat `json:"requested"`
	Observed      SourceFormat `json:"observed"`
	Pending       bool         `json:"pending"`
	FailedUpdates uint64       `json:"failedUpdates"`
	LastError     string       `json:"lastError,omitempty"`
}

var (
	ErrFormatBusy       = errors.New("a source transition is already in progress")
	ErrSourceNotRunning = errors.New("the video source is not running")
	ErrSourceClosed     = errors.New("the video source is closed")
)

// SourceFormatController is an optional capability for sources that can change
// dimensions or cadence in place. Implementations serialize transitions, honor
// cancellation and confirm the actual encoded format before reporting success.
// Cancellation stops waiting; an already submitted native/hardware request can
// still take effect. Snapshot must continue reporting those late observations.
// Callers must keep slow format transitions off the congestion-control loop.
type SourceFormatController interface {
	Snapshot() SourceFormatState
	ApplyFormat(context.Context, SourceFormat) (SourceFormatState, error)
}

type FormatControllableSource interface {
	Source
	FormatController() (SourceFormatController, bool)
}
