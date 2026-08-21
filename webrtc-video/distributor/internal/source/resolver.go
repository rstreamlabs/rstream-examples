package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxAuthorizationBytes = 8 * 1024
	maxEndpointBytes      = 4 * 1024
	maxICEServers         = 16
	maxResolverBodyBytes  = 64 * 1024
)

type ICEServer struct {
	URLs       []string  `json:"urls"`
	Username   string    `json:"username"`
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type Endpoint struct {
	URL                      *url.URL
	Authorization            string
	DestinationAuthorization string
	ICEServers               []ICEServer
	ICEExpiresAt             time.Time
	ExpiresAt                time.Time
}

type ResolutionPurpose string

const (
	ResolutionPurposeSession   ResolutionPurpose = "session"
	ResolutionPurposeSignaling ResolutionPurpose = "signaling"
)

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func Permanent(err error) error {
	if err == nil || IsPermanent(err) {
		return err
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

type Resolver interface {
	Resolve(context.Context, string, ResolutionPurpose) (Endpoint, error)
}

type StaticResolver struct {
	Endpoint Endpoint
}

func (r StaticResolver) Resolve(_ context.Context, _ string, purpose ResolutionPurpose) (Endpoint, error) {
	if err := validateResolutionPurpose(purpose); err != nil {
		return Endpoint{}, Permanent(err)
	}
	return r.Endpoint, nil
}

type HTTPResolver struct {
	authorizer      RequestAuthorizer
	endpoint        *url.URL
	client          *http.Client
	minimumLifetime time.Duration
	now             func() time.Time
}

type ResolverOptions struct {
	MinimumLifetime time.Duration
}

type resolveRequest struct {
	Path    string            `json:"path"`
	Purpose ResolutionPurpose `json:"purpose"`
}

type resolveResponse struct {
	URL                      string      `json:"url"`
	Authorization            string      `json:"authorization"`
	DestinationAuthorization string      `json:"destinationAuthorization"`
	ICEServers               []ICEServer `json:"iceServers"`
	ExpiresAt                time.Time   `json:"expiresAt"`
}

func NewHTTPResolver(endpoint *url.URL, authorizer RequestAuthorizer, client *http.Client, options ...ResolverOptions) (*HTTPResolver, error) {
	if endpoint == nil || endpoint.Host == "" {
		return nil, errors.New("resolver endpoint is required")
	}
	if authorizer == nil {
		return nil, errors.New("resolver request authorizer is required")
	}
	if client == nil {
		return nil, errors.New("resolver HTTP client is required")
	}
	if len(options) > 1 {
		return nil, errors.New("resolver accepts at most one options value")
	}
	minimumLifetime := time.Duration(0)
	if len(options) == 1 {
		minimumLifetime = options[0].MinimumLifetime
	}
	if minimumLifetime < 0 {
		return nil, errors.New("resolver minimum lifetime must not be negative")
	}
	safeClient := *client
	safeClient.CheckRedirect = rejectResolverRedirect
	return &HTTPResolver{endpoint: endpoint, authorizer: authorizer, client: &safeClient, minimumLifetime: minimumLifetime, now: time.Now}, nil
}

func rejectResolverRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (r *HTTPResolver) Resolve(ctx context.Context, path string, purpose ResolutionPurpose) (Endpoint, error) {
	if err := validateResolutionPurpose(purpose); err != nil {
		return Endpoint{}, Permanent(err)
	}
	body, err := json.Marshal(resolveRequest{Path: path, Purpose: purpose})
	if err != nil {
		return Endpoint{}, Permanent(fmt.Errorf("encode source request: %w", err))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return Endpoint{}, Permanent(fmt.Errorf("create source request: %w", err))
	}
	authorization, err := r.authorizer.Authorization(path, purpose)
	if err != nil {
		return Endpoint{}, Permanent(fmt.Errorf("authorize source request: %w", err))
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return Endpoint{}, fmt.Errorf("resolve source: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResolverBodyBytes))
		err := fmt.Errorf("resolve source: unexpected HTTP status %d", response.StatusCode)
		if retryableResolverStatus(response.StatusCode) {
			return Endpoint{}, err
		}
		return Endpoint{}, Permanent(err)
	}
	body, err = io.ReadAll(io.LimitReader(response.Body, maxResolverBodyBytes+1))
	if err != nil {
		return Endpoint{}, fmt.Errorf("read source response: %w", err)
	}
	if len(body) > maxResolverBodyBytes {
		return Endpoint{}, Permanent(fmt.Errorf("source response exceeds %d bytes", maxResolverBodyBytes))
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload resolveResponse
	if err := decoder.Decode(&payload); err != nil {
		return Endpoint{}, Permanent(fmt.Errorf("decode source response: %w", err))
	}
	if err := requireResponseEOF(decoder); err != nil {
		return Endpoint{}, Permanent(err)
	}
	endpoint, err := validateResponse(payload, r.now(), r.minimumLifetime)
	if err != nil {
		return Endpoint{}, Permanent(err)
	}
	return endpoint, nil
}

func retryableResolverStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func validateResolutionPurpose(purpose ResolutionPurpose) error {
	switch purpose {
	case ResolutionPurposeSession, ResolutionPurposeSignaling:
		return nil
	default:
		return fmt.Errorf("invalid source resolution purpose %q", purpose)
	}
}

func validateResponse(payload resolveResponse, now time.Time, minimumLifetime time.Duration) (Endpoint, error) {
	if len(payload.URL) > maxEndpointBytes {
		return Endpoint{}, errors.New("source endpoint is too long")
	}
	endpoint, err := url.Parse(strings.TrimSpace(payload.URL))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return Endpoint{}, errors.New("source endpoint is invalid")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !isLoopback(endpoint.Hostname())) {
		return Endpoint{}, errors.New("source endpoint must use HTTPS or loopback HTTP")
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil {
		return Endpoint{}, errors.New("source endpoint query is invalid")
	}
	tokens, present := query["rstream.token"]
	if present && (len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "") {
		return Endpoint{}, errors.New("source edge credential is invalid")
	}
	authorization := strings.TrimSpace(payload.Authorization)
	if len(authorization) > maxAuthorizationBytes || strings.ContainsAny(authorization, "\r\n\x00") {
		return Endpoint{}, errors.New("source authorization is invalid")
	}
	destinationAuthorization := strings.TrimSpace(payload.DestinationAuthorization)
	if destinationAuthorization == "" || len(destinationAuthorization) > maxAuthorizationBytes || strings.ContainsAny(destinationAuthorization, "\r\n\x00") {
		return Endpoint{}, errors.New("destination authorization is invalid")
	}
	if payload.ExpiresAt.IsZero() || payload.ExpiresAt.Before(now.Add(minimumLifetime)) {
		return Endpoint{}, errors.New("source authorization is expired")
	}
	if len(payload.ICEServers) > maxICEServers {
		return Endpoint{}, fmt.Errorf("source response exceeds %d ICE servers", maxICEServers)
	}
	iceServers := make([]ICEServer, len(payload.ICEServers))
	iceExpiresAt := time.Time{}
	for index, server := range payload.ICEServers {
		validated, err := validateICEServer(server, now, minimumLifetime)
		if err != nil {
			return Endpoint{}, fmt.Errorf("ICE server %d: %w", index, err)
		}
		iceServers[index] = validated
		if iceExpiresAt.IsZero() || validated.ExpiresAt.Before(iceExpiresAt) {
			iceExpiresAt = validated.ExpiresAt
		}
	}
	return Endpoint{
		URL:                      endpoint,
		Authorization:            authorization,
		DestinationAuthorization: destinationAuthorization,
		ICEServers:               iceServers,
		ICEExpiresAt:             iceExpiresAt,
		ExpiresAt:                payload.ExpiresAt,
	}, nil
}

func validateICEServer(server ICEServer, now time.Time, minimumLifetime time.Duration) (ICEServer, error) {
	if len(server.URLs) == 0 || len(server.URLs) > 16 {
		return ICEServer{}, errors.New("URLs are missing or exceed the limit")
	}
	urls := make([]string, len(server.URLs))
	for index, raw := range server.URLs {
		value := strings.TrimSpace(raw)
		if len(value) == 0 || len(value) > maxEndpointBytes || hasControl(value) || (!strings.HasPrefix(value, "stun:") && !strings.HasPrefix(value, "turn:") && !strings.HasPrefix(value, "turns:")) {
			return ICEServer{}, errors.New("URL is invalid")
		}
		urls[index] = value
	}
	if len(server.Username) > maxAuthorizationBytes || len(server.Credential) > maxAuthorizationBytes || hasControl(server.Username) || hasControl(server.Credential) {
		return ICEServer{}, errors.New("credentials are invalid")
	}
	if server.ExpiresAt.IsZero() || server.ExpiresAt.Before(now.Add(minimumLifetime)) {
		return ICEServer{}, errors.New("credentials are expired")
	}
	return ICEServer{URLs: urls, Username: server.Username, Credential: server.Credential, ExpiresAt: server.ExpiresAt}, nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) >= 0
}

func requireResponseEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode source response: %w", err)
	}
	return errors.New("source response contains more than one JSON value")
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
