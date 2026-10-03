package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-gst/go-glib/glib"
	"github.com/go-gst/go-gst/gst"
)

// GStreamerFormatConfig opts in to caps renegotiation at one explicit raw-video
// capsfilter. The caller owns pipeline construction and receiver-limit checks.
// The pipeline must support live renegotiation; arbitrary cameras/encoders are
// not assumed to support it. No pipeline text is rewritten by this controller.
type GStreamerFormatConfig struct {
	CapsFilter        string
	TransitionTimeout time.Duration
}

type gstreamerFormatController struct {
	source     *GStreamerSource
	filter     *gst.Element
	baseCaps   *gst.Caps
	timeout    time.Duration
	transition sync.Mutex
	mu         sync.Mutex
	state      SourceFormatState
	configured SourceFormat
	updates    chan struct{}
	runDone    chan struct{}
	runError   error
	sequence   uint64
}

func newGStreamerFormatController(source *GStreamerSource, cfg *GStreamerFormatConfig) (*gstreamerFormatController, error) {
	if cfg == nil {
		return nil, nil
	}
	if cfg.CapsFilter == "" {
		return nil, errors.New("source format control requires an explicit capsfilter name")
	}
	timeout := cfg.TransitionTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	if timeout < 100*time.Millisecond || timeout > 10*time.Second {
		return nil, errors.New("source format transition timeout must be between 100ms and 10s")
	}
	if source.encoder == nil {
		return nil, errors.New("source format control requires a controllable encoder named encoder")
	}
	filter, err := source.pipeline.GetElementByName(cfg.CapsFilter)
	if err != nil {
		return nil, fmt.Errorf("locate source format capsfilter: %w", err)
	}
	if factory := filter.GetFactory(); factory == nil || factory.GetName() != "capsfilter" {
		return nil, errors.New("source format control element must be a capsfilter")
	}
	value, err := filter.GetProperty("caps")
	if err != nil {
		return nil, fmt.Errorf("read source format caps: %w", err)
	}
	caps, ok := value.(*gst.Caps)
	if !ok || caps == nil || !caps.IsFixed() || caps.GetSize() != 1 || caps.GetStructureAt(0).Name() != "video/x-raw" {
		return nil, errors.New("source format capsfilter requires one fixed video/x-raw caps structure")
	}
	initial, err := formatFromCaps(caps)
	if err != nil {
		return nil, fmt.Errorf("read initial source format: %w", err)
	}
	// Allow already queued buffers with the previous caps until upstream has
	// renegotiated. Completion still requires a key frame with the requested caps.
	modeType, err := filter.GetPropertyType("caps-change-mode")
	if err != nil {
		return nil, fmt.Errorf("inspect caps negotiation mode: %w", err)
	}
	delayed, err := glib.ValueInit(modeType)
	if err != nil {
		return nil, fmt.Errorf("create caps negotiation mode: %w", err)
	}
	delayed.SetEnum(1)
	if err := filter.SetPropertyValue("caps-change-mode", delayed); err != nil {
		return nil, fmt.Errorf("configure delayed caps negotiation: %w", err)
	}
	done := make(chan struct{})
	close(done)
	return &gstreamerFormatController{
		source: source, filter: filter, baseCaps: caps.Copy(), timeout: timeout,
		state: SourceFormatState{Requested: initial}, configured: initial,
		updates: make(chan struct{}, 1), runDone: done, runError: ErrSourceNotRunning,
	}, nil
}

func (s *GStreamerSource) FormatController() (SourceFormatController, bool) {
	if s.format == nil {
		return nil, false
	}
	return s.format, true
}

func (c *gstreamerFormatController) Snapshot() SourceFormatState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *gstreamerFormatController) beginRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runDone = make(chan struct{})
	c.runError = nil
	c.state.Observed = SourceFormat{}
	c.state.Pending = false
	c.state.LastError = ""
}

func (c *gstreamerFormatController) running() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runError == nil {
		c.state.Running = true
	}
}

func (c *gstreamerFormatController) interrupt(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runError == nil {
		close(c.runDone)
	}
	c.runError = err
	c.state.Running = false
	c.state.Pending = false
}

func (c *gstreamerFormatController) observe(sample *gst.Sample) {
	caps := sample.GetCaps()
	if caps == nil || caps.GetSize() != 1 {
		return
	}
	if name := caps.GetStructureAt(0).Name(); name != "video/x-h264" && name != "video/x-av1" {
		return
	}
	format, err := formatFromCaps(caps)
	if err != nil {
		return // Unknown/missing caps are never reported as a successful update.
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runError != nil {
		return
	}
	c.state.Observed = format
	c.sequence++
	select {
	case c.updates <- struct{}{}:
	default:
	}
}

func (c *gstreamerFormatController) ApplyFormat(ctx context.Context, requested SourceFormat) (SourceFormatState, error) {
	if ctx == nil {
		return c.Snapshot(), errors.New("source format context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return c.Snapshot(), err
	}
	format, err := requested.normalized()
	if err != nil {
		return c.Snapshot(), err
	}
	// No request queue and no detached native setter goroutine: callers coalesce
	// desired states and retry busy transitions from their own control worker.
	if !c.transition.TryLock() {
		return c.Snapshot(), ErrFormatBusy
	}
	defer c.transition.Unlock()
	if !c.source.lifecycleMu.TryLock() {
		return c.Snapshot(), ErrFormatBusy
	}
	sequence, done, err := c.submit(ctx, format)
	c.source.lifecycleMu.Unlock()
	if err != nil {
		return c.Snapshot(), err
	}
	if done == nil { // The requested format is already configured and observed.
		return c.Snapshot(), nil
	}
	for {
		c.mu.Lock()
		if done != c.runDone {
			// A stopped pipeline may have restarted before this waiter resumed.
			// Do not overwrite the new run's state with an old request's error.
			state := c.state
			c.mu.Unlock()
			return state, ErrSourceNotRunning
		}
		if c.runError != nil {
			err = c.runError
		} else if ctx.Err() != nil {
			err = ctx.Err()
		} else if c.sequence > sequence && c.state.Observed == format {
			c.state.Pending = false
			state := c.state
			c.mu.Unlock()
			c.source.logger.Info("Video source format confirmed: %s", format)
			return state, nil
		}
		if err != nil {
			c.state.Pending = false
			c.state.LastError = err.Error()
			if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSourceClosed) && !errors.Is(err, ErrSourceNotRunning) {
				c.state.FailedUpdates++
			}
			state := c.state
			c.mu.Unlock()
			if errors.Is(err, context.DeadlineExceeded) {
				c.source.logger.Warn("Video source format confirmation timed out: requested %s, last observed %s", format, state.Observed)
			}
			return state, err
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-done:
		case <-c.updates:
		}
	}
}

// submit runs with both transition and source.lifecycleMu held. It releases the
// lifecycle lock before waiting for output, so Stop/Close can cancel the wait.
func (c *gstreamerFormatController) submit(ctx context.Context, format SourceFormat) (uint64, <-chan struct{}, error) {
	c.source.mu.RLock()
	started, closed, failed := c.source.started, c.source.closed, c.source.failed
	c.source.mu.RUnlock()
	if closed {
		return 0, nil, ErrSourceClosed
	}
	if failed != nil {
		return 0, nil, failed
	}
	if !started {
		return 0, nil, ErrSourceNotRunning
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	c.mu.Lock()
	if c.configured == format && c.state.Observed == format {
		c.state.Requested, c.state.LastError = format, ""
		c.mu.Unlock()
		return 0, nil, nil
	}
	c.state.Requested, c.state.Pending, c.state.LastError = format, true, ""
	sequence, done := c.sequence, c.runDone
	c.mu.Unlock()
	caps := c.baseCaps.Copy()
	structure := caps.GetStructureAt(0)
	for _, field := range []struct {
		name  string
		value any
	}{
		{"width", format.Width}, {"height", format.Height},
		{"framerate", gst.Fraction(format.FrameRate.Numerator, format.FrameRate.Denominator)},
	} {
		if err := structure.SetValue(field.name, field.value); err != nil {
			return 0, nil, c.submitFailed(fmt.Errorf("set source format %s: %w", field.name, err))
		}
	}
	if err := c.filter.SetProperty("caps", caps); err != nil {
		return 0, nil, c.submitFailed(fmt.Errorf("set source format caps: %w", err))
	}
	c.mu.Lock()
	c.configured = format
	c.mu.Unlock()
	if err := c.source.encoder.RequestKeyFrame(); err != nil {
		return 0, nil, c.submitFailed(fmt.Errorf("request source format key frame: %w", err))
	}
	c.source.logger.Info("Video source format requested: %s", format)
	return sequence, done, nil
}

func (c *gstreamerFormatController) submitFailed(err error) error {
	c.source.logger.Warn("Video source format update failed: %v", err)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Pending = false
	c.state.FailedUpdates++
	c.state.LastError = err.Error()
	return err
}

func formatFromCaps(caps *gst.Caps) (SourceFormat, error) {
	if caps == nil || !caps.IsFixed() || caps.GetSize() != 1 {
		return SourceFormat{}, errors.New("video sample has no fixed format")
	}
	structure := caps.GetStructureAt(0)
	width, err := structure.GetValue("width")
	if err != nil {
		return SourceFormat{}, err
	}
	height, err := structure.GetValue("height")
	if err != nil {
		return SourceFormat{}, err
	}
	rate, err := structure.GetValue("framerate")
	if err != nil {
		return SourceFormat{}, err
	}
	w, widthOK := width.(int)
	h, heightOK := height.(int)
	fps, rateOK := rate.(*gst.FractionValue)
	if !widthOK || !heightOK || !rateOK || fps == nil {
		return SourceFormat{}, errors.New("video sample has invalid dimensions or frame rate")
	}
	return (SourceFormat{Width: w, Height: h, FrameRate: FrameRate{fps.Num(), fps.Denom()}}).normalized()
}
