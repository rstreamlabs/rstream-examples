package source

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestSignerBindsInstancePathPurposeAndDeadline(t *testing.T) {
	encoded, public := requestSigningKey(t, 0x42)
	now := time.Date(2026, 8, 18, 12, 34, 56, 789, time.UTC)
	signer, err := NewRequestSigner(encoded, "mediamtx-eu-1", "rstream-video-distributor", "rstream-video-source-resolver", RequestSignerOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0xa5}, requestNonceBytes)),
		TTL:    12 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := signer.Authorization("devices/9e1b1e39-6f98-47df-bf55-c2f05fab6739", ResolutionPurposeSignaling)
	if err != nil {
		t.Fatal(err)
	}
	const goldenAuthorization = "Bearer eyJhbGciOiJFZERTQSIsImtpZCI6Im1vSlJmNXJ4bEJiWmo5dlBHVGNtczZsY0MyX3NIVkdJU19QaHR6bTZMdlEiLCJ0eXAiOiJKV1QifQ.eyJhdWQiOiJyc3RyZWFtLXZpZGVvLXNvdXJjZS1yZXNvbHZlciIsImV4cCI6MTc4NzA1NjUwOCwiaWF0IjoxNzg3MDU2NDk2LCJpc3MiOiJyc3RyZWFtLXZpZGVvLWRpc3RyaWJ1dG9yIiwianRpIjoicGFXbHBhV2xwYVdscGFXbHBhV2xwUSIsIm5iZiI6MTc4NzA1NjQ5MSwicGF0aCI6ImRldmljZXMvOWUxYjFlMzktNmY5OC00N2RmLWJmNTUtYzJmMDVmYWI2NzM5IiwicHVycG9zZSI6InNpZ25hbGluZyIsInN1YiI6Im1lZGlhbXR4LWV1LTEifQ.qEeHD899vL56mEJ-H2vYuCbb37-3KssRvuf0mNVYcTss9BShbnMK9AH9CEzNUyOKb-ioA2TUCw9xd8MWJobhAA" // gitleaks:allow -- deterministic public test vector
	if authorization != goldenAuthorization {
		t.Fatalf("authorization changed:\n%s", authorization)
	}
	parts := strings.Split(strings.TrimPrefix(authorization, "Bearer "), ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	var header resolverTokenHeader
	decodeTokenPart(t, parts[0], &header)
	if header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID != signer.keyID {
		t.Fatalf("header = %+v", header)
	}
	var claims resolverTokenClaims
	decodeTokenPart(t, parts[1], &claims)
	if claims.Audience != "rstream-video-source-resolver" || claims.Issuer != "rstream-video-distributor" || claims.Subject != "mediamtx-eu-1" {
		t.Fatalf("identity claims = %+v", claims)
	}
	if claims.Path != "devices/9e1b1e39-6f98-47df-bf55-c2f05fab6739" || claims.Purpose != ResolutionPurposeSignaling {
		t.Fatalf("request claims = %+v", claims)
	}
	if claims.IssuedAt != now.Unix() || claims.NotBefore != now.Add(-requestClockSkew).Unix() || claims.ExpiresAt != now.Add(12*time.Second).Unix() {
		t.Fatalf("time claims = %+v", claims)
	}
	if claims.Nonce != base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, requestNonceBytes)) {
		t.Fatalf("nonce = %q", claims.Nonce)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("resolver request signature is invalid")
	}
}

func TestRequestSignerRejectsUnsafeConfigurationAndInputs(t *testing.T) {
	encoded, _ := requestSigningKey(t, 0x24)
	rsaDER, err := x509.MarshalPKCS8PrivateKey(testRSAKey(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		key        string
		instance   string
		issuer     string
		audience   string
		options    []RequestSignerOptions
		wantSubstr string
	}{
		{name: "invalid base64", key: "not base64", instance: "one", issuer: "issuer", audience: "audience", wantSubstr: "valid base64"},
		{name: "wrong key type", key: base64.StdEncoding.EncodeToString(rsaDER), instance: "one", issuer: "issuer", audience: "audience", wantSubstr: "Ed25519"},
		{name: "unsafe instance", key: encoded, instance: "one two", issuer: "issuer", audience: "audience", wantSubstr: "instance"},
		{name: "missing issuer", key: encoded, instance: "one", audience: "audience", wantSubstr: "issuer"},
		{name: "unsafe audience", key: encoded, instance: "one", issuer: "issuer", audience: "audience\nother", wantSubstr: "audience"},
		{name: "short TTL", key: encoded, instance: "one", issuer: "issuer", audience: "audience", options: []RequestSignerOptions{{TTL: time.Millisecond}}, wantSubstr: "TTL"},
		{name: "long TTL", key: encoded, instance: "one", issuer: "issuer", audience: "audience", options: []RequestSignerOptions{{TTL: maximumRequestTokenTTL + time.Second}}, wantSubstr: "TTL"},
		{name: "multiple options", key: encoded, instance: "one", issuer: "issuer", audience: "audience", options: []RequestSignerOptions{{}, {}}, wantSubstr: "at most one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRequestSigner(test.key, test.instance, test.issuer, test.audience, test.options...)
			if err == nil || !strings.Contains(err.Error(), test.wantSubstr) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	signer, err := NewRequestSigner(encoded, "one", "issuer", "audience", RequestSignerOptions{Random: errorReader{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		path    string
		purpose ResolutionPurpose
	}{
		{path: "", purpose: ResolutionPurposeSession},
		{path: "devices/one\nother", purpose: ResolutionPurposeSession},
		{path: "devices/one", purpose: ResolutionPurpose("unknown")},
	} {
		if _, err := signer.Authorization(input.path, input.purpose); err == nil {
			t.Fatalf("Authorization(%q, %q) succeeded", input.path, input.purpose)
		}
	}
	if _, err := signer.Authorization("devices/one", ResolutionPurposeSession); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("random failure = %v", err)
	}
}

func TestRequestSignerSupportsConcurrentOnDemandChildren(t *testing.T) {
	encoded, _ := requestSigningKey(t, 0x17)
	signer, err := NewRequestSigner(encoded, "mediamtx-one", "issuer", "audience")
	if err != nil {
		t.Fatal(err)
	}
	const workers = 64
	var wait sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			authorization, err := signer.Authorization("devices/one", ResolutionPurposeSession)
			if err != nil {
				errs <- err
				return
			}
			if !strings.HasPrefix(authorization, "Bearer ") {
				errs <- errors.New("authorization is not a bearer token")
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func BenchmarkRequestSignerAuthorization(b *testing.B) {
	encoded, _ := requestSigningKey(b, 0x31)
	signer, err := NewRequestSigner(encoded, "mediamtx-one", "issuer", "audience")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := signer.Authorization("devices/9e1b1e39-6f98-47df-bf55-c2f05fab6739", ResolutionPurposeSession); err != nil {
			b.Fatal(err)
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("unavailable")
}

func requestSigningKey(t testing.TB, value byte) (string, ed25519.PublicKey) {
	t.Helper()
	seed := bytes.Repeat([]byte{value}, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der), private.Public().(ed25519.PublicKey)
}

func decodeTokenPart(t *testing.T, raw string, target any) {
	t.Helper()
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(value, target); err != nil {
		t.Fatal(err)
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
