package source

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	defaultRequestTokenTTL = 20 * time.Second
	maximumRequestTokenTTL = 30 * time.Second
	requestClockSkew       = 5 * time.Second
	requestNonceBytes      = 16
)

type RequestAuthorizer interface {
	Authorization(string, ResolutionPurpose) (string, error)
}

type RequestSigner struct {
	audience string
	instance string
	issuer   string
	key      ed25519.PrivateKey
	keyID    string
	now      func() time.Time
	random   io.Reader
	ttl      time.Duration
}

type RequestSignerOptions struct {
	Now    func() time.Time
	Random io.Reader
	TTL    time.Duration
}

type resolverTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type resolverTokenClaims struct {
	Audience  string            `json:"aud"`
	ExpiresAt int64             `json:"exp"`
	IssuedAt  int64             `json:"iat"`
	Issuer    string            `json:"iss"`
	Nonce     string            `json:"jti"`
	NotBefore int64             `json:"nbf"`
	Path      string            `json:"path"`
	Purpose   ResolutionPurpose `json:"purpose"`
	Subject   string            `json:"sub"`
}

func NewRequestSigner(encodedPrivateKey, instance, issuer, audience string, options ...RequestSignerOptions) (*RequestSigner, error) {
	if len(options) > 1 {
		return nil, errors.New("resolver request signer accepts at most one options value")
	}
	key, keyID, err := parseRequestSigningKey(encodedPrivateKey)
	if err != nil {
		return nil, err
	}
	instance, err = validateTokenClaim(instance, "instance", 128)
	if err != nil {
		return nil, err
	}
	issuer, err = validateTokenClaim(issuer, "issuer", 512)
	if err != nil {
		return nil, err
	}
	audience, err = validateTokenClaim(audience, "audience", 512)
	if err != nil {
		return nil, err
	}
	now := time.Now
	random := io.Reader(rand.Reader)
	ttl := defaultRequestTokenTTL
	if len(options) == 1 {
		if options[0].Now != nil {
			now = options[0].Now
		}
		if options[0].Random != nil {
			random = options[0].Random
		}
		if options[0].TTL != 0 {
			ttl = options[0].TTL
		}
	}
	if ttl < time.Second || ttl > maximumRequestTokenTTL || ttl%time.Second != 0 {
		return nil, fmt.Errorf("resolver request token TTL must be a whole second between 1s and %s", maximumRequestTokenTTL)
	}
	return &RequestSigner{audience: audience, instance: instance, issuer: issuer, key: key, keyID: keyID, now: now, random: random, ttl: ttl}, nil
}

func (s *RequestSigner) Authorization(path string, purpose ResolutionPurpose) (string, error) {
	if s == nil {
		return "", errors.New("resolver request signer is required")
	}
	if err := validateResolutionPurpose(purpose); err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" || len(path) > maxEndpointBytes || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("resolver request path is invalid")
	}
	nonce := make([]byte, requestNonceBytes)
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return "", errors.New("generate resolver request nonce")
	}
	now := s.now().UTC().Truncate(time.Second)
	header, err := encodeResolverTokenPart(resolverTokenHeader{Algorithm: "EdDSA", KeyID: s.keyID, Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := encodeResolverTokenPart(resolverTokenClaims{
		Audience:  s.audience,
		ExpiresAt: now.Add(s.ttl).Unix(),
		IssuedAt:  now.Unix(),
		Issuer:    s.issuer,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		NotBefore: now.Add(-requestClockSkew).Unix(),
		Path:      path,
		Purpose:   purpose,
		Subject:   s.instance,
	})
	if err != nil {
		return "", err
	}
	input := header + "." + claims
	signature := ed25519.Sign(s.key, []byte(input))
	return "Bearer " + input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRequestSigningKey(encoded string) (ed25519.PrivateKey, string, error) {
	value := strings.TrimSpace(encoded)
	der, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || value == "" {
		return nil, "", errors.New("resolver request signing key is not valid base64")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, "", errors.New("resolver request signing key is not a PKCS#8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, "", errors.New("resolver request signing key must use Ed25519")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, "", errors.New("encode resolver request public key")
	}
	digest := sha256.Sum256(publicDER)
	return append(ed25519.PrivateKey(nil), key...), base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func validateTokenClaim(raw, name string, maximumBytes int) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maximumBytes || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", fmt.Errorf("resolver request token %s is invalid", name)
	}
	return value, nil
}

func encodeResolverTokenPart(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode resolver request token")
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
