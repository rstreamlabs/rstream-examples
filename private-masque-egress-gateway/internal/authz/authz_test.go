// See LICENSE file in the project root for license information.

package authz

import (
	"net/http"
	"testing"
)

func TestCheckerAllowsWhenDisabled(t *testing.T) {
	checker := New("X-Test", "")
	req, err := http.NewRequest(http.MethodConnect, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !checker.Allow(req) {
		t.Fatal("disabled checker rejected request")
	}
}

func TestCheckerRequiresConfiguredHeader(t *testing.T) {
	checker := New("X-Test", "secret")
	req, err := http.NewRequest(http.MethodConnect, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if checker.Allow(req) {
		t.Fatal("checker allowed request without token")
	}
	req.Header.Set("X-Test", "wrong")
	if checker.Allow(req) {
		t.Fatal("checker allowed wrong token")
	}
	req.Header.Set("X-Test", "secret")
	if !checker.Allow(req) {
		t.Fatal("checker rejected correct token")
	}
}

func TestCheckerAcceptsBearerPrefix(t *testing.T) {
	checker := New("Proxy-Authorization", "secret")
	req, err := http.NewRequest(http.MethodConnect, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Proxy-Authorization", "Bearer secret")
	if !checker.Allow(req) {
		t.Fatal("checker rejected bearer token")
	}
}
