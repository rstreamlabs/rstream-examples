package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPResolverAuthenticatesAndValidatesSource(t *testing.T) {
	const authorization = "Bearer resolver-secret"
	var calls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Authorization") != authorization {
			t.Errorf("Authorization header was not forwarded")
		}
		var payload resolveRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Path != "camera/signed-path" {
			t.Errorf("path = %q", payload.Path)
		}
		if payload.Purpose != ResolutionPurposeSignaling {
			t.Errorf("purpose = %q", payload.Purpose)
		}
		_ = json.NewEncoder(w).Encode(resolveResponse{
			URL:                      "https://device.example/whep",
			Authorization:            "Bearer source-secret",
			DestinationAuthorization: "Bearer publisher-token",
			ICEServers: []ICEServer{{
				URLs:       []string{"turns:turn.example:5349?transport=tcp"},
				Username:   "user",
				Credential: "credential",
				ExpiresAt:  time.Now().Add(2 * time.Minute),
			}},
			ExpiresAt: time.Now().Add(time.Minute),
		})
	}))
	defer server.Close()
	resolverURL := mustURL(t, server.URL)
	resolver, err := NewHTTPResolver(resolverURL, staticRequestAuthorizer(authorization), server.Client())
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	endpoint, err := resolver.Resolve(context.Background(), "camera/signed-path", ResolutionPurposeSignaling)
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	if endpoint.URL.String() != "https://device.example/whep" || endpoint.Authorization != "Bearer source-secret" || endpoint.DestinationAuthorization != "Bearer publisher-token" || len(endpoint.ICEServers) != 1 {
		t.Fatalf("unexpected endpoint response")
	}
	if endpoint.ICEExpiresAt.IsZero() || !endpoint.ICEExpiresAt.Equal(endpoint.ICEServers[0].ExpiresAt) {
		t.Fatalf("ICE credential expiration = %s, server = %s", endpoint.ICEExpiresAt, endpoint.ICEServers[0].ExpiresAt)
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls.Load())
	}
}

func TestHTTPResolverDoesNotExposeResponseOrCredentialsInErrors(t *testing.T) {
	const secret = "resolver-secret-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("upstream-body-that-must-not-leak"))
	}))
	defer server.Close()
	resolver, err := NewHTTPResolver(mustURL(t, server.URL), staticRequestAuthorizer("Bearer "+secret), server.Client())
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), "camera/signed-path", ResolutionPurposeSession)
	if err == nil {
		t.Fatal("resolver accepted unauthorized response")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "upstream-body") {
		t.Fatalf("resolver error exposed sensitive data: %v", err)
	}
	if !IsPermanent(err) {
		t.Fatal("resolver did not classify rejected authorization as permanent")
	}
}

func TestHTTPResolverClassifiesRetryableStatuses(t *testing.T) {
	tests := []struct {
		status    int
		permanent bool
	}{
		{status: http.StatusBadRequest, permanent: true},
		{status: http.StatusUnauthorized, permanent: true},
		{status: http.StatusForbidden, permanent: true},
		{status: http.StatusNotFound, permanent: true},
		{status: http.StatusRequestTimeout},
		{status: http.StatusConflict},
		{status: http.StatusTooEarly},
		{status: http.StatusTooManyRequests},
		{status: http.StatusInternalServerError},
		{status: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					Body:       io.NopCloser(strings.NewReader("response must remain private")),
					Header:     make(http.Header),
					StatusCode: test.status,
				}, nil
			})}
			resolver, err := NewHTTPResolver(mustURL(t, "https://resolver.example/source"), staticRequestAuthorizer("Bearer resolver"), client)
			if err != nil {
				t.Fatalf("new resolver: %v", err)
			}
			_, err = resolver.Resolve(context.Background(), "devices/00000000-0000-4000-8000-000000000000", ResolutionPurposeSession)
			if err == nil {
				t.Fatal("resolver accepted an error response")
			}
			if IsPermanent(err) != test.permanent {
				t.Fatalf("permanent = %t, want %t for status %d", IsPermanent(err), test.permanent, test.status)
			}
			if strings.Contains(err.Error(), "response must remain private") {
				t.Fatalf("resolver error exposed response body: %v", err)
			}
		})
	}
}

func TestHTTPResolverClassifiesContractAndTransportFailures(t *testing.T) {
	const resolverCredential = "resolver-credential-that-must-not-leak"
	transportFailure := errors.New("network unavailable")
	transportClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportFailure
	})}
	resolver, err := NewHTTPResolver(mustURL(t, "https://resolver.example/source?credential="+resolverCredential), staticRequestAuthorizer("Bearer resolver"), transportClient)
	if err != nil {
		t.Fatalf("new transport resolver: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), "devices/00000000-0000-4000-8000-000000000000", ResolutionPurposeSession)
	if !errors.Is(err, transportFailure) || IsPermanent(err) {
		t.Fatalf("transport error = %v, want retryable cause", err)
	}
	if strings.Contains(err.Error(), resolverCredential) {
		t.Fatalf("transport error exposed the resolver credential: %v", err)
	}
	contractClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Body:       io.NopCloser(strings.NewReader(`{"url":"https://device.example/whep","unknown":true}`)),
			Header:     make(http.Header),
			StatusCode: http.StatusOK,
		}, nil
	})}
	resolver, err = NewHTTPResolver(mustURL(t, "https://resolver.example/source"), staticRequestAuthorizer("Bearer resolver"), contractClient)
	if err != nil {
		t.Fatalf("new contract resolver: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), "devices/00000000-0000-4000-8000-000000000000", ResolutionPurposeSession)
	if err == nil || !IsPermanent(err) {
		t.Fatalf("contract error = %v, want permanent failure", err)
	}
}

func TestHTTPResolverIgnoresBodyCloseFailureAfterCompleteResponse(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(resolveResponse{
		URL:                      "https://device.example/whep",
		DestinationAuthorization: "Bearer destination",
		ExpiresAt:                now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Body:       closeErrorBody{Reader: bytes.NewReader(payload)},
			Header:     make(http.Header),
			StatusCode: http.StatusOK,
		}, nil
	})}
	resolver, err := NewHTTPResolver(mustURL(t, "https://resolver.example/source"), staticRequestAuthorizer("Bearer resolver"), client)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	resolver.now = func() time.Time { return now }
	if _, err := resolver.Resolve(context.Background(), "devices/00000000-0000-4000-8000-000000000000", ResolutionPurposeSession); err != nil {
		t.Fatalf("resolve complete response: %v", err)
	}
}

func TestHTTPResolverRejectsRedirectWithoutForwardingSignedIdentity(t *testing.T) {
	var targetRequests atomic.Uint32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	resolver, err := NewHTTPResolver(mustURL(t, redirect.URL), staticRequestAuthorizer("Bearer signed-request"), redirect.Client())
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), "camera/signed-path", ResolutionPurposeSession)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect error = %v, want rejected HTTP 307", err)
	}
	if requests := targetRequests.Load(); requests != 0 {
		t.Fatalf("redirect target requests = %d, want 0", requests)
	}
}

func TestHTTPResolverRejectsInvalidAndExpiredPayloads(t *testing.T) {
	tests := []resolveResponse{
		{URL: "http://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute)},
		{URL: "https://device.example/whep", Authorization: "Bearer token\x00injected", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute)},
		{URL: "https://device.example/whep?rstream.token=one&rstream.token=two", Authorization: "", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute)},
		{URL: "https://device.example/whep?rstream.token=", Authorization: "", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute)},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "", ExpiresAt: time.Now().Add(time.Minute)},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(-time.Second)},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute), ICEServers: []ICEServer{{URLs: []string{"https://not-ice.example"}}}},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute), ICEServers: []ICEServer{{URLs: []string{"turn:relay.example\r\nX-Injected:true"}}}},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute), ICEServers: []ICEServer{{URLs: []string{"turn:relay.example"}, Username: "viewer", Credential: "secret\x00suffix"}}},
		{URL: "https://device.example/whep", Authorization: "Bearer token", DestinationAuthorization: "Bearer publisher", ExpiresAt: time.Now().Add(time.Minute), ICEServers: []ICEServer{{URLs: []string{"turn:relay.example"}, Username: "viewer", Credential: "secret", ExpiresAt: time.Now().Add(-time.Second)}}},
	}
	for index, payload := range tests {
		if _, err := validateResponse(payload, time.Now(), 0); err == nil {
			t.Fatalf("case %d accepted invalid payload", index)
		}
	}
}

func TestHTTPResolverAcceptsSeparateEdgeAndApplicationCredentials(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	endpoint, err := validateResponse(resolveResponse{
		URL:                      "https://device.example/whep?rstream.token=edge-token",
		Authorization:            "",
		DestinationAuthorization: "Bearer publisher",
		ExpiresAt:                now.Add(time.Minute),
	}, now, 0)
	if err != nil {
		t.Fatalf("validate separate credentials: %v", err)
	}
	if endpoint.URL.Query().Get("rstream.token") != "edge-token" || endpoint.Authorization != "" {
		t.Fatalf("validated credentials = URL %q authorization %q", endpoint.URL, endpoint.Authorization)
	}
}

func TestHTTPResolverEnforcesMinimumCredentialLifetimeAtBoundary(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	minimum := time.Minute
	payload := resolveResponse{
		URL:                      "https://device.example/whep",
		Authorization:            "Bearer source",
		DestinationAuthorization: "Bearer destination",
		ExpiresAt:                now.Add(minimum - time.Nanosecond),
	}
	if _, err := validateResponse(payload, now, minimum); err == nil {
		t.Fatal("resolver accepted credentials below the minimum lifetime")
	}
	payload.ExpiresAt = now.Add(minimum)
	if _, err := validateResponse(payload, now, minimum); err != nil {
		t.Fatalf("resolver rejected credentials at the minimum lifetime: %v", err)
	}
	payload.ICEServers = []ICEServer{{URLs: []string{"turn:relay.example"}, Username: "viewer", Credential: "secret", ExpiresAt: now.Add(minimum - time.Nanosecond)}}
	if _, err := validateResponse(payload, now, minimum); err == nil {
		t.Fatal("resolver accepted ICE credentials below the minimum lifetime")
	}
	payload.ICEServers[0].ExpiresAt = now.Add(minimum)
	if endpoint, err := validateResponse(payload, now, minimum); err != nil {
		t.Fatalf("resolver rejected ICE credentials at the minimum lifetime: %v", err)
	} else if !endpoint.ICEExpiresAt.Equal(now.Add(minimum)) {
		t.Fatalf("ICE expiration = %s, want %s", endpoint.ICEExpiresAt, now.Add(minimum))
	}
}

func TestHTTPResolverRejectsInvalidMinimumLifetimeOptions(t *testing.T) {
	client := &http.Client{}
	endpoint := mustURL(t, "https://resolver.example/source")
	if _, err := NewHTTPResolver(endpoint, nil, client); err == nil {
		t.Fatal("resolver accepted a missing request authorizer")
	}
	if _, err := NewHTTPResolver(endpoint, staticRequestAuthorizer("Bearer resolver"), client, ResolverOptions{MinimumLifetime: -time.Second}); err == nil {
		t.Fatal("resolver accepted a negative minimum lifetime")
	}
	if _, err := NewHTTPResolver(endpoint, staticRequestAuthorizer("Bearer resolver"), client, ResolverOptions{}, ResolverOptions{}); err == nil {
		t.Fatal("resolver accepted multiple options values")
	}
}

func TestResolversRejectInvalidResolutionPurpose(t *testing.T) {
	var calls atomic.Uint32
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected request")
	})}
	resolver, err := NewHTTPResolver(mustURL(t, "https://resolver.example/source"), staticRequestAuthorizer("Bearer resolver"), client)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), "camera/signed-path", ResolutionPurpose("invalid")); err == nil {
		t.Fatal("HTTP resolver accepted an invalid purpose")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid purpose made %d HTTP requests", calls.Load())
	}
	if _, err := (StaticResolver{}).Resolve(context.Background(), "camera/signed-path", ResolutionPurpose("invalid")); err == nil {
		t.Fatal("static resolver accepted an invalid purpose")
	}
}

func TestHTTPResolverHonorsCancellationWithoutRetry(t *testing.T) {
	var calls atomic.Uint32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	resolver, err := NewHTTPResolver(mustURL(t, server.URL), staticRequestAuthorizer("Bearer resolver-secret"), server.Client())
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := resolver.Resolve(ctx, "camera/signed-path", ResolutionPurposeSession); err == nil {
		t.Fatal("resolver ignored cancellation")
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls.Load())
	}
}

func TestHTTPResolverRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), maxResolverBodyBytes+1))
	}))
	defer server.Close()
	resolver, err := NewHTTPResolver(mustURL(t, server.URL), staticRequestAuthorizer("Bearer resolver-secret"), server.Client())
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), "camera/signed-path", ResolutionPurposeSession)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("resolve oversized response error = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

type closeErrorBody struct {
	*bytes.Reader
}

func (closeErrorBody) Close() error {
	return errors.New("synthetic close failure")
}

type staticRequestAuthorizer string

func (a staticRequestAuthorizer) Authorization(_ string, _ ResolutionPurpose) (string, error) {
	return string(a), nil
}

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
