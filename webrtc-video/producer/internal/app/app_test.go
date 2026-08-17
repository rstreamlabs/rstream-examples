package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/tunnel"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/web"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

type fakeTunnelManager struct {
	listener  net.Listener
	publicURL string
	auth      config.TunnelAuthConfig
	closeOnce sync.Once
}

func (m *fakeTunnelManager) Listener() net.Listener {
	return m.listener
}

func (m *fakeTunnelManager) PublicURL() string {
	return m.publicURL
}

func (m *fakeTunnelManager) Auth() config.TunnelAuthConfig {
	return m.auth
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
		_ tunnel.OpenOptions,
	) (tunnelManager, error) {
		openCount++
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		manager := &fakeTunnelManager{
			listener:  listener,
			publicURL: fmt.Sprintf("https://public-%d.example.com", openCount),
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
		errCh <- app.serveTunnelLoop(ctx, http.NewServeMux())
	}()
	select {
	case <-secondTunnelReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the second tunnel connection")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		publicURL := currentPublicURL(app)
		if publicURL != nil && *publicURL == "https://public-2.example.com" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the second tunnel public URL to be published, got %#v", publicURL)
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
		_ tunnel.OpenOptions,
	) (tunnelManager, error) {
		openCount++
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		manager := &fakeTunnelManager{
			listener:  listener,
			publicURL: "https://public.example.com",
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = manager.Close()
		}()
		return manager, nil
	}
	err := app.serveTunnelLoop(context.Background(), http.NewServeMux())
	if err == nil {
		t.Fatal("expected the tunnel loop to stop with an error")
	}
	if openCount != 1 {
		t.Fatalf("expected a single tunnel attempt, got %d", openCount)
	}
}

func TestRunSkipsLocalServerWhenViewerDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Web.Viewer.Enabled = false
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.Reconnect.Enabled = false
	occupiedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupiedListener.Close() }()
	cfg.Server.Listen = occupiedListener.Addr().String()
	app := newTestApp(cfg)
	tunnelReady := make(chan struct{}, 1)
	app.openTunnel = func(
		_ context.Context,
		_ config.Config,
		_ *logs.Logger,
		_ tunnel.OpenOptions,
	) (tunnelManager, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		tunnelReady <- struct{}{}
		return &fakeTunnelManager{
			listener:  listener,
			publicURL: "https://public.example.com",
		}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx)
	}()
	select {
	case <-tunnelReady:
	case err := <-errCh:
		t.Fatalf("expected app to run through the tunnel only, got %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the tunnel")
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

func TestRunKeepsMetricsOffThePublishedApplicationHandler(t *testing.T) {
	cfg := config.Default()
	cfg.Web.Viewer.Enabled = false
	cfg.Tunnel.Enabled = false
	cfg.Server.Listen = unusedLoopbackAddress(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Listen = unusedLoopbackAddress(t)
	app := newTestApp(cfg)
	app.metrics = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("producer metrics"))
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx)
	}()
	metricsBody := waitForHTTPBody(t, "http://"+cfg.Metrics.Listen+"/metrics")
	if metricsBody != "producer metrics" {
		t.Fatalf("metrics body = %q, want producer metrics", metricsBody)
	}
	response, err := http.Get("http://" + cfg.Server.Listen + "/metrics")
	if err != nil {
		t.Fatalf("request application metrics path: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("application metrics status = %d, want 404", response.StatusCode)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

func TestRunFailsBeforeOpeningTunnelWhenMetricsListenerIsUnavailable(t *testing.T) {
	occupiedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupiedListener.Close() }()
	cfg := config.Default()
	cfg.Web.Viewer.Enabled = false
	cfg.Metrics.Enabled = true
	cfg.Metrics.Listen = occupiedListener.Addr().String()
	app := newTestApp(cfg)
	tunnelOpened := false
	app.openTunnel = func(
		context.Context,
		config.Config,
		*logs.Logger,
		tunnel.OpenOptions,
	) (tunnelManager, error) {
		tunnelOpened = true
		return nil, errors.New("unexpected tunnel open")
	}
	err = app.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to listen for metrics") {
		t.Fatalf("Run() error = %v, want metrics listener failure", err)
	}
	if tunnelOpened {
		t.Fatal("tunnel was opened after metrics listener failure")
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
			TunnelAuth: cfg.Tunnel.Auth,
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

func currentPublicURL(app *App) *string {
	app.infoMu.RLock()
	defer app.infoMu.RUnlock()
	return app.info.PublicURL
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate loopback address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback address: %v", err)
	}
	return address
}

func waitForHTTPBody(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, err := http.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatalf("read %s: %v", url, readErr)
			}
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
