package media

import "sync/atomic"

type SourceStats struct {
	Sources                  int64
	EncodedBytes             uint64
	EncodedFrames            uint64
	EncodedKeyFrames         uint64
	EncodedMediaNanoseconds  uint64
	LastEncodedFrameUnixNano int64
	DeliveryDroppedBytes     uint64
	DeliveryDroppedFrames    uint64
	SampleExtractionErrors   uint64
	PipelineErrors           uint64
	PipelineCreateErrors     uint64
}

type sourceStats struct {
	sources                  atomic.Int64
	encodedBytes             atomic.Uint64
	encodedFrames            atomic.Uint64
	encodedKeyFrames         atomic.Uint64
	encodedMediaNanoseconds  atomic.Uint64
	lastEncodedFrameUnixNano atomic.Int64
	deliveryDroppedBytes     atomic.Uint64
	deliveryDroppedFrames    atomic.Uint64
	sampleExtractionErrors   atomic.Uint64
	pipelineErrors           atomic.Uint64
	pipelineCreateErrors     atomic.Uint64
}

func (s *sourceStats) snapshot() SourceStats {
	return SourceStats{
		Sources:                  s.sources.Load(),
		EncodedBytes:             s.encodedBytes.Load(),
		EncodedFrames:            s.encodedFrames.Load(),
		EncodedKeyFrames:         s.encodedKeyFrames.Load(),
		EncodedMediaNanoseconds:  s.encodedMediaNanoseconds.Load(),
		LastEncodedFrameUnixNano: s.lastEncodedFrameUnixNano.Load(),
		DeliveryDroppedBytes:     s.deliveryDroppedBytes.Load(),
		DeliveryDroppedFrames:    s.deliveryDroppedFrames.Load(),
		SampleExtractionErrors:   s.sampleExtractionErrors.Load(),
		PipelineErrors:           s.pipelineErrors.Load(),
		PipelineCreateErrors:     s.pipelineCreateErrors.Load(),
	}
}
