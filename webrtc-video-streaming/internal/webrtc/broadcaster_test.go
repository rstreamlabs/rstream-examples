package webrtc

import (
	"context"
	"testing"
	"time"

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
