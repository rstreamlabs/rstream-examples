// Package tunnel publishes one HTTP rstream tunnel and exposes it as a
// net.Listener, mirroring the pattern used by the other Go samples.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/config"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

// Manager owns the control connection and the published tunnel.
type Manager struct {
	control   interface{ Close() error }
	tunnel    rstream.BytestreamTunnel
	publicURL string
	closeOnce sync.Once
	closeErr  error
}

// Open connects to the rstream engine and creates one published HTTP tunnel.
func Open(ctx context.Context, cfg config.Config) (*Manager, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to rstream tunnel engine: %w", err)
	}
	name := strings.TrimSpace(cfg.TunnelName)
	if name == "" {
		name = "private-llm-mesh"
	}
	properties := rstream.TunnelProperties{
		Name:        rstream.StringPtr(name),
		Publish:     rstream.BoolPtr(true),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
	}
	if cfg.TokenAuth {
		properties.TokenAuth = rstream.BoolPtr(true)
	}
	if len(cfg.Labels) > 0 {
		properties.Labels = cfg.Labels
	}
	rawTunnel, err := control.CreateTunnel(ctx, properties)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("create published HTTP tunnel: %w", err)
	}
	t, ok := rawTunnel.(rstream.BytestreamTunnel)
	if !ok {
		_ = rawTunnel.Close()
		_ = control.Close()
		return nil, errors.New("created tunnel does not expose a bytestream listener")
	}
	publicURL, err := t.ForwardingAddress()
	if err != nil {
		_ = t.Close()
		_ = control.Close()
		return nil, fmt.Errorf("resolve published tunnel URL: %w", err)
	}
	return &Manager{control: control, tunnel: t, publicURL: publicURL}, nil
}

func newClient(cfg config.Config) (*rstream.Client, error) {
	engine := strings.TrimSpace(cfg.Engine)
	token := strings.TrimSpace(cfg.Token)
	if engine != "" || token != "" {
		if engine == "" {
			return nil, errors.New("rstream engine is required when a token is provided")
		}
		if token == "" {
			return nil, errors.New("rstream token is required when an engine is provided")
		}
		client, err := rstream.NewClient(rstream.ClientOptions{Engine: engine, Token: token})
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
	client, err := rsconfig.NewClientFromResolved(resolution.Resolved)
	if err != nil {
		return nil, fmt.Errorf("create rstream client from local configuration: %w", err)
	}
	return client, nil
}

// Listener returns the published tunnel as a net.Listener for http.Serve.
func (m *Manager) Listener() net.Listener { return m.tunnel }

// PublicURL is the address the mesh reaches this worker on.
func (m *Manager) PublicURL() string { return m.publicURL }

// Close tears down the tunnel and control connection.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		if m.tunnel != nil {
			m.closeErr = errors.Join(m.closeErr, m.tunnel.Close())
		}
		if m.control != nil {
			m.closeErr = errors.Join(m.closeErr, m.control.Close())
		}
	})
	return m.closeErr
}
