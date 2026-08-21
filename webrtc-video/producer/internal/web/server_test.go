package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-go"
)

func TestStatusReturnsLowercaseTunnelAuthFields(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("not implemented")
		},
		func(context.Context) (Session, error) {
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

func TestViewerFaviconDoesNotCreateABrowserConsoleError(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("not implemented")
		},
		func(context.Context) (Session, error) {
			return nil, errors.New("not implemented")
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if cacheControl := res.Header().Get("Cache-Control"); cacheControl == "" {
		t.Fatal("favicon response has no cache policy")
	}
}

func TestAPITURNFailureKeepsBackendDetailsInLogs(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		func(context.Context) (*rstream.TURNCredentials, error) {
			return nil, errors.New("credential backend unavailable")
		},
		func(context.Context) (Session, error) {
			return nil, errors.New("not implemented")
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/turn", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("TURN status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	responseBody := res.Body.Bytes()
	if bytes.Contains(responseBody, []byte("credential backend unavailable")) {
		t.Fatal("TURN response exposed the backend failure")
	}
	var body map[string]string
	if err := json.Unmarshal(responseBody, &body); err != nil {
		t.Fatalf("decode TURN error response: %v", err)
	}
	if body["error"] != "failed to issue TURN credentials" {
		t.Fatalf("TURN error = %q, want stable public failure", body["error"])
	}
	entries := hub.Recent()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if entries[0].Level != "error" {
		t.Fatalf("log level = %q, want error", entries[0].Level)
	}
	if entries[0].Message != "TURN credential request failed: credential backend unavailable" {
		t.Fatalf("log message = %q", entries[0].Message)
	}
}

func TestAPITURNIncludesAnAbsoluteCredentialDeadline(t *testing.T) {
	before := time.Now()
	server := NewServer(
		logs.NewLogger(logs.NewHub(16), false),
		func(context.Context) (*rstream.TURNCredentials, error) {
			return &rstream.TURNCredentials{URLs: []string{"turn:relay.example:3478?transport=udp"}, Username: "viewer", Credential: "secret", TTL: 600}, nil
		},
		func(context.Context) (Session, error) { return nil, errors.New("not implemented") },
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/turn", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("TURN status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Username  string    `json:"username"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode TURN response: %v", err)
	}
	if payload.Username != "viewer" || payload.ExpiresAt.Before(before.Add(599*time.Second)) || payload.ExpiresAt.After(time.Now().Add(601*time.Second)) {
		t.Fatalf("TURN response = username %q expiration %s", payload.Username, payload.ExpiresAt)
	}
}

func TestSameOriginAllowsBrowserViewerOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://viewer.example/ws", nil)
	req.Host = "viewer.example"
	req.Header.Set("Origin", "https://viewer.example")
	if !sameOrigin(req) {
		t.Fatal("expected same-origin browser request to be allowed")
	}
	req.Header.Set("Origin", "https://viewer.example:443")
	if !sameOrigin(req) {
		t.Fatal("expected same-origin browser request with default HTTPS port to be allowed")
	}
}

func TestSameOriginRejectsCrossOriginViewerOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://viewer.example/ws", nil)
	req.Host = "viewer.example"
	req.Header.Set("Origin", "https://evil.example")
	if sameOrigin(req) {
		t.Fatal("expected cross-origin browser request to be rejected")
	}
	req.Header.Set("Origin", "https://viewer.example:444")
	if sameOrigin(req) {
		t.Fatal("expected same host with non-default origin port to be rejected")
	}
}

func TestProvisioningModeAllowsProductViewerOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://device-tunnel.example/ws", nil)
	req.Host = "device-tunnel.example"
	req.Header.Set("Origin", "https://platform.example")
	if !browserOrigin(req) {
		t.Fatal("expected product viewer origin to be allowed in provisioning mode")
	}
	req.Header.Set("Origin", "not a URL")
	if browserOrigin(req) {
		t.Fatal("expected invalid Origin header to be rejected")
	}
}

func TestWHEPRoutesRemainAvailableWhenViewerUIIsDisabled(t *testing.T) {
	hub := logs.NewHub(16)
	server := NewServer(
		logs.NewLogger(hub, false),
		nil,
		func(context.Context) (Session, error) {
			return nil, errors.New("not implemented")
		},
		ServerOptions{Viewer: false},
	)
	req := httptest.NewRequest(http.MethodPost, "/whep", strings.NewReader("v=0\r\n"))
	req.Header.Set("Content-Type", "application/sdp")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("WHEP status = %d, want %d", res.Code, http.StatusInternalServerError)
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
