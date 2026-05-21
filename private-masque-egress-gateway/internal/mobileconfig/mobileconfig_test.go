// See LICENSE file in the project root for license information.

package mobileconfig

import (
	"bytes"
	"testing"
)

func TestRelayURLsFromForwarding(t *testing.T) {
	h3, h2, err := RelayURLsFromForwarding("https://relay.example.com")
	if err != nil {
		t.Fatalf("RelayURLsFromForwarding: %v", err)
	}
	if h3 != "https://relay.example.com:443/.well-known/masque/udp/{target_host}/{target_port}/" {
		t.Fatalf("HTTP3 URL = %q", h3)
	}
	if h2 != "https://relay.example.com:443/" {
		t.Fatalf("HTTP2 URL = %q", h2)
	}
}

func TestRenderIncludesRelayAndHeaders(t *testing.T) {
	out, err := Render(Options{
		HTTP3RelayURL: "https://relay.example.com/.well-known/masque/udp/{target_host}/{target_port}/",
		HTTP2RelayURL: "https://relay.example.com/",
		HeaderName:    "X-Egress-Token",
		HeaderValue:   "secret&value",
		MatchDomains:  []string{"example.com", "amp.example"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte(`<?xml version="1.0" encoding="UTF-8"?>`)) {
		t.Fatalf("profile must start with a raw XML declaration:\n%s", out)
	}
	for _, want := range [][]byte{
		[]byte("<string>com.apple.relay.managed</string>"),
		[]byte("<key>HTTP3RelayURL</key>"),
		[]byte("<key>X-Egress-Token</key>"),
		[]byte("<string>secret&amp;value</string>"),
		[]byte("<string>example.com</string>"),
	} {
		if !bytes.Contains(out, want) {
			t.Fatalf("profile missing %q:\n%s", want, out)
		}
	}
}

func TestRenderGeneratesUUIDs(t *testing.T) {
	first, err := Render(Options{
		HTTP3RelayURL: "https://relay.example.com/.well-known/masque/udp/{target_host}/{target_port}/",
	})
	if err != nil {
		t.Fatalf("Render first profile: %v", err)
	}
	second, err := Render(Options{
		HTTP3RelayURL: "https://relay.example.com/.well-known/masque/udp/{target_host}/{target_port}/",
	})
	if err != nil {
		t.Fatalf("Render second profile: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("generated profiles should use fresh UUIDs")
	}
}
