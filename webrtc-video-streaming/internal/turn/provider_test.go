package turn

import (
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
