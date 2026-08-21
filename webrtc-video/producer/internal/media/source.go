package media

import (
	"context"
	"time"
)

type AccessUnit struct {
	Data     []byte
	Duration time.Duration
	KeyFrame bool
}

type EncoderInfo struct {
	Name              string
	Factory           string
	TargetBitrateKbps int
}

type EncoderController interface {
	Info() EncoderInfo
	SetTargetBitrateKbps(int) error
}

type KeyFrameRequester interface {
	RequestKeyFrame() error
}

type Source interface {
	Start(context.Context) error
	Stop() error
	Subscribe() (<-chan AccessUnit, func())
	Close() error
}

type ControllableSource interface {
	Source
	EncoderController() (EncoderController, bool)
}

type Factory interface {
	New() (Source, error)
}
