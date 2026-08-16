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

type Provider struct {
	provisioning *provisioning.Client
	options      rsconfig.TURNCredentialsEnvOptions
	mu           sync.Mutex
	cached       *rstream.TURNCredentials
	expires      time.Time
	transports   map[string]struct{}
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
	return &Provider{
		provisioning: provisioningClient,
		options: rsconfig.TURNCredentialsEnvOptions{
			TTL: ttl,
		},
		transports: allowed,
	}, nil
}

func (p *Provider) Credentials(ctx context.Context) (*rstream.TURNCredentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if p.cached != nil && now.Before(p.expires) {
		return cloneCredentials(p.cached), nil
	}
	if p.provisioning != nil {
		// Provisioned devices ask the platform for TURN credentials over HTTP.
		credentials, err := p.provisioning.TURN(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to provision TURN credentials: %w", err)
		}
		p.cached, err = filterCredentials(credentials, p.transports)
		if err != nil {
			return nil, err
		}
		p.expires = provisioning.TURNExpires(credentials, now)
		return cloneCredentials(p.cached), nil
	}
	// Local demos can mint rstream TURN credentials directly from SDK env config.
	credentials, err := rsconfig.CreateTURNCredentialsFromEnv(ctx, p.options)
	if err != nil {
		return nil, fmt.Errorf("failed to create TURN credentials: %w", err)
	}
	p.cached, err = filterCredentials(credentials, p.transports)
	if err != nil {
		return nil, err
	}
	p.expires = credentialsExpires(credentials, now)
	return cloneCredentials(p.cached), nil
}

func filterCredentials(credentials *rstream.TURNCredentials, allowed map[string]struct{}) (*rstream.TURNCredentials, error) {
	filtered := cloneCredentials(credentials)
	if filtered == nil {
		return nil, errors.New("TURN credentials are missing")
	}
	filtered.URLs = filtered.URLs[:0]
	for _, rawURL := range credentials.URLs {
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

func credentialsExpires(credentials *rstream.TURNCredentials, now time.Time) time.Time {
	if credentials == nil || credentials.TTL <= 0 {
		return now
	}
	expires := now.Add(time.Duration(credentials.TTL)*time.Second - 5*time.Minute)
	if expires.Before(now) {
		return now
	}
	return expires
}
