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
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/tunnel"
	turnprovider "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/turn"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/web"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

type App struct {
	cfg         config.Config
	logHub      *logs.Hub
	logger      *logs.Logger
	turn        *turnprovider.Provider
	broadcaster *rtc.Broadcaster
	web         *web.Server
	infoMu      sync.RWMutex
	info        web.Info
	openTunnel  func(context.Context, config.Config, *logs.Logger, *string) (tunnelManager, error)
}

type tunnelManager interface {
	Listener() net.Listener
	PublicURL() string
	ViewerURL() string
	AuthMode() config.TunnelAuthMode
	Close() error
}

func New(cfg config.Config) (*App, error) {
	logHub := logs.NewHub(256)
	logger := logs.NewLogger(logHub, cfg.Logging.Verbose)
	sourceFactory := media.NewGStreamerFactory(
		cfg.Media.Pipeline,
		cfg.Media.SinkName,
		cfg.InitialBitrateKbps(),
		logger,
	)
	turn, err := turnprovider.NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	broadcaster, err := rtc.NewBroadcaster(cfg, sourceFactory, turn, logger)
	if err != nil {
		return nil, err
	}
	instance := &App{
		cfg:         cfg,
		logHub:      logHub,
		logger:      logger,
		turn:        turn,
		broadcaster: broadcaster,
		openTunnel: func(
			ctx context.Context,
			cfg config.Config,
			logger *logs.Logger,
			accessToken *string,
		) (tunnelManager, error) {
			return tunnel.Open(ctx, cfg, logger, accessToken)
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
	)
	return instance, nil
}

func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	handler := a.web.Handler()
	localListener, err := net.Listen("tcp", a.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", a.cfg.Server.Listen, err)
	}
	a.info = web.Info{
		LocalURL:        "http://" + localListener.Addr().String(),
		AuthMode:        a.cfg.TunnelAuthMode(),
		VideoMimeType:   a.cfg.WebRTC.Video.MimeType,
		TWCCEnabled:     a.cfg.WebRTC.Interceptors.TWCC,
		NACKEnabled:     a.cfg.WebRTC.Interceptors.NACK,
		RTXEnabled:      a.cfg.WebRTC.Interceptors.RTX,
		FlexFECEnabled:  a.cfg.WebRTC.Interceptors.FlexFEC,
		AdaptiveBackend: a.cfg.AdaptiveBackend(),
	}
	a.web.SetInfo(a.info)
	localServer := &http.Server{Handler: handler}
	localServerErrors := serveHTTP(localServer, localListener)
	a.logger.Info("Local URL: %s", a.info.LocalURL)
	var tunnelErrors <-chan error
	if a.cfg.Tunnel.Enabled {
		accessToken, err := tunnel.ResolveAccessToken(a.cfg)
		if err != nil {
			_ = localServer.Shutdown(context.Background())
			return err
		}
		tunnelErrors = a.runTunnelLoop(ctx, handler, accessToken)
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
	_ = localServer.Shutdown(shutdownCtx)
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
	if closeErr := a.broadcaster.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	if errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func (a *App) runTunnelLoop(
	ctx context.Context,
	handler http.Handler,
	accessToken *string,
) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		errCh <- a.serveTunnelLoop(ctx, handler, accessToken)
	}()
	return errCh
}

func (a *App) serveTunnelLoop(
	ctx context.Context,
	handler http.Handler,
	accessToken *string,
) error {
	reconnectInterval, err := a.cfg.TunnelReconnectInterval()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = a.serveTunnelOnce(ctx, handler, accessToken)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if !a.cfg.Tunnel.Reconnect.Enabled {
			return err
		}
		if info := a.currentInfo(); info.PublicURL == nil {
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
	accessToken *string,
) error {
	tunnelManager, err := a.openTunnel(ctx, a.cfg, a.logger, accessToken)
	if err != nil {
		return err
	}
	defer func() {
		_ = tunnelManager.Close()
	}()
	a.setTunnelInfo(tunnelManager)
	accessURL := tunnelManager.PublicURL()
	if viewerURL := tunnelManager.ViewerURL(); viewerURL != tunnelManager.PublicURL() {
		accessURL = viewerURL
	}
	a.logger.Info("Public URL: %s", accessURL)
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

func (a *App) setTunnelInfo(tunnelManager tunnelManager) {
	a.infoMu.Lock()
	defer a.infoMu.Unlock()
	publicURL := tunnelManager.PublicURL()
	viewerURL := tunnelManager.ViewerURL()
	a.info.PublicURL = &publicURL
	a.info.ViewerURL = &viewerURL
	a.info.AuthMode = tunnelManager.AuthMode()
	a.web.SetInfo(a.info)
}

func (a *App) clearTunnelInfo() {
	a.infoMu.Lock()
	defer a.infoMu.Unlock()
	a.info.PublicURL = nil
	a.info.ViewerURL = nil
	a.info.AuthMode = a.cfg.TunnelAuthMode()
	a.web.SetInfo(a.info)
}

func (a *App) currentInfo() web.Info {
	a.infoMu.RLock()
	defer a.infoMu.RUnlock()
	return a.info
}

func serveHTTP(server *http.Server, listener net.Listener) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		errCh <- server.Serve(listener)
	}()
	return errCh
}
