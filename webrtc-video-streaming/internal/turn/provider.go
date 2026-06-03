package turn

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/provisioning"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

type Provider struct {
	provisioning *provisioning.Client
	options      rsconfig.TURNCredentialsEnvOptions
	mu           sync.Mutex
	cached       *rstream.TURNCredentials
	expires      time.Time
}

func NewProvider(cfg config.Config, provisioningClient *provisioning.Client) (*Provider, error) {
	ttl, err := cfg.TURNTTL()
	if err != nil {
		return nil, err
	}
	return &Provider{
		provisioning: provisioningClient,
		options: rsconfig.TURNCredentialsEnvOptions{
			TTL: ttl,
		},
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
		p.cached = credentials
		p.expires = provisioning.TURNExpires(credentials, now)
		return cloneCredentials(p.cached), nil
	}
	// Local demos can mint rstream TURN credentials directly from SDK env config.
	credentials, err := rsconfig.CreateTURNCredentialsFromEnv(ctx, p.options)
	if err != nil {
		return nil, fmt.Errorf("failed to create TURN credentials: %w", err)
	}
	p.cached = credentials
	p.expires = credentialsExpires(credentials, now)
	return cloneCredentials(p.cached), nil
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
