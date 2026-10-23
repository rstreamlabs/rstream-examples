//go:build qualification

package whipwhep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

func TestICERestartDecodesH264ThroughTURN(t *testing.T) {
	testRestartRenewsCredentialsAndKeepsOneConnectedPeer(t, localTURNConfiguration(t), startRestartDecoderProbe)
}

type decoderDiagnostics struct {
	bytes.Buffer
}

func (d *decoderDiagnostics) Write(value []byte) (int, error) {
	count := len(value)
	if remaining := 16*1024 - d.Len(); remaining > 0 {
		_, _ = d.Buffer.Write(value[:min(remaining, count)])
	}
	return count, nil
}

func startRestartDecoderProbe(t *testing.T, serverPeer, clientPeer *webrtc.PeerConnection) func() {
	t.Helper()
	seconds := 10
	if value := os.Getenv("RSTREAM_RESTART_PHASE_SECONDS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 600 {
			t.Fatal("RSTREAM_RESTART_PHASE_SECONDS must be between 1 and 600")
		}
		seconds = parsed
	}
	encoded, err := os.ReadFile("../bridge/testdata/qualification-baseline-30fps.h264.b64")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	delimiter := []byte{0, 0, 0, 1, 9}
	parts := bytes.Split(stream, delimiter)
	if len(parts) != 31 || len(parts[0]) != 0 {
		t.Fatal("expected one 30-frame H264 GOP")
	}
	units := make([][]byte, 0, 30)
	for _, part := range parts[1:] {
		units = append(units, append(append([]byte(nil), delimiter...), part...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-probesize", "32", "-analyzeduration", "0", "-f", "h264", "-i", "pipe:0", "-an", "-f", "framehash", "pipe:1")
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cancel()
		t.Fatal(err)
	}
	diagnostics := &decoderDiagnostics{}
	command.Stderr = diagnostics
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		cancel()
		t.Fatalf("start required ffmpeg decoder: %v", err)
	}
	var frames atomic.Uint64
	decoded := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "0,") {
				frames.Add(1)
			}
		}
		decoded <- scanner.Err()
	}()
	received := make(chan struct{})
	clientPeer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		defer close(received)
		depacketizer := &codecs.H264Packet{}
		started := false
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			payload, err := depacketizer.Unmarshal(packet.Payload)
			if err != nil {
				continue
			}
			if !started {
				if len(payload) < 5 || payload[4]&0x1f != 7 {
					continue
				}
				started = true
			}
			if _, err := input.Write(payload); err != nil {
				return
			}
		}
	})
	written := make(chan struct{})
	track := serverPeer.GetSenders()[0].Track().(*webrtc.TrackLocalStaticRTP)
	go func() {
		defer close(written)
		ticker := time.NewTicker(time.Second / 30)
		defer ticker.Stop()
		payloader := &codecs.H264Payloader{}
		var sequence uint16
		var frame uint32
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				payloads := payloader.Payload(1188, units[int(frame)%len(units)])
				for index, payload := range payloads {
					sequence++
					if err := track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: sequence, Timestamp: frame * 3000, Marker: index == len(payloads)-1}, Payload: payload}); err != nil {
						return
					}
				}
				frame++
			}
		}
	}()
	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = input.Close()
		shutdown := time.AfterFunc(3*time.Second, cancel)
		select {
		case err := <-decoded:
			if err != nil && err != io.EOF {
				t.Errorf("decoder output: %v", err)
			}
		case <-time.After(4 * time.Second):
			t.Error("decoder reader did not stop")
		}
		if err := command.Wait(); err != nil {
			t.Errorf("decoder exit: %v; %s", err, diagnostics.String())
		}
		shutdown.Stop()
		cancel()
		select {
		case <-written:
		case <-time.After(time.Second):
			t.Error("H264 writer did not stop")
		}
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Error("RTP receiver did not stop")
		}
	})
	return func() {
		for second := 0; second < seconds; second++ {
			before := frames.Load()
			deadline := time.Now().Add(5 * time.Second)
			for frames.Load() < before+15 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if frames.Load() < before+15 {
				t.Fatalf("decoded video stalled after ICE restart: before=%d after=%d", before, frames.Load())
			}
			time.Sleep(500 * time.Millisecond)
			if (second+1)%60 == 0 {
				t.Logf("relay phase progress: %d seconds, frame %d", second+1, frames.Load())
			}
		}
		t.Logf("relay phase decoded through frame %d", frames.Load())
	}
}
