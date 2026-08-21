package media

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func TestConcurrentStartWaitsForTheActualPipelineTransition(t *testing.T) {
	firstFailure := errors.New("first pipeline transition failed")
	transitionStarted := make(chan struct{})
	releaseTransition := make(chan struct{})
	var transitions atomic.Int32
	source := &GStreamerSource{
		logger: logs.NewLogger(logs.NewHub(8), false),
		subs:   make(map[chan AccessUnit]struct{}),
		stats:  &sourceStats{},
	}
	source.startPipeline = func(context.Context) error {
		if transitions.Add(1) == 1 {
			close(transitionStarted)
			<-releaseTransition
			return firstFailure
		}
		return nil
	}
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- source.Start(context.Background()) }()
	<-transitionStarted
	go func() { second <- source.Start(context.Background()) }()
	select {
	case err := <-second:
		t.Fatalf("concurrent Start returned before the active transition: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseTransition)
	if err := <-first; !errors.Is(err, firstFailure) {
		t.Fatalf("first Start error = %v, want %v", err, firstFailure)
	}
	if err := <-second; err != nil {
		t.Fatalf("second Start did not retry the failed transition: %v", err)
	}
	if got := transitions.Load(); got != 2 {
		t.Fatalf("pipeline transitions = %d, want 2", got)
	}
	source.mu.RLock()
	started := source.started
	source.mu.RUnlock()
	if !started {
		t.Fatal("source was not started after the successful retry")
	}
}

func TestGStreamerStartPropagatesCancellation(t *testing.T) {
	source := &GStreamerSource{
		logger: logs.NewLogger(logs.NewHub(8), false),
		subs:   make(map[chan AccessUnit]struct{}),
		stats:  &sourceStats{},
	}
	source.startPipeline = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := source.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context cancellation", err)
	}
	source.mu.RLock()
	started := source.started
	source.mu.RUnlock()
	if started {
		t.Fatal("source was marked started after cancellation")
	}
}

func TestGStreamerStartRejectsMissingContext(t *testing.T) {
	source := &GStreamerSource{}
	var ctx context.Context
	if err := source.Start(ctx); err == nil {
		t.Fatal("Start() accepted a nil context")
	}
}

func TestGStreamerStartFailureResetsPartialPipelineState(t *testing.T) {
	source, err := NewGStreamerSource("videotestsrc is-live=true ! video/x-raw,width=320,height=240,framerate=30/1 ! videoconvert ! x264enc name=encoder tune=zerolatency bitrate=500 ! h264parse ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=video emit-signals=true sync=false", "video", 500, logs.NewLogger(logs.NewHub(8), false))
	if err != nil {
		t.Fatalf("NewGStreamerSource() error = %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if err := source.pipeline.BlockSetState(gst.StateReady); err != nil {
		t.Fatalf("prepare partial pipeline state: %v", err)
	}
	startFailure := errors.New("synthetic pipeline start failure")
	source.startPipeline = func(context.Context) error { return startFailure }
	if err := source.Start(context.Background()); !errors.Is(err, startFailure) {
		t.Fatalf("Start() error = %v, want %v", err, startFailure)
	}
	status, state := source.pipeline.GetState(gst.StateNull, gst.ClockTime(time.Second))
	if status != gst.StateChangeSuccess || state != gst.StateNull {
		t.Fatalf("pipeline after failed start = state %s status %s, want null success", state, status)
	}
}

func TestGStreamerSourceRestartsWithoutFalseEOS(t *testing.T) {
	hub := logs.NewHub(64)
	source, err := NewGStreamerSource("videotestsrc is-live=true pattern=smpte ! video/x-raw,width=320,height=240,framerate=30/1 ! videoconvert ! x264enc name=encoder tune=zerolatency bitrate=500 key-int-max=30 ! h264parse ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=video emit-signals=true sync=false max-buffers=4 drop=true", "video", 500, logs.NewLogger(hub, false))
	if err != nil {
		t.Fatalf("NewGStreamerSource() error = %v", err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	accessUnits, unsubscribe := source.Subscribe()
	defer unsubscribe()
	for cycle := range 2 {
		if err := source.Start(context.Background()); err != nil {
			t.Fatalf("Start() cycle %d error = %v", cycle, err)
		}
		select {
		case unit := <-accessUnits:
			if len(unit.Data) == 0 {
				t.Fatalf("cycle %d returned an empty access unit", cycle)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("cycle %d produced no access unit", cycle)
		}
		if err := source.Stop(); err != nil {
			t.Fatalf("Stop() cycle %d error = %v", cycle, err)
		}
	}
	source.mu.RLock()
	failure := source.failed
	source.mu.RUnlock()
	if failure != nil {
		t.Fatalf("normal stop marked the source failed: %v", failure)
	}
	for _, entry := range hub.Recent() {
		if entry.Level == "warn" && strings.Contains(strings.ToLower(entry.Message), "end of stream") {
			t.Fatalf("normal stop emitted a false warning: %s", entry.Message)
		}
	}
}

func TestGStreamerEncoderRequestsAKeyFrame(t *testing.T) {
	hub := logs.NewHub(64)
	source, err := NewGStreamerSource("videotestsrc is-live=true pattern=smpte ! video/x-raw,width=320,height=240,framerate=30/1 ! videoconvert ! x264enc name=encoder tune=zerolatency bitrate=500 option-string=scenecut=0 key-int-max=300 ! h264parse ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=video emit-signals=true sync=false max-buffers=4 drop=true", "video", 500, logs.NewLogger(hub, false))
	if err != nil {
		t.Fatalf("NewGStreamerSource() error = %v", err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	accessUnits, unsubscribe := source.Subscribe()
	defer unsubscribe()
	if err := source.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deltaDeadline := time.NewTimer(5 * time.Second)
	defer deltaDeadline.Stop()
	deltaSeen := false
	for !deltaSeen {
		select {
		case unit := <-accessUnits:
			if !unit.KeyFrame {
				deltaSeen = true
			}
		case <-deltaDeadline.C:
			t.Fatal("pipeline produced no delta access unit")
		}
	}
	if err := source.encoder.RequestKeyFrame(); err != nil {
		t.Fatalf("RequestKeyFrame() error = %v", err)
	}
	keyFrameDeadline := time.NewTimer(2 * time.Second)
	defer keyFrameDeadline.Stop()
	for range 15 {
		select {
		case unit := <-accessUnits:
			if unit.KeyFrame {
				return
			}
		case <-keyFrameDeadline.C:
			t.Fatal("pipeline stopped after key-frame request")
		}
	}
	t.Fatal("encoder did not produce a requested key frame within 15 access units")
}
