package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-go"
)

type ownedTestTransport struct {
	conn   net.Conn
	closed atomic.Int32
	err    error
}

func (d *ownedTestTransport) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	return d.conn, nil
}

func (d *ownedTestTransport) Close() error {
	d.closed.Add(1)
	if d.conn != nil {
		_ = d.conn.Close()
	}
	return d.err
}

func TestTunnelSetupDeadlineClosesOwnedClient(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	transport := &ownedTestTransport{conn: conn}
	client, err := rstream.NewClient(rstream.ClientOptions{
		Engine: "engine.example.com:443", Token: "test-token",
		Transport: transport, OwnTransport: true,
		TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := make(chan error, 1)
	go func() {
		_, err := openClient(t.Context(), config.Default(), nil, OpenOptions{}, client, 25*time.Millisecond)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked tunnel setup = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		_ = peer.Close()
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("tunnel setup did not exit after peer closure")
		}
		t.Fatal("tunnel setup exceeded its deadline")
	}
	if got := transport.closed.Load(); got != 1 {
		t.Fatalf("failed setup closed its owned transport %d times, want 1", got)
	}
}

func TestManagerConcurrentCloseReleasesClientAndPreservesErrors(t *testing.T) {
	clientError := errors.New("client close failed")
	controlError := errors.New("control close failed")
	client := &ownedTestTransport{err: clientError}
	control := &ownedTestTransport{err: controlError}
	manager := &Manager{client: client, control: control}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			err := manager.Close()
			if !errors.Is(err, clientError) || !errors.Is(err, controlError) {
				t.Errorf("Close() lost an owned-resource error: %v", err)
			}
		})
	}
	workers.Wait()
	if client.closed.Load() != 1 || control.closed.Load() != 1 {
		t.Fatalf("owned close counts: client=%d control=%d", client.closed.Load(), control.closed.Load())
	}
}

func TestNewRstreamClientTransportModes(t *testing.T) {
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "")
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "")
	for _, test := range []struct {
		mode string
		want any
	}{
		{mode: "auto", want: &rstream.AutoTransport{}},
		{mode: "tls", want: &rstream.Transport{}},
		{mode: "quic", want: &rstream.QUICTransport{}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			client, err := newRstreamClient(OpenOptions{
				Engine: "edge.example.com:8443",
				Token:  "token",
			}, config.TunnelTransportConfig{Mode: test.mode})
			if err != nil {
				t.Fatalf("expected client creation to succeed, got %v", err)
			}
			if reflect.TypeOf(client.Transport) != reflect.TypeOf(test.want) {
				t.Fatalf("expected %T transport, got %T", test.want, client.Transport)
			}
		})
	}
}

func TestNewRstreamClientRejectsInvalidTransport(t *testing.T) {
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "")
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "")
	_, err := newRstreamClient(OpenOptions{
		Engine: "edge.example.com:8443",
		Token:  "token",
	}, config.TunnelTransportConfig{Mode: "sctp"})
	if err == nil {
		t.Fatal("expected invalid transport mode to fail")
	}
}

func TestTunnelTransportEnvironmentPrecedence(t *testing.T) {
	legacyQUIC := true
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "1")
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "tls")
	got := tunnelTransportMode(config.TunnelTransportConfig{Mode: "auto", UseQUIC: &legacyQUIC})
	if got != "tls" {
		t.Fatalf("tunnelTransportMode() = %q, want tls", got)
	}
}

func TestTunnelTransportLegacyEnvironment(t *testing.T) {
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "")
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "1")
	if got := tunnelTransportMode(config.TunnelTransportConfig{Mode: "auto"}); got != "quic" {
		t.Fatalf("tunnelTransportMode() = %q, want quic", got)
	}
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "0")
	if got := tunnelTransportMode(config.TunnelTransportConfig{Mode: "quic"}); got != "tls" {
		t.Fatalf("tunnelTransportMode() = %q, want tls", got)
	}
}
