package tunnel

import (
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestNewRstreamClientUsesQUICTransport(t *testing.T) {
	client, err := newRstreamClient(OpenOptions{
		Engine: "edge.example.com:8443",
		Token:  "token",
	}, true)
	if err != nil {
		t.Fatalf("expected client creation to succeed, got %v", err)
	}
	if _, ok := client.Transport.(*rstream.QUICTransport); !ok {
		t.Fatalf("expected QUIC transport, got %T", client.Transport)
	}
}
