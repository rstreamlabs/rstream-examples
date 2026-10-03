package media

import (
	"context"
	"errors"
	"fmt"
)

// FrameRate represents a rational frame rate, including rates such as 30000/1001.
type FrameRate struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// SourceFormat describes raw video presented to the encoder. It does not change
// the codec or authorize exceeding a receiver's negotiated decoding limits.
type SourceFormat struct {
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	FrameRate FrameRate `json:"frameRate"`
}

func (f SourceFormat) normalized() (SourceFormat, error) {
	if f.Width < 2 || f.Width > 16384 || f.Height < 2 || f.Height > 16384 || f.Width%2 != 0 || f.Height%2 != 0 {
		return SourceFormat{}, errors.New("source dimensions must be even integers between 2 and 16384")
	}
	n, d := f.FrameRate.Numerator, f.FrameRate.Denominator
	if n < 1 || n > 1000000 || d < 1 || d > 1000000 || int64(n) > 240*int64(d) {
		return SourceFormat{}, errors.New("source frame rate must be positive, at most 240 fps, with numerator and denominator at most 1000000")
	}
	a, b := n, d
	for b != 0 {
		a, b = b, a%b
	}
	f.FrameRate = FrameRate{Numerator: n / a, Denominator: d / a}
	return f, nil
}

func (f SourceFormat) String() string {
	return fmt.Sprintf("%dx%d@%d/%d", f.Width, f.Height, f.FrameRate.Numerator, f.FrameRate.Denominator)
}

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
