package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

func TestStatusReturnsLowercaseTunnelAuthFields(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		hub,
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("not implemented")
		},
		func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error) {
			return nil, errors.New("not implemented")
		},
	)
	server.SetInfo(Info{
		TunnelAuth: config.TunnelAuthConfig{
			Token:   true,
			Rstream: false,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	tunnelAuth, ok := body["tunnelAuth"].(map[string]any)
	if !ok {
		t.Fatal("expected tunnelAuth to be an object")
	}
	token, ok := tunnelAuth["token"].(bool)
	if !ok {
		t.Fatal("expected tunnelAuth.token to be a boolean")
	}
	if !token {
		t.Fatal("expected tunnelAuth.token to be true")
	}
	if _, ok := tunnelAuth["rstream"].(bool); !ok {
		t.Fatal("expected tunnelAuth.rstream to be a boolean")
	}
}

func TestSameOriginAllowsBrowserViewerOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://viewer.example/ws", nil)
	req.Host = "viewer.example"
	req.Header.Set("Origin", "https://viewer.example")
	if !sameOrigin(req) {
		t.Fatal("expected same-origin websocket upgrade to be allowed")
	}
}

func TestSameOriginRejectsCrossOriginViewerOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://viewer.example/ws", nil)
	req.Host = "viewer.example"
	req.Header.Set("Origin", "https://evil.example")
	if sameOrigin(req) {
		t.Fatal("expected cross-origin websocket upgrade to be rejected")
	}
}

func TestProvisioningModeAllowsProductViewerOrigin(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		hub,
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("not implemented")
		},
		func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error) {
			return nil, errors.New("not implemented")
		},
		ServerOptions{Viewer: false},
	)
	req := httptest.NewRequest(http.MethodGet, "https://device-tunnel.example/ws", nil)
	req.Host = "device-tunnel.example"
	req.Header.Set("Origin", "https://platform.example")
	if !server.upgrader.CheckOrigin(req) {
		t.Fatal("expected product viewer origin to be allowed in provisioning mode")
	}
	req.Header.Set("Origin", "not a URL")
	if server.upgrader.CheckOrigin(req) {
		t.Fatal("expected invalid Origin header to be rejected")
	}
}

func TestSameOriginAllowsMissingOriginAndRejectsInvalidOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://viewer.example/ws", nil)
	req.Host = "viewer.example"
	if !sameOrigin(req) {
		t.Fatal("expected non-browser clients without Origin to be allowed")
	}
	req.Header.Set("Origin", "://bad")
	if sameOrigin(req) {
		t.Fatal("expected invalid Origin header to be rejected")
	}
}
