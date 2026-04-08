package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"
)

type Manager struct {
	logger    *logs.Logger
	control   interface{ Close() error }
	tunnel    rstream.BytestreamTunnel
	publicURL string
	viewerURL string
	authMode  config.TunnelAuthMode
}

func ResolveAccessToken(cfg config.Config) (*string, error) {
	if cfg.TunnelAuthMode() != config.TunnelAuthModeToken {
		return nil, nil
	}
	if token := strings.TrimSpace(cfg.Tunnel.Auth.Token); token != "" {
		return &token, nil
	}
	resolution, err := rsconfig.ResolveFromEnv(rsconfig.ClientEnvOptions{
		RequireToken: true,
	})
	if err != nil {
		return nil, err
	}
	token := resolution.Resolved.Token
	return &token, nil
}

func Open(ctx context.Context, cfg config.Config, logger *logs.Logger, accessToken *string) (*Manager, error) {
	resolution, err := rsconfig.ResolveFromEnv(rsconfig.ClientEnvOptions{
		RequireEngine: true,
		RequireToken:  true,
	})
	if err != nil {
		return nil, err
	}
	client, err := rsconfig.NewClientFromResolved(resolution.Resolved)
	if err != nil {
		return nil, err
	}
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the rstream engine server: %w", err)
	}
	properties := rstream.TunnelProperties{
		Name:        rstream.StringPtr(strings.TrimSpace(cfg.Tunnel.Name)),
		Publish:     rstream.BoolPtr(true),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
	}
	authMode := cfg.TunnelAuthMode()
	switch authMode {
	case config.TunnelAuthModePlain:
	case config.TunnelAuthModeToken:
		properties.TokenAuth = rstream.BoolPtr(true)
	case config.TunnelAuthModeRstream:
		properties.RstreamAuth = rstream.BoolPtr(true)
	default:
		_ = control.Close()
		return nil, fmt.Errorf("invalid tunnel auth mode %q", cfg.Tunnel.Auth.Mode)
	}
	rawTunnel, err := control.CreateTunnel(ctx, properties)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("failed to create the published HTTP tunnel: %w", err)
	}
	tunnel, ok := rawTunnel.(rstream.BytestreamTunnel)
	if !ok {
		_ = rawTunnel.Close()
		_ = control.Close()
		return nil, fmt.Errorf("the tunnel does not expose a bytestream listener")
	}
	publicURL, err := tunnel.ForwardingAddress()
	if err != nil {
		_ = tunnel.Close()
		_ = control.Close()
		return nil, fmt.Errorf("failed to compute the public tunnel URL: %w", err)
	}
	viewerURL := publicURL
	if authMode == config.TunnelAuthModeToken {
		token := resolution.Resolved.Token
		if accessToken != nil {
			token = strings.TrimSpace(*accessToken)
		}
		if strings.TrimSpace(cfg.Tunnel.Auth.Token) == "" {
			logger.Warn("Tunnel token auth is using the current rstream token. Use a dedicated token for any shared or repeatable deployment.")
		}
		viewerURL, err = appendQueryToken(publicURL, token)
		if err != nil {
			_ = tunnel.Close()
			_ = control.Close()
			return nil, err
		}
	}
	return &Manager{
		logger:    logger,
		control:   control,
		tunnel:    tunnel,
		publicURL: publicURL,
		viewerURL: viewerURL,
		authMode:  authMode,
	}, nil
}

func (m *Manager) Listener() net.Listener {
	return m.tunnel
}

func (m *Manager) PublicURL() string {
	return m.publicURL
}

func (m *Manager) ViewerURL() string {
	return m.viewerURL
}

func (m *Manager) AuthMode() config.TunnelAuthMode {
	return m.authMode
}

func (m *Manager) Close() error {
	if m.tunnel != nil {
		if err := m.tunnel.Close(); err != nil {
			return err
		}
	}
	if m.control != nil {
		return m.control.Close()
	}
	return nil
}

func appendQueryToken(rawURL, token string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to build the tokenized public URL: %w", err)
	}
	query := u.Query()
	query.Set("rstream.token", token)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
