package turn

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/provisioning"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

const (
	defaultRefreshTimeout = 10 * time.Second
	maxCredentialBytes    = 8 * 1024
	maxTURNURLBytes       = 4 * 1024
	maxTURNURLs           = 16
)

type Provider struct {
	fetch          func(context.Context) (*rstream.TURNCredentials, error)
	mu             sync.Mutex
	cached         *rstream.TURNCredentials
	refresh        *credentialRefresh
	refreshAt      time.Time
	validUntil     time.Time
	now            func() time.Time
	refreshTimeout time.Duration
	transports     map[string]struct{}
}

type credentialRefresh struct {
	done        chan struct{}
	credentials *rstream.TURNCredentials
	validUntil  time.Time
	err         error
}

func NewProvider(cfg config.Config, provisioningClient *provisioning.Client) (*Provider, error) {
	ttl, err := cfg.TURNTTL()
	if err != nil {
		return nil, err
	}
	transports, err := cfg.TURNTransports()
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(transports))
	for _, transport := range transports {
		allowed[transport] = struct{}{}
	}
	fetch := func(ctx context.Context) (*rstream.TURNCredentials, error) {
		return rsconfig.CreateTURNCredentialsFromEnv(ctx, rsconfig.TURNCredentialsEnvOptions{TTL: ttl})
	}
	if provisioningClient != nil {
		fetch = provisioningClient.TURN
	}
	refreshTimeout, err := cfg.TunnelProvisioningTimeout()
	if err != nil {
		return nil, err
	}
	return &Provider{fetch: fetch, transports: allowed, now: time.Now, refreshTimeout: refreshTimeout}, nil
}

func (p *Provider) Credentials(ctx context.Context) (*rstream.TURNCredentials, error) {
	p.mu.Lock()
	now := p.now()
	if p.cached != nil && now.Before(p.refreshAt) {
		credentials := cloneCredentialsAt(p.cached, p.validUntil, now)
		p.mu.Unlock()
		return credentials, nil
	}
	refresh := p.refresh
	if refresh == nil {
		refresh = &credentialRefresh{done: make(chan struct{})}
		p.refresh = refresh
		go p.refreshCredentials(refresh, context.WithoutCancel(ctx))
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for TURN credential refresh: %w", ctx.Err())
	case <-refresh.done:
		if refresh.err != nil {
			return nil, refresh.err
		}
		return cloneCredentialsAt(refresh.credentials, refresh.validUntil, p.now()), nil
	}
}

func (p *Provider) refreshCredentials(refresh *credentialRefresh, parent context.Context) {
	now := p.now()
	timeout := p.refreshTimeout
	if timeout <= 0 {
		timeout = defaultRefreshTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	credentials, err := p.fetch(ctx)
	if err != nil {
		err = fmt.Errorf("create TURN credentials: %w", err)
	}
	if err == nil {
		credentials, err = filterCredentials(credentials, p.transports)
	}
	validUntil := turnValidUntil(credentials, now)
	if err == nil && !validUntil.After(p.now()) {
		err = errors.New("TURN credentials expired before the refresh completed")
	}
	p.mu.Lock()
	if err == nil {
		p.cached = credentials
		p.refreshAt = credentialsRefreshAt(credentials, now)
		p.validUntil = validUntil
		refresh.credentials = credentials
		refresh.validUntil = validUntil
	}
	refresh.err = err
	p.refresh = nil
	close(refresh.done)
	p.mu.Unlock()
}

func filterCredentials(credentials *rstream.TURNCredentials, allowed map[string]struct{}) (*rstream.TURNCredentials, error) {
	filtered := cloneCredentials(credentials)
	if filtered == nil {
		return nil, errors.New("TURN credentials are missing")
	}
	if filtered.TTL <= 0 || strings.TrimSpace(filtered.Username) == "" || strings.TrimSpace(filtered.Credential) == "" || len(filtered.Username) > maxCredentialBytes || len(filtered.Credential) > maxCredentialBytes || strings.ContainsAny(filtered.Username, "\r\n\x00") || strings.ContainsAny(filtered.Credential, "\r\n\x00") {
		return nil, errors.New("TURN credentials are invalid")
	}
	if len(credentials.URLs) == 0 || len(credentials.URLs) > maxTURNURLs {
		return nil, errors.New("TURN credential URLs are missing or exceed the limit")
	}
	filtered.URLs = filtered.URLs[:0]
	for _, rawURL := range credentials.URLs {
		if len(rawURL) > maxTURNURLBytes || strings.ContainsAny(rawURL, "\r\n\x00") {
			return nil, errors.New("TURN credential URL is invalid")
		}
		transport, err := classifyTURNTransport(rawURL)
		if err != nil {
			return nil, err
		}
		_, restricted := allowed[transport]
		if len(allowed) == 0 || restricted {
			filtered.URLs = append(filtered.URLs, rawURL)
		}
	}
	if len(filtered.URLs) == 0 {
		return nil, errors.New("TURN credentials did not contain an allowed transport")
	}
	return filtered, nil
}

// URLsByTransport indexes validated credential URLs by their physical client
// transport. The returned URLs never contain the username or credential.
func URLsByTransport(credentials *rstream.TURNCredentials) (map[string]string, error) {
	urls := make(map[string]string)
	if credentials == nil {
		return urls, nil
	}
	for _, rawURL := range credentials.URLs {
		transport, err := classifyTURNTransport(rawURL)
		if err != nil {
			return nil, err
		}
		if _, exists := urls[transport]; !exists {
			urls[transport] = rawURL
		}
	}
	return urls, nil
}

func classifyTURNTransport(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse TURN URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if strings.TrimSpace(parsed.Opaque) == "" && strings.TrimSpace(parsed.Hostname()) == "" {
		return "", fmt.Errorf("unsupported TURN URL %q", rawURL)
	}
	transport := strings.ToLower(parsed.Query().Get("transport"))
	switch {
	case scheme == "turn" && transport == "udp":
		return "udp", nil
	case scheme == "turn" && transport == "tcp":
		return "tcp", nil
	case scheme == "turns" && transport == "udp":
		return "dtls", nil
	case scheme == "turns" && transport == "tcp":
		return "tls", nil
	default:
		return "", fmt.Errorf("unsupported TURN URL %q", rawURL)
	}
}

func ICEConfig(credentials *rstream.TURNCredentials) webrtc.Configuration {
	if credentials == nil {
		return webrtc.Configuration{}
	}
	// Map the SDK TURN response to the WebRTC ICE server format.
	return webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs:       append([]string(nil), credentials.URLs...),
				Username:   credentials.Username,
				Credential: credentials.Credential,
			},
		},
	}
}

func cloneCredentials(credentials *rstream.TURNCredentials) *rstream.TURNCredentials {
	if credentials == nil {
		return nil
	}
	out := *credentials
	out.URLs = append([]string(nil), credentials.URLs...)
	return &out
}

func cloneCredentialsAt(credentials *rstream.TURNCredentials, validUntil time.Time, now time.Time) *rstream.TURNCredentials {
	out := cloneCredentials(credentials)
	if out == nil {
		return nil
	}
	remaining := validUntil.Sub(now)
	if remaining <= 0 {
		out.TTL = 0
		return out
	}
	out.TTL = int((remaining + time.Second - 1) / time.Second)
	return out
}

func turnValidUntil(credentials *rstream.TURNCredentials, now time.Time) time.Time {
	if credentials == nil || credentials.TTL <= 0 {
		return now
	}
	return now.Add(time.Duration(credentials.TTL) * time.Second)
}

func credentialsRefreshAt(credentials *rstream.TURNCredentials, now time.Time) time.Time {
	if credentials == nil || credentials.TTL <= 0 {
		return now
	}
	lifetime := time.Duration(credentials.TTL) * time.Second
	lead := 5 * time.Minute
	if half := lifetime / 2; half < lead {
		lead = half
	}
	return now.Add(lifetime - lead)
}
