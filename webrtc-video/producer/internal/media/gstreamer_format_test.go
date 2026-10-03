package media

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

const formatTestEncoding = " ! x264enc name=encoder tune=zerolatency speed-preset=ultrafast bitrate=1500 key-int-max=60 bframes=0 byte-stream=true aud=true ! h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au,profile=constrained-baseline ! valve name=output ! appsink name=video sync=false"

func sourceWithFormat(t *testing.T, prefix string, timeout time.Duration) *GStreamerSource {
	t.Helper()
	factory := NewGStreamerFactory(prefix+formatTestEncoding, "video", 1500,
		logs.NewLogger(logs.NewHub(32), false), &GStreamerFormatConfig{CapsFilter: "format", TransitionTimeout: timeout})
	created, err := factory.New()
	if err != nil {
		t.Fatal(err)
	}
	source := created.(*GStreamerSource)
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("close format source: %v", err)
		}
	})
	return source
}

func TestGStreamerFormatChangesDecodeWithoutRestart(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		prefix string
	}{
		{"source-negotiation", "videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,format=I420,width=640,height=360,framerate=30/1"},
		{"scale-and-drop", "videotestsrc is-live=true ! video/x-raw,format=I420,width=640,height=360,framerate=30/1 ! videoscale ! videorate drop-only=true ! capsfilter name=format caps=video/x-raw,format=I420,width=640,height=360,framerate=30/1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := sourceWithFormat(t, scenario.prefix, 0)
			controller, ok := source.FormatController()
			if !ok {
				t.Fatal("explicit capsfilter did not enable format control")
			}
			initial := SourceFormat{640, 360, FrameRate{30, 1}}
			if _, err := controller.ApplyFormat(context.Background(), initial); !errors.Is(err, ErrSourceNotRunning) {
				t.Fatalf("format change before start: %v", err)
			}
			units, unsubscribe := source.Subscribe()
			defer unsubscribe()
			decoded, push := formatTestDecoder(t)
			if err := source.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, target := range []SourceFormat{initial, {320, 180, FrameRate{15, 1}}, {480, 270, FrameRate{24, 1}}, initial} {
				state, err := controller.ApplyFormat(context.Background(), target)
				if err != nil {
					t.Fatalf("apply %s: %v (%+v)", target, err, state)
				}
				if state.Pending || state.Observed != target || state.Requested != target || !state.Running {
					t.Fatalf("unconfirmed format: %+v", state)
				}
				deadline := time.NewTimer(3 * time.Second)
				matched := false
				for !matched {
					select {
					case unit, open := <-units:
						if !open {
							t.Fatal("source ended during format change")
						}
						push(unit)
					case frame := <-decoded:
						matched = frame == target
					case <-deadline.C:
						t.Fatalf("decoder never produced requested format %s", target)
					}
				}
				deadline.Stop()
			}
			caps, err := source.format.filter.GetProperty("caps")
			if err != nil || !strings.Contains(caps.(*gst.Caps).String(), "format=(string)I420") {
				t.Fatalf("format change lost the configured pixel format: %v %v", caps, err)
			}
		})
	}
}

func formatTestDecoder(t *testing.T) (<-chan SourceFormat, func(AccessUnit)) {
	t.Helper()
	pipeline, err := gst.NewPipelineFromString("appsrc name=input is-live=true format=time block=false max-bytes=4194304 ! h264parse ! avdec_h264 ! appsink name=decoded sync=false max-buffers=4 drop=true")
	if err != nil {
		t.Fatal(err)
	}
	inputElement, err := pipeline.GetElementByName("input")
	if err != nil {
		t.Fatal(err)
	}
	input := app.SrcFromElement(inputElement)
	input.SetCaps(gst.NewCapsFromString("video/x-h264,stream-format=byte-stream,alignment=au"))
	outputElement, err := pipeline.GetElementByName("decoded")
	if err != nil {
		t.Fatal(err)
	}
	output := app.SinkFromElement(outputElement)
	decoded := make(chan SourceFormat, 16)
	output.SetCallbacks(&app.SinkCallbacks{NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
		sample := sink.PullSample()
		if sample == nil {
			return gst.FlowEOS
		}
		format, err := formatFromCaps(sample.GetCaps())
		if err != nil {
			return gst.FlowError
		}
		select {
		case decoded <- format:
		default:
		}
		return gst.FlowOK
	}})
	t.Cleanup(func() {
		if err := pipeline.BlockSetState(gst.StateNull); err != nil {
			t.Errorf("stop decoder: %v", err)
		}
		output.SetCallbacks(&app.SinkCallbacks{})
	})
	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		t.Fatal(err)
	}
	var timestamp time.Duration
	return decoded, func(unit AccessUnit) {
		buffer := gst.NewBufferFromBytes(unit.Data)
		buffer.SetPresentationTimestamp(gst.ClockTime(timestamp))
		buffer.SetDuration(gst.ClockTime(unit.Duration))
		timestamp += unit.Duration
		if result := input.PushBuffer(buffer); result != gst.FlowOK {
			t.Fatalf("decoder rejected access unit: %s", result)
		}
	}
}

func TestGStreamerFormatTransitionCancellationAndSerialization(t *testing.T) {
	for _, scenario := range []string{"cancel", "timeout", "stop", "close"} {
		t.Run(scenario, func(t *testing.T) {
			source := sourceWithFormat(t, "videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,format=I420,width=640,height=360,framerate=30/1", 400*time.Millisecond)
			if err := source.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			controller, _ := source.FormatController()
			waitForFormatState(t, controller, func(state SourceFormatState) bool { return state.Observed.Width == 640 })
			valve, err := source.pipeline.GetElementByName("output")
			if err != nil {
				t.Fatal(err)
			}
			if err := valve.SetProperty("drop", true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			target := SourceFormat{320, 180, FrameRate{15, 1}}
			go func() {
				_, err := controller.ApplyFormat(ctx, target)
				done <- err
			}()
			waitForFormatState(t, controller, func(state SourceFormatState) bool { return state.Pending })
			if _, err := controller.ApplyFormat(context.Background(), SourceFormat{640, 360, FrameRate{30, 1}}); !errors.Is(err, ErrFormatBusy) {
				t.Fatalf("concurrent transition was not rejected: %v", err)
			}
			// Slow format confirmation must not hold the bitrate controller's lock.
			if err := source.encoder.SetTargetBitrateKbps(900); err != nil {
				t.Fatal(err)
			}
			var want error
			switch scenario {
			case "cancel":
				cancel()
				want = context.Canceled
			case "timeout":
				want = context.DeadlineExceeded
			case "stop":
				if err := source.Stop(); err != nil {
					t.Fatal(err)
				}
				want = ErrSourceNotRunning
			case "close":
				if err := source.Close(); err != nil {
					t.Fatal(err)
				}
				want = ErrSourceClosed
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("transition returned %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("format waiter outlived cancellation/teardown deadline")
			}
			state := controller.Snapshot()
			if state.Pending || state.Requested != target || state.Observed == target {
				t.Fatalf("unconfirmed caps were reported as applied: %+v", state)
			}
			if scenario == "timeout" && state.FailedUpdates != 1 {
				t.Fatalf("timeout not accounted for: %+v", state)
			}
			if scenario == "cancel" || scenario == "timeout" {
				if err := valve.SetProperty("drop", false); err != nil {
					t.Fatal(err)
				}
				if err := source.encoder.RequestKeyFrame(); err != nil {
					t.Fatal(err)
				}
				waitForFormatState(t, controller, func(state SourceFormatState) bool { return state.Observed == target && !state.Pending })
				state, err = controller.ApplyFormat(context.Background(), target)
				if err != nil || state.LastError != "" {
					t.Fatalf("already observed format did not recover: %+v %v", state, err)
				}
			}
		})
	}
}

func waitForFormatState(t *testing.T, controller SourceFormatController, ready func(SourceFormatState) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready(controller.Snapshot()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("format state deadline: %+v", controller.Snapshot())
}

func TestSourceFormatValidationAndFractionNormalization(t *testing.T) {
	for _, invalid := range []SourceFormat{
		{}, {321, 180, FrameRate{30, 1}}, {320, 181, FrameRate{30, 1}},
		{16386, 180, FrameRate{30, 1}}, {320, 180, FrameRate{1, 0}},
		{320, 180, FrameRate{241, 1}}, {320, 180, FrameRate{-1, 1}},
		{320, 180, FrameRate{1000001, 1000001}},
	} {
		if _, err := invalid.normalized(); err == nil {
			t.Errorf("accepted invalid source format %s", invalid)
		}
	}
	for _, rate := range []FrameRate{{30, 1}, {30000, 1001}, {15, 1}} {
		format := SourceFormat{320, 180, FrameRate{rate.Numerator * 2, rate.Denominator * 2}}
		normalized, err := format.normalized()
		if err != nil || normalized.FrameRate != rate {
			t.Errorf("normalized %s to %s: %v", format, normalized, err)
		}
	}
}

func TestGStreamerFormatRejectsInvalidControlPoints(t *testing.T) {
	for _, pipeline := range []string{
		"videotestsrc is-live=true name=format ! video/x-raw,width=640,height=360,framerate=30/1",
		"videotestsrc is-live=true ! capsfilter name=format",
		"videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,width=640,height=360",
		"videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,width=[320,640],height=360,framerate=30/1",
	} {
		factory := NewGStreamerFactory(pipeline+formatTestEncoding, "video", 1500, logs.NewLogger(logs.NewHub(8), false), &GStreamerFormatConfig{CapsFilter: "format"})
		created, err := factory.New()
		if err == nil {
			_ = created.Close()
			t.Errorf("accepted invalid format control point: %s", pipeline)
		}
	}
}

func TestGStreamerFormatStopRestartAndConcurrentSnapshots(t *testing.T) {
	source := sourceWithFormat(t, "videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,format=I420,width=640,height=360,framerate=30/1", 0)
	controller, _ := source.FormatController()
	done := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					_ = controller.Snapshot()
				}
			}
		})
	}
	defer func() { close(done); readers.Wait() }()
	for range 3 {
		if err := source.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := controller.ApplyFormat(context.Background(), SourceFormat{320, 180, FrameRate{30000, 1001}}); err != nil {
			t.Fatal(err)
		}
		if err := source.Stop(); err != nil {
			t.Fatal(err)
		}
		if controller.Snapshot().Running {
			t.Fatal("stopped source is still reported as running")
		}
	}
}

func TestGStreamerFormatControlDoesNotRetainClosedSource(t *testing.T) {
	ref := func() weak.Pointer[GStreamerSource] {
		factory := NewGStreamerFactory("videotestsrc is-live=true ! capsfilter name=format caps=video/x-raw,format=I420,width=640,height=360,framerate=30/1"+formatTestEncoding,
			"video", 1500, logs.NewLogger(logs.NewHub(8), false), &GStreamerFormatConfig{CapsFilter: "format"})
		created, err := factory.New()
		if err != nil {
			t.Fatal(err)
		}
		source := created.(*GStreamerSource)
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		return weak.Make(source)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		if ref.Value() == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("format controller retained the closed source")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
