package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalControlPreservesWHEPAndReadOnlyQuality(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.Host, "127.0.0.1:") {
			t.Errorf("upstream Host = %q", r.Host)
		}
		if r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("untrusted forwarding header reached upstream")
		}
		w.Header().Set("Location", "/whep/session")
		w.Header().Set("ETag", `"session-version"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "fixture")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := controlHandler(target, http.DefaultTransport)
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/whep", true},
		{"PATCH", "/whep/session", true},
		{"DELETE", "/whep/session", true},
		{"GET", "/api/quality", true},
		{"PUT", "/api/quality", false},
		{"POST", "/api/quality", false},
		{"GET", "/private", false},
		{"POST", "/whep-other", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			before := requests.Load()
			r := httptest.NewRequest(tc.method, "http://producer:18080"+tc.path, nil)
			r.Header.Set("X-Forwarded-Host", "untrusted.example")
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, r)
			if tc.allowed {
				if w.Code != http.StatusCreated || w.Header().Get("Location") != "/whep/session" || w.Header().Get("ETag") != `"session-version"` || w.Body.String() != "fixture" {
					t.Fatalf("forwarded response = %#v", w.Result())
				}
			} else if w.Code != http.StatusNotFound || requests.Load() != before {
				t.Fatal("forbidden method/path reached upstream")
			}
		})
	}
}
