// See LICENSE file in the project root for license information.

package egress

import (
	"context"
	"net"
	"testing"
)

type fakeResolver map[string][]net.IPAddr

func (r fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return r[host], nil
}

func TestResolveTargetRejectsInvalidAuthority(t *testing.T) {
	policy := DefaultPolicy()
	for _, target := range []string{"", "example.com", "example.com:0", "example.com:99999", "https://example.com:443"} {
		if _, err := policy.ResolveTarget(t.Context(), "tcp", target); err == nil {
			t.Fatalf("ResolveTarget(%q) succeeded unexpectedly", target)
		}
	}
}

func TestResolveTargetDeniesPrivateAndLoopbackByDefault(t *testing.T) {
	policy := DefaultPolicy()
	for _, target := range []string{"127.0.0.1:443", "10.0.0.1:443", "[::1]:443", "[fd00::1]:443"} {
		if _, err := policy.ResolveTarget(t.Context(), "tcp", target); err == nil {
			t.Fatalf("ResolveTarget(%q) succeeded unexpectedly", target)
		}
	}
}

func TestResolveTargetAllowsPrivateWhenConfigured(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowPrivate = true
	got, err := policy.ResolveTarget(t.Context(), "tcp", "10.0.0.1:443")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Addr.String() != "10.0.0.1:443" {
		t.Fatalf("Addr = %s", got.Addr)
	}
}

func TestResolveTargetFiltersDNSAnswers(t *testing.T) {
	policy := DefaultPolicy()
	policy.Resolver = fakeResolver{
		"example.test": {
			{IP: net.ParseIP("10.0.0.1")},
			{IP: net.ParseIP("93.184.216.34")},
		},
	}
	got, err := policy.ResolveTarget(t.Context(), "tcp", "example.test:443")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Addr.String() != "93.184.216.34:443" {
		t.Fatalf("Addr = %s", got.Addr)
	}
}
