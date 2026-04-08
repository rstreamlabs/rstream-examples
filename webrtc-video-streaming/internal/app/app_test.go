package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/web"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

type fakeTunnelManager struct {
	listener  net.Listener
	publicURL string
	viewerURL string
	authMode  config.TunnelAuthMode
	closeOnce sync.Once
}

func (m *fakeTunnelManager) Listener() net.Listener {
	return m.listener
}

func (m *fakeTunnelManager) PublicURL() string {
	return m.publicURL
}

func (m *fakeTunnelManager) ViewerURL() string {
	return m.viewerURL
}

func (m *fakeTunnelManager) AuthMode() config.TunnelAuthMode {
	return m.authMode
}

func (m *fakeTunnelManager) Close() error {
	var err error
	m.closeOnce.Do(func() {
		err = m.listener.Close()
	})
	return err
}

func TestServeTunnelLoopReconnectsAfterDisconnect(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Reconnect.Enabled = true
	cfg.Tunnel.Reconnect.Interval = "10ms"
	app := newTestApp(cfg)
	openCount := 0
	secondTunnelReady := make(chan struct{}, 1)
	app.openTunnel = func(
		_ context.Context,
		_ config.Config,
		_ *logs.Logger,
		_ *string,
	) (tunnelManager, error) {
		openCount++
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		manager := &fakeTunnelManager{
			listener:  listener,
			publicURL: fmt.Sprintf("https://public-%d.example.com", openCount),
			viewerURL: fmt.Sprintf("https://viewer-%d.example.com", openCount),
			authMode:  config.TunnelAuthModePlain,
		}
		if openCount == 1 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				_ = manager.Close()
			}()
		}
		if openCount == 2 {
			secondTunnelReady <- struct{}{}
		}
		return manager, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.serveTunnelLoop(ctx, http.NewServeMux(), nil)
	}()
	select {
	case <-secondTunnelReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the second tunnel connection")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		info := app.currentInfo()
		if info.ViewerURL != nil && *info.ViewerURL == "https://viewer-2.example.com" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the second tunnel viewer URL to be published, got %#v", info.ViewerURL)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the tunnel loop to stop")
	}
	if openCount != 2 {
		t.Fatalf("expected the tunnel to be opened twice, got %d", openCount)
	}
}

func TestServeTunnelLoopStopsWhenReconnectIsDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Reconnect.Enabled = false
	app := newTestApp(cfg)
	openCount := 0
	app.openTunnel = func(
		_ context.Context,
		_ config.Config,
		_ *logs.Logger,
		_ *string,
	) (tunnelManager, error) {
		openCount++
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		manager := &fakeTunnelManager{
			listener:  listener,
			publicURL: "https://public.example.com",
			viewerURL: "https://viewer.example.com",
			authMode:  config.TunnelAuthModePlain,
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = manager.Close()
		}()
		return manager, nil
	}
	err := app.serveTunnelLoop(context.Background(), http.NewServeMux(), nil)
	if err == nil {
		t.Fatal("expected the tunnel loop to stop with an error")
	}
	if openCount != 1 {
		t.Fatalf("expected a single tunnel attempt, got %d", openCount)
	}
}

func newTestApp(cfg config.Config) *App {
	logHub := logs.NewHub(16)
	logger := logs.NewLogger(logHub, false)
	instance := &App{
		cfg:    cfg,
		logHub: logHub,
		logger: logger,
		info: web.Info{
			AuthMode: cfg.TunnelAuthMode(),
		},
	}
	instance.web = web.NewServer(
		logger,
		logHub,
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("not implemented")
		},
		func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error) {
			return nil, errors.New("not implemented")
		},
	)
	return instance
}
