package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/provisioning"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/tunnel"
	turnprovider "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/turn"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/web"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

type App struct {
	cfg          config.Config
	logHub       *logs.Hub
	logger       *logs.Logger
	provisioning *provisioning.Client
	turn         *turnprovider.Provider
	broadcaster  *rtc.Broadcaster
	web          *web.Server
	infoMu       sync.RWMutex
	info         web.Info
	openTunnel   func(context.Context, config.Config, *logs.Logger, tunnel.OpenOptions) (tunnelManager, error)
}

type tunnelManager interface {
	Listener() net.Listener
	PublicURL() string
	Auth() config.TunnelAuthConfig
	Close() error
}

func New(cfg config.Config) (*App, error) {
	logHub := logs.NewHub(256)
	logger := logs.NewLogger(logHub, cfg.Logging.Verbose)
	var provisioningClient *provisioning.Client
	if cfg.TunnelProvisioningMode() == config.TunnelProvisioningModeRemote {
		var err error
		provisioningClient, err = provisioning.NewClient(cfg)
		if err != nil {
			return nil, err
		}
	}
	sourceFactory := media.NewGStreamerFactory(
		cfg.Media.Pipeline,
		cfg.Media.SinkName,
		cfg.InitialBitrateKbps(),
		logger,
	)
	turn, err := turnprovider.NewProvider(cfg, provisioningClient)
	if err != nil {
		return nil, err
	}
	broadcaster, err := rtc.NewBroadcaster(cfg, sourceFactory, turn, logger)
	if err != nil {
		return nil, err
	}
	instance := &App{
		cfg:          cfg,
		logHub:       logHub,
		logger:       logger,
		provisioning: provisioningClient,
		turn:         turn,
		broadcaster:  broadcaster,
		openTunnel: func(
			ctx context.Context,
			cfg config.Config,
			logger *logs.Logger,
			opts tunnel.OpenOptions,
		) (tunnelManager, error) {
			return tunnel.Open(ctx, cfg, logger, opts)
		},
	}
	instance.web = web.NewServer(
		logger,
		logHub,
		func(ctx context.Context) (*rstream.TURNCredentials, error) {
			return turn.Credentials(ctx)
		},
		func(ctx context.Context, send func(rtc.SignalMessage) error) (*rtc.Session, error) {
			return broadcaster.OpenSession(ctx, send)
		},
		web.ServerOptions{
			Viewer: cfg.Web.Viewer.Enabled,
		},
	)
	return instance, nil
}

func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	handler := a.web.Handler()
	a.info = web.Info{
		TunnelAuth:      a.configuredTunnelAuth(),
		VideoMimeType:   a.cfg.WebRTC.Video.MimeType,
		TWCCEnabled:     a.cfg.WebRTC.Interceptors.TWCC,
		NACKEnabled:     a.cfg.WebRTC.Interceptors.NACK,
		RTXEnabled:      a.cfg.WebRTC.Interceptors.RTX,
		FlexFECEnabled:  a.cfg.WebRTC.Interceptors.FlexFEC,
		AdaptiveBackend: a.cfg.AdaptiveBackend(),
	}
	var localServer *http.Server
	var localServerErrors <-chan error
	if a.localServerEnabled() {
		localListener, err := net.Listen("tcp", a.cfg.Server.Listen)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", a.cfg.Server.Listen, err)
		}
		a.info.LocalURL = "http://" + localListener.Addr().String()
		localServer = &http.Server{Handler: handler}
		localServerErrors = serveHTTP(localServer, localListener)
		a.logger.Info("Local URL: %s", a.info.LocalURL)
	}
	a.web.SetInfo(a.info)
	var tunnelErrors <-chan error
	if a.cfg.Tunnel.Enabled {
		tunnelErrors = a.runTunnelLoop(ctx, handler)
	}
	var runErr error
	select {
	case err := <-localServerErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("the local HTTP server stopped: %w", err)
			cancel()
		}
	case err := <-tunnelErrors:
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = err
			cancel()
		}
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if localServer != nil {
		_ = localServer.Shutdown(shutdownCtx)
	}
	if tunnelErrors != nil {
		select {
		case err := <-tunnelErrors:
			if runErr == nil && err != nil && !errors.Is(err, context.Canceled) {
				runErr = err
			}
		case <-time.After(10 * time.Second):
			if runErr == nil {
				runErr = errors.New("tunnel shutdown timed out")
			}
		}
	}
	if a.broadcaster != nil {
		closeErr := a.broadcaster.Close()
		if closeErr != nil && runErr == nil {
			runErr = closeErr
		}
	}
	if errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func (a *App) localServerEnabled() bool {
	return a.cfg.Web.Viewer.Enabled || !a.cfg.Tunnel.Enabled
}

func (a *App) runTunnelLoop(
	ctx context.Context,
	handler http.Handler,
) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		errCh <- a.serveTunnelLoop(ctx, handler)
	}()
	return errCh
}

func (a *App) serveTunnelLoop(
	ctx context.Context,
	handler http.Handler,
) error {
	reconnectInterval, err := a.cfg.TunnelReconnectInterval()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = a.serveTunnelOnce(ctx, handler)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if !a.cfg.Tunnel.Reconnect.Enabled {
			return err
		}
		if !a.publicTunnelOnline() {
			a.logger.Warn("Public tunnel connection failed: %v", err)
		} else {
			a.logger.Warn("Public tunnel disconnected: %v", err)
		}
		a.clearTunnelInfo()
		a.logger.Info("Retrying tunnel connection in %s", reconnectInterval)
		select {
		case <-time.After(reconnectInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *App) serveTunnelOnce(
	ctx context.Context,
	handler http.Handler,
) error {
	openOptions, err := a.resolveTunnelOpenOptions(ctx)
	if err != nil {
		return err
	}
	tunnelManager, err := a.openTunnel(ctx, a.cfg, a.logger, openOptions)
	if err != nil {
		return err
	}
	defer func() { _ = tunnelManager.Close() }()
	a.setTunnelInfo(tunnelManager)
	a.logger.Info("Public URL: %s", tunnelManager.PublicURL())
	server := &http.Server{Handler: handler}
	serverErrors := serveHTTP(server, tunnelManager.Listener())
	select {
	case err := <-serverErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("the public tunnel stopped: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	}
}

func (a *App) resolveTunnelOpenOptions(ctx context.Context) (tunnel.OpenOptions, error) {
	if a.provisioning != nil {
		response, err := a.provisioning.Tunnel(ctx)
		if err != nil {
			return tunnel.OpenOptions{}, fmt.Errorf("failed to provision tunnel: %w", err)
		}
		return tunnel.OpenOptions{
			Engine:      response.Engine,
			Token:       response.Token,
			Name:        response.Name,
			Labels:      response.Labels,
			Provisioned: true,
		}, nil
	}
	return tunnel.OpenOptions{}, nil
}

func (a *App) setTunnelInfo(tunnelManager tunnelManager) {
	a.infoMu.Lock()
	defer a.infoMu.Unlock()
	publicURL := tunnelManager.PublicURL()
	a.info.PublicURL = &publicURL
	a.info.TunnelAuth = tunnelManager.Auth()
	a.web.SetInfo(a.info)
}

func (a *App) clearTunnelInfo() {
	a.infoMu.Lock()
	defer a.infoMu.Unlock()
	a.info.PublicURL = nil
	a.info.TunnelAuth = a.configuredTunnelAuth()
	a.web.SetInfo(a.info)
}

func (a *App) publicTunnelOnline() bool {
	a.infoMu.RLock()
	defer a.infoMu.RUnlock()
	return a.info.PublicURL != nil
}

func (a *App) configuredTunnelAuth() config.TunnelAuthConfig {
	if a.provisioning != nil {
		return config.TunnelAuthConfig{Token: true}
	}
	return a.cfg.Tunnel.Auth
}

func serveHTTP(server *http.Server, listener net.Listener) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		errCh <- server.Serve(listener)
	}()
	return errCh
}
