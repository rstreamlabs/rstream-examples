package media

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func TestH264RandomAccessClassification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gradual bool
		keys    int
	}{{"IDR", false, 3}, {"gradual refresh", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			// h264parse clears DELTA_UNIT at recovery points too. A WebRTC decoder
			// still needs an IDR to start or recover; the intra-refresh sweep is not one.
			pipeline := fmt.Sprintf("videotestsrc is-live=true num-buffers=65 ! video/x-raw,format=I420,width=128,height=96,framerate=30/1 ! x264enc tune=zerolatency speed-preset=ultrafast bitrate=200 key-int-max=30 intra-refresh=%t bframes=0 byte-stream=true ! h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=video sync=false", tc.gradual)
			source, err := NewGStreamerSource(pipeline, "video", 200, logs.NewLogger(logs.NewHub(8), false))
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			units, unsubscribe := source.Subscribe()
			defer unsubscribe()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = source.Start(ctx); err != nil {
				t.Fatal(err)
			}
			count, keys := 0, 0
			for count < 65 {
				select {
				case unit := <-units:
					count++
					if unit.KeyFrame {
						keys++
					}
				case <-ctx.Done():
					t.Fatalf("only %d source frames: %v", count, ctx.Err())
				}
			}
			if keys != tc.keys {
				t.Fatalf("%d independent key frames; want %d", keys, tc.keys)
			}
		})
	}
}
