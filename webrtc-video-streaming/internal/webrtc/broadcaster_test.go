package webrtc

import (
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/media"
)

type fakeSourceFactory struct{}

func (fakeSourceFactory) New() (media.Source, error) {
	return &fakeSource{subs: make(map[chan media.AccessUnit]struct{})}, nil
}

type fakeSource struct {
	subs map[chan media.AccessUnit]struct{}
}

type closedSourceFactory struct{}

type closedSource struct{}

func (closedSourceFactory) New() (media.Source, error) {
	return closedSource{}, nil
}

func (closedSource) Start() error {
	return nil
}

func (closedSource) Stop() error {
	return nil
}

func (closedSource) Subscribe() (<-chan media.AccessUnit, func()) {
	ch := make(chan media.AccessUnit)
	close(ch)
	return ch, func() {}
}

func (closedSource) Close() error {
	return nil
}

func (s *fakeSource) Start() error {
	return nil
}

func (s *fakeSource) Stop() error {
	return nil
}

func (s *fakeSource) Subscribe() (<-chan media.AccessUnit, func()) {
	ch := make(chan media.AccessUnit, 1)
	s.subs[ch] = struct{}{}
	return ch, func() {
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *fakeSource) Close() error {
	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
	return nil
}

func TestBroadcasterHonorsMaxViewers(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.MaxViewers = 1
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the first session: %v", err)
	}
	defer session.Close("test cleanup")
	if _, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	}); err == nil {
		t.Fatal("expected the second viewer to be rejected")
	}
}

func TestBroadcasterRemovesSessionWhenSourceStopsDuringOpen(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, closedSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("expected the session to close when the source stops")
	}
	deadline := time.Now().Add(time.Second)
	for {
		broadcaster.mu.Lock()
		count := len(broadcaster.sessions)
		broadcaster.mu.Unlock()
		if count == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected no active sessions, got %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionBuffersICECandidatesBeforeOffer(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	defer session.Close("test cleanup")
	mid := "0"
	line := uint16(0)
	if err := session.AddICECandidate(
		"candidate:1 1 udp 2122260223 127.0.0.1 12345 typ host",
		&mid,
		&line,
		nil,
	); err != nil {
		t.Fatalf("expected early ICE candidate to be buffered, got %v", err)
	}
	if got := len(session.pendingICE); got != 1 {
		t.Fatalf("expected one buffered candidate, got %d", got)
	}
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("failed to create offer peer: %v", err)
	}
	defer func() {
		_ = offerer.Close()
	}()
	if _, err := offerer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("failed to add offer transceiver: %v", err)
	}
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("failed to create offer: %v", err)
	}
	if err := offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("failed to set local offer: %v", err)
	}
	if err := session.HandleOffer(offer.SDP); err != nil {
		t.Fatalf("expected offer to flush buffered candidates, got %v", err)
	}
	if got := len(session.pendingICE); got != 0 {
		t.Fatalf("expected no buffered candidates after offer, got %d", got)
	}
}
