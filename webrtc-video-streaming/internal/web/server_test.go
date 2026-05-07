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
