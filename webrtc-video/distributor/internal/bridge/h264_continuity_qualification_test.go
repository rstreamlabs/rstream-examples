//go:build qualification

package bridge

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/pion/rtp/codecs"
)

func TestH264ContinuityDecodesAfterLosingASlice(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30",
		"-frames:v", "30", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline",
		"-x264-params", "slices=4:keyint=10:min-keyint=10:scenecut=0:aud=1:repeat-headers=1", "-f", "h264", "pipe:1")
	var errors bytes.Buffer
	command.Stderr = &errors
	encoded, err := command.Output()
	if err != nil {
		t.Fatalf("generate required multi-slice fixture: %v: %s", err, errors.String())
	}
	delimiter := []byte{0, 0, 0, 1, 9}
	frames := bytes.Split(encoded, delimiter)
	if len(frames) != 31 || len(frames[0]) != 0 {
		t.Fatalf("expected 30 access units, got %d", len(frames)-1)
	}
	filter := newH264ContinuityFilter(true)
	payloader := &codecs.H264Payloader{}
	depacketizer := &codecs.H264Packet{}
	var output bytes.Buffer
	var sequence uint16
	now := time.Now()
	emitted := 0
	for index, frame := range frames[1:] {
		payloads := payloader.Payload(500, append(bytes.Clone(delimiter), frame...))
		if index == 2 && len(payloads) < 3 {
			t.Fatal("fixture did not exercise a lost interior packet")
		}
		for n, payload := range payloads {
			sequence++
			if index == 2 && n == len(payloads)/2 {
				continue
			}
			batch := filter.accept(h264Packet(sequence, uint32(index*3000), n == len(payloads)-1, payload...), now)
			if len(batch.packets) != 0 {
				if index >= 2 && index < 10 {
					t.Fatalf("damaged or dependent frame %d escaped", index)
				}
				emitted++
			}
			for _, packet := range batch.packets {
				nalus, decodeErr := depacketizer.Unmarshal(packet.Payload)
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				output.Write(nalus)
			}
		}
	}
	if emitted != 22 {
		t.Fatalf("emitted %d frames, want 22 independently decodable frames", emitted)
	}
	errors.Reset()
	command = exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror", "-f", "h264", "-i", "pipe:0", "-f", "framehash", "pipe:1")
	command.Stdin, command.Stderr = &output, &errors
	decoded, err := command.Output()
	if err != nil {
		t.Fatalf("strict decode after packet loss: %v: %s", err, errors.String())
	}
	count := 0
	for _, line := range bytes.Split(decoded, []byte{'\n'}) {
		if len(line) != 0 && line[0] != '#' {
			count++
		}
	}
	if count != emitted {
		t.Fatalf("decoded %d frames, want %d", count, emitted)
	}
}
