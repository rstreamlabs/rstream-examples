package media

import (
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

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
		if err := source.Start(); err != nil {
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
	if err := source.Start(); err != nil {
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
