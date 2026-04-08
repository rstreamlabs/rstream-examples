package turn

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

type Provider struct {
	options rsconfig.TURNCredentialsEnvOptions
	mu      sync.Mutex
	cached  *rstream.TURNCredentials
	expires time.Time
}

func NewProvider(cfg config.Config) (*Provider, error) {
	ttl, err := cfg.TURNTTL()
	if err != nil {
		return nil, err
	}
	return &Provider{
		options: rsconfig.TURNCredentialsEnvOptions{
			TTL: ttl,
		},
	}, nil
}

func (p *Provider) Credentials(ctx context.Context) (*rstream.TURNCredentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Now().Before(p.expires) {
		copy := *p.cached
		copy.URLs = append([]string(nil), p.cached.URLs...)
		return &copy, nil
	}
	credentials, err := rsconfig.CreateTURNCredentialsFromEnv(ctx, p.options)
	if err != nil {
		return nil, fmt.Errorf("failed to create TURN credentials: %w", err)
	}
	p.cached = credentials
	p.expires = time.Now().Add(time.Duration(credentials.TTL)*time.Second - 5*time.Minute)
	if p.expires.Before(time.Now()) {
		p.expires = time.Now()
	}
	return credentials, nil
}

func ICEConfig(credentials *rstream.TURNCredentials) webrtc.Configuration {
	if credentials == nil {
		return webrtc.Configuration{}
	}
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
