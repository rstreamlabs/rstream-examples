package provisioning

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestClientTunnelUsesBoundedAuthenticatedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/api/devices/tunnel" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer device-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if !bytes.Contains(body, []byte(`"agent":"`+userAgent+`"`)) {
			t.Errorf("request body = %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"device":"device-1","engine":"https://engine.example","token":"token","name":"camera"}`))
	}))
	defer server.Close()
	client := testClient(t, server.URL+"/control", "device-secret", server.Client())
	tunnel, err := client.Tunnel(context.Background())
	if err != nil {
		t.Fatalf("provision tunnel: %v", err)
	}
	if tunnel.Device != "device-1" || tunnel.Name != "camera" {
		t.Fatalf("unexpected tunnel: %+v", tunnel)
	}
}

func TestClientRejectsOversizedProvisioningResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(bytes.Repeat([]byte("x"), maxProvisioningResponseSize+1))
	}))
	defer server.Close()
	client := testClient(t, server.URL, "device-secret", server.Client())
	_, err := client.Tunnel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestClientDoesNotExposeProvisioningResponseBody(t *testing.T) {
	const sensitiveBody = "upstream-secret-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(sensitiveBody))
	}))
	defer server.Close()
	client := testClient(t, server.URL, "device-secret", server.Client())
	_, err := client.Tunnel(context.Background())
	if err == nil {
		t.Fatal("failed provisioning response was accepted")
	}
	if strings.Contains(err.Error(), sensitiveBody) {
		t.Fatalf("provisioning error exposed response body: %v", err)
	}
}

func TestClientRejectsProvisioningRedirectWithoutForwardingSecret(t *testing.T) {
	var targetRequests atomic.Uint32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	cfg := config.Default()
	cfg.Tunnel.Provisioning.Mode = config.TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = redirect.URL
	cfg.Tunnel.Provisioning.Secret = "device-secret"
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("create provisioning client: %v", err)
	}
	_, err = client.Tunnel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect error = %v, want rejected HTTP 307", err)
	}
	if requests := targetRequests.Load(); requests != 0 {
		t.Fatalf("redirect target requests = %d, want 0", requests)
	}
}

func TestNewClientUsesFreshBoundedConnectionsAcrossNetworkChanges(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Provisioning.Mode = config.TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = "https://platform.example"
	cfg.Tunnel.Provisioning.Secret = "device-secret"
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("create provisioning client: %v", err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("provisioning transport = %T, want *http.Transport", client.http.Transport)
	}
	if !transport.DisableKeepAlives {
		t.Fatal("provisioning transport can retain a connection bound to an obsolete interface")
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("provisioning transport cannot safely negotiate an advertised HTTP/2 protocol")
	}
	if transport.DialContext == nil {
		t.Fatal("provisioning transport omitted its bounded network dialer")
	}
}

func TestClientNegotiatesAnAdvertisedHTTP2Protocol(t *testing.T) {
	var protocol atomic.Int32
	var connections sync.Map
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		protocol.Store(int32(request.ProtoMajor))
		connections.Store(request.RemoteAddr, struct{}{})
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"device":"device-1","engine":"https://engine.example","token":"token","name":"camera"}`))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	cfg := config.Default()
	cfg.Tunnel.Provisioning.Mode = config.TunnelProvisioningModeRemote
	cfg.Tunnel.Provisioning.Endpoint = server.URL
	cfg.Tunnel.Provisioning.Secret = "device-secret"
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("create provisioning client: %v", err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("provisioning transport = %T, want *http.Transport", client.http.Transport)
	}
	serverTransport, ok := server.Client().Transport.(*http.Transport)
	if !ok || serverTransport.TLSClientConfig == nil {
		t.Fatal("HTTP/2 test server did not expose its client TLS configuration")
	}
	transport.TLSClientConfig = serverTransport.TLSClientConfig.Clone()
	transport.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	for range 2 {
		if _, err := client.Tunnel(t.Context()); err != nil {
			t.Fatalf("provision tunnel over HTTP/2: %v", err)
		}
	}
	if got := protocol.Load(); got != 2 {
		t.Fatalf("provisioning protocol = HTTP/%d, want HTTP/2", got)
	}
	connectionCount := 0
	connections.Range(func(any, any) bool {
		connectionCount++
		return true
	})
	if connectionCount != 2 {
		t.Fatalf("provisioning connections = %d, want one bounded connection per request", connectionCount)
	}
}

func TestClientEvictsTransportStateAfterNetworkFailure(t *testing.T) {
	transport := &closeIdleTransport{err: errors.New("obsolete network path")}
	client := testClient(t, "https://platform.example", "device-secret", &http.Client{Transport: transport})
	_, err := client.TURN(context.Background())
	if !errors.Is(err, transport.err) {
		t.Fatalf("TURN error = %v, want %v", err, transport.err)
	}
	if closes := transport.closes.Load(); closes != 1 {
		t.Fatalf("idle connection evictions = %d, want 1", closes)
	}
}

func TestClientPropagatesResponseCloseFailure(t *testing.T) {
	closeErr := errors.New("response close failed")
	client := testClient(t, "https://platform.example", "device-secret", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: &closeErrorBody{
					Reader: strings.NewReader(`{"device":"device-1","engine":"https://engine.example","token":"token","name":"camera"}`),
					err:    closeErr,
				},
				Header: make(http.Header),
			}, nil
		}),
	})
	_, err := client.Tunnel(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("response close error = %v, want %v", err, closeErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type closeIdleTransport struct {
	err    error
	closes atomic.Uint32
}

func (transport *closeIdleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func (transport *closeIdleTransport) CloseIdleConnections() {
	transport.closes.Add(1)
}

type closeErrorBody struct {
	io.Reader
	err error
}

func (body *closeErrorBody) Close() error {
	return body.err
}

func testClient(t *testing.T, endpoint, secret string, httpClient *http.Client) *Client {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	return &Client{endpoint: parsed, secret: secret, http: httpClient}
}
