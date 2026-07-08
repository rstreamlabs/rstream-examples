package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

type Manager struct {
	logger    *logs.Logger
	control   interface{ Close() error }
	tunnel    rstream.BytestreamTunnel
	publicURL string
	auth      config.TunnelAuthConfig
	closeOnce sync.Once
	closeErr  error
}

type OpenOptions struct {
	Engine      string
	Token       string
	Name        string
	Labels      map[string]string
	Provisioned bool
}

func Open(ctx context.Context, cfg config.Config, logger *logs.Logger, opts OpenOptions) (*Manager, error) {
	client, err := newRstreamClient(opts, cfg.Tunnel.Transport.UseQUIC)
	if err != nil {
		return nil, err
	}
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to rstream tunnel engine: %w", err)
	}
	auth := cfg.Tunnel.Auth
	if opts.Provisioned {
		auth = config.TunnelAuthConfig{Token: true}
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = strings.TrimSpace(cfg.Tunnel.Name)
	}
	properties := rstream.TunnelProperties{
		Name:        rstream.StringPtr(name),
		Publish:     rstream.BoolPtr(true),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
	}
	if auth.Token {
		properties.TokenAuth = rstream.BoolPtr(true)
	}
	if auth.Rstream {
		properties.RstreamAuth = rstream.BoolPtr(true)
	}
	if len(opts.Labels) > 0 {
		labels := make(map[string]string, len(opts.Labels))
		for key, value := range opts.Labels {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				labels[key] = value
			}
		}
		if len(labels) > 0 {
			properties.Labels = labels
		}
	}
	// Create one published HTTP tunnel for the embedded device web server.
	rawTunnel, err := control.CreateTunnel(ctx, properties)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("create published HTTP tunnel: %w", err)
	}
	tunnel, ok := rawTunnel.(rstream.BytestreamTunnel)
	if !ok {
		_ = rawTunnel.Close()
		_ = control.Close()
		return nil, errors.New("created tunnel does not expose a bytestream listener")
	}
	publicURL, err := tunnel.ForwardingAddress()
	if err != nil {
		_ = tunnel.Close()
		_ = control.Close()
		return nil, fmt.Errorf("resolve published tunnel URL: %w", err)
	}
	return &Manager{
		logger:    logger,
		control:   control,
		tunnel:    tunnel,
		publicURL: publicURL,
		auth:      auth,
	}, nil
}

func newRstreamClient(opts OpenOptions, useQUIC bool) (*rstream.Client, error) {
	engine := strings.TrimSpace(opts.Engine)
	token := strings.TrimSpace(opts.Token)
	useQUIC = useQUIC || rsconfig.ReadEnv().UseQUIC
	if engine != "" || token != "" {
		if engine == "" {
			return nil, errors.New("rstream engine is required when a provisioned token is provided")
		}
		if token == "" {
			return nil, errors.New("rstream token is required when a provisioned engine is provided")
		}
		options := rstream.ClientOptions{
			Engine: engine,
			Token:  token,
		}
		if useQUIC {
			options.Transport = &rstream.QUICTransport{}
		}
		// Provisioned devices receive an explicit engine URL and scoped token.
		client, err := rstream.NewClient(options)
		if err != nil {
			return nil, fmt.Errorf("create rstream client from provisioned configuration: %w", err)
		}
		return client, nil
	}
	resolution, err := rsconfig.ResolveFromEnv(rsconfig.ClientEnvOptions{
		RequireEngine: true,
		RequireToken:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve local rstream client configuration: %w", err)
	}
	if useQUIC {
		resolution.Resolved.Transport = &rstream.QUICTransport{}
	}
	// Local demos reuse the resolved CLI context when provisioning is disabled.
	client, err := rsconfig.NewClientFromResolved(resolution.Resolved)
	if err != nil {
		return nil, fmt.Errorf("create rstream client from local configuration: %w", err)
	}
	return client, nil
}

func (m *Manager) Listener() net.Listener {
	return m.tunnel
}

func (m *Manager) PublicURL() string {
	return m.publicURL
}

func (m *Manager) Auth() config.TunnelAuthConfig {
	return m.auth
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		if m.tunnel != nil {
			m.closeErr = errors.Join(m.closeErr, m.tunnel.Close())
		}
		if m.control != nil {
			m.closeErr = errors.Join(m.closeErr, m.control.Close())
		}
		if m.closeErr != nil && m.logger != nil {
			m.logger.Warn("Public tunnel close failed: %v", m.closeErr)
		}
	})
	return m.closeErr
}
