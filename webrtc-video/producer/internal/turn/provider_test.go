package turn

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestCloneCredentialsCopiesURLs(t *testing.T) {
	original := &rstream.TURNCredentials{
		URLs:       []string{"turn:one.example.com"},
		Username:   "viewer",
		Credential: "secret",
		TTL:        3600,
	}
	clone := cloneCredentials(original)
	clone.URLs[0] = "turn:mutated.example.com"
	if original.URLs[0] != "turn:one.example.com" {
		t.Fatalf("expected the cloned URL slice to be independent, got %q", original.URLs[0])
	}
}

func TestFilterCredentialsSelectsExplicitTransports(t *testing.T) {
	original := &rstream.TURNCredentials{
		URLs: []string{
			"turn:turn.example.com:3478?transport=udp",
			"turn:turn.example.com:3478?transport=tcp",
			"turns:turn.example.com:5349?transport=udp",
			"turns:turn.example.com:5349?transport=tcp",
		},
		Username:   "viewer",
		Credential: "secret",
	}
	filtered, err := filterCredentials(original, map[string]struct{}{
		"udp": {},
		"tls": {},
	})
	if err != nil {
		t.Fatalf("filter credentials: %v", err)
	}
	want := []string{
		"turn:turn.example.com:3478?transport=udp",
		"turns:turn.example.com:5349?transport=tcp",
	}
	if !reflect.DeepEqual(filtered.URLs, want) {
		t.Fatalf("filtered URLs = %v, want %v", filtered.URLs, want)
	}
	if len(original.URLs) != 4 {
		t.Fatal("filter changed the original credentials")
	}
}

func TestFilterCredentialsRejectsUnsupportedOrMissingTransports(t *testing.T) {
	tests := []struct {
		name string
		urls []string
	}{
		{name: "malformed", urls: []string{"https://turn.example.com"}},
		{name: "missing", urls: []string{"turn:turn.example.com:3478?transport=udp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := filterCredentials(&rstream.TURNCredentials{URLs: tc.urls}, map[string]struct{}{"tls": {}})
			if err == nil {
				t.Fatal("expected transport filter failure")
			}
		})
	}
}

func TestClassifyTURNTransport(t *testing.T) {
	tests := map[string]string{
		"turn:turn.example.com:3478?transport=udp":  "udp",
		"turn:turn.example.com:3478?transport=tcp":  "tcp",
		"turns:turn.example.com:5349?transport=udp": "dtls",
		"turns:turn.example.com:5349?transport=tcp": "tls",
	}
	for rawURL, want := range tests {
		got, err := classifyTURNTransport(rawURL)
		if err != nil {
			t.Fatalf("classify %q: %v", rawURL, err)
		}
		if got != want {
			t.Fatalf("classify %q = %q, want %q", rawURL, got, want)
		}
	}
}

func TestFilterCredentialsValidatesUnrestrictedURLs(t *testing.T) {
	if _, err := filterCredentials(&rstream.TURNCredentials{
		URLs: []string{"https://turn.example.com"},
	}, nil); err == nil {
		t.Fatal("expected an unsupported unrestricted TURN URL to fail")
	}
}

func TestFilterCredentialsRejectsMissingCredentials(t *testing.T) {
	if _, err := filterCredentials(nil, nil); err == nil {
		t.Fatal("expected missing TURN credentials to fail")
	}
}

func TestClassifyTURNTransportRejectsMissingEndpoint(t *testing.T) {
	if _, err := classifyTURNTransport("turn:?transport=udp"); err == nil {
		t.Fatal("expected an empty TURN endpoint to fail")
	}
}

func TestURLsByTransportIndexesEveryFallback(t *testing.T) {
	credentials := &rstream.TURNCredentials{URLs: []string{
		"turn:turn.example.com:3478?transport=udp",
		"turn:turn.example.com:3478?transport=tcp",
		"turns:turn.example.com:5349?transport=udp",
		"turns:turn.example.com:5349?transport=tcp",
	}}
	urls, err := URLsByTransport(credentials)
	if err != nil {
		t.Fatalf("index TURN URLs: %v", err)
	}
	if len(urls) != 4 {
		t.Fatalf("indexed transports = %d, want 4", len(urls))
	}
	if got := urls["dtls"]; got != credentials.URLs[2] {
		t.Fatalf("DTLS URL = %q, want %q", got, credentials.URLs[2])
	}
	if got := urls["tls"]; got != credentials.URLs[3] {
		t.Fatalf("TLS URL = %q, want %q", got, credentials.URLs[3])
	}
}
