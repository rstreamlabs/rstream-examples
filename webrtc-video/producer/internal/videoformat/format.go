// Package videoformat defines transport-independent raw-video format values.
// It has no GStreamer/cgo dependency, so configuration tools can use it too.
package videoformat

import (
	"errors"
	"fmt"
)

type FrameRate struct {
	Numerator   int `yaml:"numerator" json:"numerator"`
	Denominator int `yaml:"denominator" json:"denominator"`
}

// Format does not change the codec or authorize exceeding negotiated receive
// limits. Those limits must be checked separately before sending media.
type Format struct {
	Width     int       `yaml:"width" json:"width"`
	Height    int       `yaml:"height" json:"height"`
	FrameRate FrameRate `yaml:"frameRate" json:"frameRate"`
}

func (f Format) Normalize() (Format, error) {
	if f.Width < 2 || f.Width > 16384 || f.Height < 2 || f.Height > 16384 || f.Width%2 != 0 || f.Height%2 != 0 {
		return Format{}, errors.New("source dimensions must be even integers between 2 and 16384")
	}
	n, d := f.FrameRate.Numerator, f.FrameRate.Denominator
	if n < 1 || n > 1000000 || d < 1 || d > 1000000 || int64(n) > 240*int64(d) {
		return Format{}, errors.New("source frame rate must be positive, at most 240 fps, with numerator and denominator at most 1000000")
	}
	a, b := n, d
	for b != 0 {
		a, b = b, a%b
	}
	f.FrameRate = FrameRate{Numerator: n / a, Denominator: d / a}
	return f, nil
}

func (f Format) String() string {
	return fmt.Sprintf("%dx%d@%d/%d", f.Width, f.Height, f.FrameRate.Numerator, f.FrameRate.Denominator)
}
