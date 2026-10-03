package adaptation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

func formatTestConfig() config.SourceFormatConfig {
	return config.SourceFormatConfig{Default: "large", CapsFilter: "source_format", Adaptive: config.SourceFormatAdaptive{Enabled: true}, Profiles: []config.SourceFormatProfile{
		{ID: "small", MinBitrateKbps: 500, Format: media.SourceFormat{Width: 320, Height: 180, FrameRate: media.FrameRate{Numerator: 15, Denominator: 1}}},
		{ID: "medium", MinBitrateKbps: 1500, Format: media.SourceFormat{Width: 640, Height: 360, FrameRate: media.FrameRate{Numerator: 24, Denominator: 1}}},
		{ID: "large", MinBitrateKbps: 3000, Format: media.SourceFormat{Width: 1280, Height: 720, FrameRate: media.FrameRate{Numerator: 30, Denominator: 1}}},
	}}
}

func TestFormatPolicyRequiresSustainedEvidenceAndDwell(t *testing.T) {
	cfg := formatTestConfig()
	p, err := newFormatPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	small, medium, large := cfg.Profiles[0].Format, cfg.Profiles[1].Format, cfg.Profiles[2].Format
	check := func(seconds int, bps int, observed media.SourceFormat, want string, ready bool) {
		t.Helper()
		got, change := p.choose(now.Add(time.Duration(seconds)*time.Second), bps, observed, "", false)
		if change != ready || (ready && got.ID != want) {
			t.Fatalf("t=%ds: target=%s change=%v, want %s/%v", seconds, got.ID, change, want, ready)
		}
	}
	check(0, 900000, large, "", false)
	check(2, 4000000, large, "", false) // A short dip must not reconfigure the source.
	check(3, 900000, large, "", false)
	check(5, 900000, large, "", false)
	check(6, 900000, large, "small", true) // A sustained fall can skip tiers.
	p.confirm(now.Add(6*time.Second), small)
	check(7, 10000000, small, "", false)
	check(21, 10000000, small, "", false)
	check(22, 10000000, small, "medium", true) // Even large headroom raises one tier.
	p.confirm(now.Add(22*time.Second), medium)
	check(23, 500000, medium, "", false)
	check(26, 500000, medium, "", false) // Down-hold elapsed, minimum dwell has not.
	check(32, 500000, medium, "small", true)
	// Manual requests bypass the automatic hold/dwell, but only affect format;
	// the independent bitrate controller continues responding to congestion.
	got, ready := p.choose(now.Add(33*time.Second), 500000, medium, "large", true)
	if !ready || got.Format != large {
		t.Fatalf("manual request: %+v %v", got, ready)
	}
}

func TestFormatPolicyHeadroomMissingEstimateAndLateObservation(t *testing.T) {
	cfg := formatTestConfig()
	p, _ := newFormatPolicy(cfg)
	now := time.Unix(100, 0)
	small, medium, large := cfg.Profiles[0].Format, cfg.Profiles[1].Format, cfg.Profiles[2].Format
	for i := range 20 {
		if _, ok := p.choose(now.Add(time.Duration(i)*time.Second), 1949999, small, "", false); ok {
			t.Fatal("upgrade without 30% headroom")
		}
	}
	p.choose(now.Add(20*time.Second), 1950000, small, "", false)
	p.choose(now.Add(34*time.Second), 0, small, "", false) // Missing telemetry invalidates accumulated evidence.
	if _, ok := p.choose(now.Add(35*time.Second), 1950000, small, "", false); ok {
		t.Fatal("missing estimate counted toward hold")
	}
	if target, ok := p.choose(now.Add(50*time.Second), 1950000, small, "", false); !ok || target.Format != medium {
		t.Fatal("headroom boundary rejected")
	}
	// A timed-out operation can become visible later. That actual observation,
	// not the previous request time, starts the dwell period.
	p.choose(now.Add(51*time.Second), 500000, large, "", false)
	if _, ok := p.choose(now.Add(54*time.Second), 500000, large, "", false); ok {
		t.Fatal("late observation skipped dwell")
	}
	if _, ok := p.choose(now.Add(61*time.Second), 500000, large, "", false); !ok {
		t.Fatal("late observation never settled")
	}
	cfg.Adaptive.Enabled = false
	p, _ = newFormatPolicy(cfg)
	if target, ok := p.choose(now, 0, small, "", false); !ok || target.Format != large {
		t.Fatal("Auto did not restore default with ladder disabled")
	}
}

type pendingFormatRequest struct {
	ctx     context.Context
	format  media.SourceFormat
	confirm chan struct{}
}

type heldFormatController struct {
	mu              sync.Mutex
	state           media.SourceFormatState
	requests        chan pendingFormatRequest
	active, maximum int
}

func (s *heldFormatController) Snapshot() media.SourceFormatState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *heldFormatController) ApplyFormat(ctx context.Context, format media.SourceFormat) (media.SourceFormatState, error) {
	s.mu.Lock()
	s.active++
	s.maximum = max(s.maximum, s.active)
	s.state.Requested, s.state.Pending = format, true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	request := pendingFormatRequest{ctx: ctx, format: format, confirm: make(chan struct{})}
	select {
	case s.requests <- request:
	case <-ctx.Done():
	}
	var err error
	select {
	case <-request.confirm:
	case <-ctx.Done():
		err = ctx.Err()
	}
	s.mu.Lock()
	s.state.Pending = false
	if err == nil {
		s.state.Observed = format
	} else if errors.Is(err, context.DeadlineExceeded) {
		s.state.FailedUpdates++
	}
	state := s.state
	s.mu.Unlock()
	return state, err
}

func receiveFormatRequest(t *testing.T, s *heldFormatController) pendingFormatRequest {
	t.Helper()
	select {
	case request := <-s.requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("format request timed out")
		return pendingFormatRequest{}
	}
}

func TestFormatWorkerCancelsSupersededRequestsWithoutBlockingBitrate(t *testing.T) {
	cfg, quality := testQualityPolicy(t)
	formats := formatTestConfig()
	cfg.Quality.Presets[0].SourceProfile = "small"
	quality, _ = NewQualityPolicy(cfg)
	large := formats.Profiles[2].Format
	source := &heldFormatController{state: media.SourceFormatState{Running: true, Requested: large, Observed: large}, requests: make(chan pendingFormatRequest, 4)}
	logger := logs.NewLogger(logs.NewHub(32), false)
	worker, err := NewFormatWorker(formats, source, quality, func() int { return 4000000 }, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	encoder := &recordingEncoder{target: 5000, updates: make(chan int, 32)}
	bitrate := NewController(logger, encoder, newTestTWCCGCCBackend(t, cfg), time.Hour, nil, nil, nil, quality)
	bitrate.Start()
	t.Cleanup(bitrate.Close)
	selectQuality := func(mode string) {
		t.Helper()
		state, _ := quality.Snapshot()
		if err := quality.Select(mode, state.Version); err != nil {
			t.Fatal(err)
		}
		worker.QualityChanged()
		bitrate.QualityChanged()
	}
	selectQuality("low")
	worker.Start()
	request := receiveFormatRequest(t, source)
	if request.format != formats.Profiles[0].Format {
		t.Fatal("wrong manual profile")
	}
	bitrate.UpdateEstimatedBitrate(700000)
	deadline := time.After(time.Second)
	waiting := true
	for waiting {
		select {
		case value := <-encoder.updates:
			waiting = value != 700
		case <-deadline:
			t.Fatal("pending source transition blocked bitrate decrease")
		}
	}
	selectQuality("high") // Bitrate-only preset restores the default format.
	select {
	case <-request.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("superseded request not cancelled")
	}
	selectQuality("low")
	request = receiveFormatRequest(t, source)
	closed := make(chan struct{})
	go func() { worker.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel source transition")
	}
	if request.ctx.Err() == nil || worker.Snapshot().Running {
		t.Fatal("worker still active after close")
	}
	source.mu.Lock()
	maximum := source.maximum
	source.mu.Unlock()
	if maximum != 1 {
		t.Fatalf("overlapping transitions: %d", maximum)
	}
	worker.QualityChanged()
	worker.Start() // No restart or blocking after Close.
}

func TestFormatWorkerTimeoutBackoffAndConcurrentLifecycle(t *testing.T) {
	formats := formatTestConfig()
	formats.TransitionTimeout = "100ms"
	formats.Adaptive.Enabled = false
	small := formats.Profiles[0].Format
	source := &heldFormatController{state: media.SourceFormatState{Running: true, Requested: small, Observed: small}, requests: make(chan pendingFormatRequest, 4)}
	worker, err := NewFormatWorker(formats, source, nil, nil, logs.NewLogger(logs.NewHub(32), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	worker.Start()
	request := receiveFormatRequest(t, source)
	select {
	case <-request.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("transition not bounded")
	}
	select {
	case <-source.requests:
		t.Fatal("failed transition retried without backoff")
	case <-time.After(600 * time.Millisecond):
	}
	state := worker.Snapshot()
	if state.Pending || state.Observed != small || state.FailedUpdates != 1 {
		t.Fatalf("unconfirmed format reported as applied: %+v", state)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 100 {
				worker.QualityChanged()
				worker.Start()
				_ = worker.Snapshot()
			}
			worker.Close()
		})
	}
	group.Wait()
	if worker.Snapshot().Running {
		t.Fatal("concurrent close left worker running")
	}
}
