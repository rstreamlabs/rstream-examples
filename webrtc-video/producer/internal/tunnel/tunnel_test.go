package tunnel

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-go"
)

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
