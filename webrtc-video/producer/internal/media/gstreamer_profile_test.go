package media

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/videoformat"
)

// Check the actual encoded SPS and decoded output, not just a pipeline string.
// Hardware capture examples require their own device qualification; these are
// the bundled synthetic sources used by standalone, provisioned and discovered
// inventory, including the optional source-format example.
func TestBundledH264SourcesFitTheirAdvertisedLevel(t *testing.T) {
	t.Setenv("API_URL", "https://video.example.com")
	t.Setenv("DEVICE_SECRET", "dev_test_secret")
	t.Setenv("VIDEO_DEVICE_ID", "bf011bb6-4d38-4ff8-87a6-99c71d5b3187")
	t.Setenv("VIDEO_DEVICE_NAME", "Qualification source")
	for _, name := range []string{
		"default", "custom-1080p-level-4", "config.h264.yaml", "config.provisioning.h264.yaml",
		"config.provisioning.quality.h264.yaml", "config.discovery.h264.yaml",
		"config.provisioning.source-formats.h264.yaml",
		"config.test-pattern.h264.twcc-gcc.yaml", "config.test-pattern.h264.twcc-gcc-flexfec.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			if name == "custom-1080p-level-4" {
				cfg.Media.Pipeline = strings.ReplaceAll(cfg.Media.Pipeline, "width=1280,height=720", "width=1920,height=1080")
				cfg.Media.Pipeline = strings.ReplaceAll(cfg.Media.Pipeline, "level=(string)3.1", "level=(string)4")
				fmtp := strings.ReplaceAll(*cfg.WebRTC.Video.SDPFmtpLine, "42e01f", "42e028")
				cfg.WebRTC.Video.SDPFmtpLine = &fmtp
			} else if name != "default" {
				var err error
				cfg, err = config.Load(filepath.Join("..", "..", name))
				if err != nil {
					t.Fatal(err)
				}
			}
			limits, err := videoformat.H264Bounds(*cfg.WebRTC.Video.SDPFmtpLine, false)
			if err != nil {
				t.Fatal(err)
			}
			source, err := NewGStreamerSource(cfg.Media.Pipeline, cfg.Media.SinkName,
				cfg.InitialBitrateKbps(), logs.NewLogger(logs.NewHub(16), false))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := source.Close(); err != nil {
					t.Error(err)
				}
			})
			units, unsubscribe := source.Subscribe()
			defer unsubscribe()
			decoded, push := formatTestDecoder(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := source.Start(ctx); err != nil {
				t.Fatal(err)
			}
			observedSPS := false
			for {
				select {
				case unit, open := <-units:
					if !open {
						t.Fatal("source ended before a decoded frame")
					}
					// Annex B always contains a 3-byte start-code suffix, including
					// NAL units preceded by the optional fourth zero byte.
					for _, nalu := range bytes.Split(unit.Data, []byte{0, 0, 1}) {
						if len(nalu) < 4 || nalu[0]&0x1f != 7 {
							continue
						}
						actual, err := videoformat.H264Bounds(fmt.Sprintf("packetization-mode=1;profile-level-id=%x", nalu[1:4]), false)
						if err != nil || actual.Macroblocks > limits.Macroblocks || actual.MacroblocksPerSecond > limits.MacroblocksPerSecond || actual.BitrateKbps > limits.BitrateKbps {
							t.Fatalf("encoded SPS %x exceeds advertised %s: %+v, %v", nalu[1:4], *cfg.WebRTC.Video.SDPFmtpLine, actual, err)
						}
						observedSPS = true
					}
					push(unit)
				case format := <-decoded:
					if !observedSPS || !limits.Allows(format, cfg.InitialBitrateKbps()) {
						t.Fatalf("decoded %s exceeds negotiated source envelope (SPS observed: %v)", format, observedSPS)
					}
					want := SourceFormat{Width: 1280, Height: 720, FrameRate: FrameRate{Numerator: 30, Denominator: 1}}
					if name == "custom-1080p-level-4" {
						want.Width, want.Height = 1920, 1080
					}
					if format != want {
						t.Fatalf("decoded %s, expected %s", format, want)
					}
					t.Logf("encoded SPS and decoded %s fit configured H264 limits", format)
					return
				case <-ctx.Done():
					t.Fatal("source did not produce a decoded frame within the deadline")
				}
			}
		})
	}
}
