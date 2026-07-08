package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

var gstInitOnce sync.Once

// Fallback to 30 fps when GStreamer does not expose a buffer duration.
const (
	defaultAccessUnitDuration = time.Second / 30
	pipelineStopTimeout       = 2 * time.Second
)

type GStreamerFactory struct {
	pipelineDescription string
	sinkName            string
	initialBitrateKbps  int
	logger              *logs.Logger
}

type GStreamerSource struct {
	logger   *logs.Logger
	pipeline *gst.Pipeline
	sink     *app.Sink
	encoder  *gstreamerEncoderController
	busDone  chan struct{}
	stopBus  context.CancelFunc
	failOnce sync.Once
	mu       sync.RWMutex
	subs     map[chan AccessUnit]struct{}
	started  bool
	closed   bool
	failed   error
}

type gstreamerEncoderController struct {
	logger            *logs.Logger
	element           *gst.Element
	name              string
	factory           string
	bitrateProperty   string
	targetBitrateKbps int
	mu                sync.Mutex
}

func NewGStreamerFactory(
	pipelineDescription,
	sinkName string,
	initialBitrateKbps int,
	logger *logs.Logger,
) *GStreamerFactory {
	return &GStreamerFactory{
		pipelineDescription: pipelineDescription,
		sinkName:            sinkName,
		initialBitrateKbps:  initialBitrateKbps,
		logger:              logger,
	}
}

func (f *GStreamerFactory) New() (Source, error) {
	return NewGStreamerSource(f.pipelineDescription, f.sinkName, f.initialBitrateKbps, f.logger)
}

func NewGStreamerSource(
	pipelineDescription,
	sinkName string,
	initialBitrateKbps int,
	logger *logs.Logger,
) (*GStreamerSource, error) {
	gstInitOnce.Do(func() {
		gst.Init(nil)
	})
	pipeline, err := gst.NewPipelineFromString(pipelineDescription)
	if err != nil {
		return nil, fmt.Errorf("failed to create the GStreamer pipeline: %w", err)
	}
	element, err := pipeline.GetElementByName(sinkName)
	if err != nil {
		return nil, fmt.Errorf("failed to locate the appsink %q: %w", sinkName, err)
	}
	sink := app.SinkFromElement(element)
	if sink == nil {
		return nil, fmt.Errorf("element %q is not an appsink", sinkName)
	}
	encoderController, err := newGStreamerEncoderController(pipeline, initialBitrateKbps, logger)
	if err != nil {
		return nil, err
	}
	source := &GStreamerSource{
		logger:   logger,
		pipeline: pipeline,
		sink:     sink,
		encoder:  encoderController,
		busDone:  make(chan struct{}),
		subs:     make(map[chan AccessUnit]struct{}),
	}
	busCtx, cancel := context.WithCancel(context.Background())
	source.stopBus = cancel
	go source.watchBus(busCtx)
	sink.SetDrop(true)
	sink.SetMaxBuffers(4)
	sink.SetWaitOnEOS(false)
	sink.SetBufferListSupport(true)
	sink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(appSink *app.Sink) gst.FlowReturn {
			sample := appSink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}
			data, duration, ok := extractAccessUnit(sample)
			if !ok {
				return gst.FlowError
			}
			source.publish(AccessUnit{
				Data:     data,
				Duration: duration,
				KeyFrame: sampleIsKeyFrame(sample),
			})
			return gst.FlowOK
		},
		EOSFunc: func(_ *app.Sink) {
			source.logger.Warn("GStreamer pipeline reached end of stream")
		},
	})
	return source, nil
}

func (s *GStreamerSource) EncoderController() (EncoderController, bool) {
	if s.encoder == nil {
		return nil, false
	}
	return s.encoder, true
}

func (s *GStreamerSource) Start() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("the GStreamer pipeline is closed")
	}
	if s.failed != nil {
		err := s.failed
		s.mu.Unlock()
		return err
	}
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()
	if err := s.pipeline.BlockSetState(gst.StatePlaying); err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return err
	}
	s.logger.Info("GStreamer pipeline started")
	return nil
}

func (s *GStreamerSource) Stop() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	s.mu.Unlock()
	if err := s.stopPipeline(); err != nil {
		return err
	}
	s.logger.Info("GStreamer pipeline stopped")
	return nil
}

func (s *GStreamerSource) Subscribe() (<-chan AccessUnit, func()) {
	ch := make(chan AccessUnit, 8)
	s.mu.Lock()
	if s.closed || s.failed != nil {
		close(ch)
		s.mu.Unlock()
		return ch, func() {}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

func (s *GStreamerSource) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	wasStarted := s.started
	stopBus := s.stopBus
	s.closed = true
	s.started = false
	s.mu.Unlock()
	var closeErr error
	if wasStarted {
		closeErr = s.stopPipeline()
		if closeErr == nil {
			s.logger.Info("GStreamer pipeline stopped")
		}
	}
	if stopBus != nil {
		stopBus()
	}
	<-s.busDone
	s.mu.Lock()
	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
	s.mu.Unlock()
	return closeErr
}

func (s *GStreamerSource) publish(unit AccessUnit) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- unit:
		default:
		}
	}
}

func (s *GStreamerSource) watchBus(ctx context.Context) {
	defer close(s.busDone)
	bus := s.pipeline.GetPipelineBus()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msg := bus.TimedPop(gst.ClockTime(250 * time.Millisecond))
		if msg == nil {
			continue
		}
		switch msg.Type() {
		case gst.MessageError:
			gerr := msg.ParseError()
			if gerr != nil && gerr.DebugString() != "" {
				s.logger.Error(
					"GStreamer error from %s: %v (%s)",
					msg.Source(),
					gerr,
					gerr.DebugString(),
				)
				s.fail(fmt.Errorf("the GStreamer pipeline failed: %w", gerr))
			} else if gerr != nil {
				s.logger.Error("GStreamer error from %s: %v", msg.Source(), gerr)
				s.fail(fmt.Errorf("the GStreamer pipeline failed: %w", gerr))
			} else {
				s.logger.Error("GStreamer error from %s: unknown error", msg.Source())
				s.fail(errors.New("the GStreamer pipeline failed"))
			}
			return
		case gst.MessageWarning:
			s.logger.Warn("GStreamer warning from %s: %v", msg.Source(), msg.ParseWarning())
		case gst.MessageEOS:
			s.logger.Warn("GStreamer pipeline emitted EOS from %s", msg.Source())
			s.fail(errors.New("the GStreamer pipeline reached end of stream"))
			return
		case gst.MessageStateChanged:
			oldState, newState := msg.ParseStateChanged()
			s.logger.Debug(
				"GStreamer element %s changed state from %s to %s",
				msg.Source(),
				oldState,
				newState,
			)
		}
	}
}

func (s *GStreamerSource) fail(err error) {
	s.failOnce.Do(func() {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.started = false
		s.failed = err
		subs := make([]chan AccessUnit, 0, len(s.subs))
		for ch := range s.subs {
			subs = append(subs, ch)
			delete(s.subs, ch)
		}
		s.mu.Unlock()
		s.pipeline.CallAsync(func() {
			_ = s.pipeline.SendEvent(gst.NewEOSEvent())
			if stopErr := s.pipeline.SetState(gst.StateNull); stopErr != nil {
				s.logger.Warn("GStreamer pipeline stop failed after error: %v", stopErr)
			}
		})
		for _, ch := range subs {
			close(ch)
		}
	})
}

func (s *GStreamerSource) stopPipeline() error {
	done := make(chan error, 1)
	go func() {
		_ = s.pipeline.SendEvent(gst.NewEOSEvent())
		done <- s.pipeline.BlockSetState(gst.StateNull)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(pipelineStopTimeout):
		s.logger.Warn("GStreamer pipeline stop timed out; forcing teardown")
		s.pipeline.AbortState()
		_ = s.pipeline.SetState(gst.StateNull)
		return nil
	}
}

func extractAccessUnit(sample *gst.Sample) ([]byte, time.Duration, bool) {
	if buffer := sample.GetBuffer(); buffer != nil {
		mapInfo := buffer.Map(gst.MapRead)
		if mapInfo == nil {
			return nil, 0, false
		}
		data := append([]byte(nil), mapInfo.Bytes()...)
		buffer.Unmap()
		return data, bufferDuration(buffer), true
	}
	bufferList := sample.GetBufferList()
	if bufferList == nil || bufferList.Length() == 0 {
		return nil, 0, false
	}
	data := make([]byte, 0, bufferList.CalculateSize())
	duration := 0 * time.Millisecond
	bufferList.ForEach(func(buffer *gst.Buffer, _ uint) bool {
		if buffer == nil {
			return true
		}
		mapInfo := buffer.Map(gst.MapRead)
		if mapInfo == nil {
			return true
		}
		data = append(data, mapInfo.Bytes()...)
		buffer.Unmap()
		if duration <= 0 {
			duration = bufferDuration(buffer)
		}
		return true
	})
	if len(data) == 0 {
		return nil, 0, false
	}
	if duration <= 0 {
		duration = defaultAccessUnitDuration
	}
	return data, duration, true
}

func bufferDuration(buffer *gst.Buffer) time.Duration {
	duration := defaultAccessUnitDuration
	if value := buffer.Duration().AsDuration(); value != nil && *value > 0 {
		duration = *value
	}
	return duration
}

func sampleIsKeyFrame(sample *gst.Sample) bool {
	if buffer := sample.GetBuffer(); buffer != nil {
		return !buffer.HasFlags(gst.BufferFlagDeltaUnit)
	}
	bufferList := sample.GetBufferList()
	if bufferList == nil || bufferList.Length() == 0 {
		return false
	}
	firstBuffer := bufferList.GetBufferAt(0)
	if firstBuffer == nil {
		return false
	}
	return !firstBuffer.HasFlags(gst.BufferFlagDeltaUnit)
}

var errGStreamerRequired = errors.New("a GStreamer runtime with the configured pipeline is required")

func GStreamerRequiredError() error {
	return errGStreamerRequired
}

func newGStreamerEncoderController(
	pipeline *gst.Pipeline,
	initialBitrateKbps int,
	logger *logs.Logger,
) (*gstreamerEncoderController, error) {
	element, err := pipeline.GetElementByName("encoder")
	if err != nil {
		logger.Debug("GStreamer pipeline has no element named encoder; dynamic encoder control is unavailable")
		return nil, nil
	}
	factory := element.GetFactory()
	if factory == nil {
		return nil, fmt.Errorf("failed to inspect the GStreamer encoder element %q", element.GetName())
	}
	controller := &gstreamerEncoderController{
		logger:            logger,
		element:           element,
		name:              element.GetName(),
		factory:           factory.GetName(),
		targetBitrateKbps: initialBitrateKbps,
	}
	switch controller.factory {
	case "x264enc":
		controller.bitrateProperty = "bitrate"
	case "av1enc":
		controller.bitrateProperty = "target-bitrate"
	default:
		logger.Debug(
			"GStreamer encoder %s uses the unsupported factory %s; dynamic encoder control is unavailable",
			controller.name,
			controller.factory,
		)
		return nil, nil
	}
	logger.Debug(
		"GStreamer encoder control is available on %s (%s) via property %s",
		controller.name,
		controller.factory,
		controller.bitrateProperty,
	)
	return controller, nil
}

func (c *gstreamerEncoderController) Info() EncoderInfo {
	c.mu.Lock()
	targetBitrate := c.targetBitrateKbps
	c.mu.Unlock()
	return EncoderInfo{
		Name:              c.name,
		Factory:           c.factory,
		TargetBitrateKbps: targetBitrate,
	}
}

func (c *gstreamerEncoderController) SetTargetBitrateKbps(value int) error {
	if value <= 0 {
		return fmt.Errorf("encoder bitrate must be greater than 0, got %d", value)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.element.Set(c.bitrateProperty, uint(value)); err != nil {
		return fmt.Errorf(
			"failed to set %s on GStreamer encoder %s (%s): %w",
			c.bitrateProperty,
			c.name,
			c.factory,
			err,
		)
	}
	c.targetBitrateKbps = value
	c.logger.Debug(
		"GStreamer encoder %s (%s) target bitrate set to %d kbit/s",
		c.name,
		c.factory,
		value,
	)
	return nil
}
