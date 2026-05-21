// See LICENSE file in the project root for license information.

// private-masque-egress-gateway publishes a rstream HTTP/3 tunnel and serves a
// private MASQUE egress gateway behind it.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	rsconfig "github.com/rstreamlabs/rstream-go/config"

	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/authz"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/egress"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/metrics"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/mobileconfig"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/tlsutil"
)

type labelFlags map[string]string

type stringFlags []string

func (l *labelFlags) String() string {
	if l == nil || len(*l) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*l))
	for key, value := range *l {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (l *labelFlags) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("label must be key=value")
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)
	if key == "" || val == "" {
		return fmt.Errorf("label key and value must be non-empty")
	}
	if *l == nil {
		*l = make(map[string]string)
	}
	(*l)[key] = val
	return nil
}

func (s *stringFlags) String() string {
	if s == nil || len(*s) == 0 {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value must be non-empty")
	}
	*s = append(*s, value)
	return nil
}

func main() {
	stdlog.SetOutput(io.Discard)
	var labels labelFlags
	var matchDomains stringFlags
	name := flag.String("name", "private-masque-egress", "rstream tunnel name")
	hostname := flag.String("hostname", "", "optional stable rstream hostname")
	local := flag.Bool("local", false, "serve a local HTTP/3 endpoint instead of publishing through rstream")
	listen := flag.String("listen", "127.0.0.1:9443", "local UDP listen address when --local is set")
	metricsAddr := flag.String("metrics", "", "optional Prometheus metrics listen address")
	accessHeader := flag.String("access-header", "X-Egress-Token", "request header used for gateway access tokens")
	accessToken := flag.String("access-token", os.Getenv("PRIVATE_EGRESS_TOKEN"), "gateway access token; defaults to PRIVATE_EGRESS_TOKEN")
	rstreamTokenAuth := flag.Bool("rstream-token-auth", false, "require rstream edge token authentication")
	rstreamAuth := flag.Bool("rstream-auth", false, "require rstream account authentication at the edge")
	allowPrivate := flag.Bool("allow-private-targets", false, "allow egress to RFC1918 and ULA targets")
	allowLoopback := flag.Bool("allow-loopback-targets", false, "allow egress to loopback targets")
	allowLinkLocal := flag.Bool("allow-link-local-targets", false, "allow egress to link-local targets")
	connectIPDiagnostic := flag.Bool("connect-ip-diagnostic", false, "enable CONNECT-IP diagnostic packet echo backend")
	mobileconfigPath := flag.String("write-mobileconfig", "", "write an Apple Relay .mobileconfig for the published endpoint")
	verbose := flag.Bool("verbose", false, "enable debug logs")
	flag.Var(&labels, "label", "rstream tunnel label as key=value; may be repeated")
	flag.Var(&matchDomains, "match-domain", "domain routed by the generated Apple Relay profile; may be repeated")
	flag.Parse()
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	counters := metrics.NewCounterSet()
	policy := egress.DefaultPolicy()
	policy.AllowPrivate = *allowPrivate
	policy.AllowLoopback = *allowLoopback
	policy.AllowLinkLocal = *allowLinkLocal
	access := authz.New(*accessHeader, *accessToken)
	gateway := egress.NewServer(policy, access, counters, logger)
	if *connectIPDiagnostic {
		gateway.ConnectIP = egress.DiagnosticIPBackend{Logger: logger}
	}
	if !access.Enabled() {
		logger.Warn("gateway access token is disabled; use --access-token or PRIVATE_EGRESS_TOKEN outside local tests")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := runOptions{
		name:             *name,
		hostname:         *hostname,
		labels:           labels,
		tokenAuth:        *rstreamTokenAuth,
		rstreamAuth:      *rstreamAuth,
		accessHeader:     access.Header(),
		accessToken:      *accessToken,
		mobileconfigPath: *mobileconfigPath,
		matchDomains:     matchDomains,
		logger:           logger,
	}
	if *metricsAddr != "" {
		go serveMetrics(ctx, *metricsAddr, counters, logger)
	}
	var err error
	if *local {
		err = runLocal(ctx, *listen, gateway, opts)
	} else {
		err = runRstream(ctx, gateway, opts)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

type runOptions struct {
	name             string
	hostname         string
	labels           map[string]string
	tokenAuth        bool
	rstreamAuth      bool
	accessHeader     string
	accessToken      string
	mobileconfigPath string
	matchDomains     []string
	logger           *slog.Logger
}

func runRstream(ctx context.Context, handler http.Handler, opts runOptions) error {
	client, err := rsconfig.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("load rstream configuration: %w", err)
	}
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect to rstream engine: %w", err)
	}
	defer ctrl.Close()
	props := rstream.TunnelProperties{
		Name:        rstream.StringPtr(opts.name),
		Type:        rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish:     rstream.BoolPtr(true),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
	}
	if opts.hostname != "" {
		props.Hostname = rstream.StringPtr(opts.hostname)
	}
	if opts.tokenAuth {
		props.TokenAuth = rstream.BoolPtr(true)
	}
	if opts.rstreamAuth {
		props.RstreamAuth = rstream.BoolPtr(true)
	}
	if len(opts.labels) > 0 {
		props.Labels = opts.labels
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("create published HTTP/3 tunnel: %w", err)
	}
	defer tunnel.Close()
	datagramTunnel, ok := tunnel.(rstream.DatagramTunnel)
	if !ok {
		return fmt.Errorf("created tunnel does not expose a datagram listener")
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("resolve forwarding address: %w", err)
	}
	opts.logger.Info("private MASQUE egress gateway online", "forwarding", forwarding, "tunnel", opts.name)
	fmt.Printf("READY %s\n", forwarding)
	if opts.mobileconfigPath != "" {
		if err := writeMobileconfig(opts.mobileconfigPath, forwarding, opts); err != nil {
			return err
		}
		opts.logger.Info("wrote Apple Relay profile", "path", opts.mobileconfigPath)
	}
	packetConn := rstream.PacketConnFromPacketListener(datagramTunnel)
	return serveH3(ctx, packetConn, handler)
}

func runLocal(ctx context.Context, addr string, handler http.Handler, opts runOptions) error {
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen UDP %s: %w", addr, err)
	}
	forwarding := "https://" + packetConn.LocalAddr().String()
	opts.logger.Info("private MASQUE egress gateway listening locally", "addr", packetConn.LocalAddr().String())
	fmt.Printf("READY %s\n", forwarding)
	if opts.mobileconfigPath != "" {
		if err := writeMobileconfig(opts.mobileconfigPath, forwarding, opts); err != nil {
			_ = packetConn.Close()
			return err
		}
		opts.logger.Info("wrote Apple Relay profile", "path", opts.mobileconfigPath)
	}
	return serveH3(ctx, packetConn, handler)
}

func serveH3(ctx context.Context, packetConn net.PacketConn, handler http.Handler) error {
	tlsConfig, err := tlsutil.SelfSignedH3Config()
	if err != nil {
		_ = packetConn.Close()
		return fmt.Errorf("create TLS config: %w", err)
	}
	server := &http3.Server{
		Handler:         handler,
		TLSConfig:       tlsConfig,
		EnableDatagrams: true,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(packetConn)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = packetConn.Close()
		return ctx.Err()
	case err := <-errCh:
		_ = packetConn.Close()
		return err
	}
}

func serveMetrics(ctx context.Context, addr string, counters *metrics.CounterSet, logger *slog.Logger) {
	server := &http.Server{Addr: addr, Handler: counters.Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("metrics endpoint listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("metrics endpoint stopped", "error", err)
	}
}

func writeMobileconfig(path, forwarding string, opts runOptions) error {
	http3URL, http2URL, err := mobileconfig.RelayURLsFromForwarding(forwarding)
	if err != nil {
		return fmt.Errorf("build Apple Relay URLs: %w", err)
	}
	profile, err := mobileconfig.Render(mobileconfig.Options{
		DisplayName:   "rstream private MASQUE egress",
		Identifier:    "io.rstream.examples.private-masque-egress." + sanitizeIdentifierPart(opts.name),
		HTTP3RelayURL: http3URL,
		HTTP2RelayURL: http2URL,
		MatchDomains:  opts.matchDomains,
		HeaderName:    opts.accessHeader,
		HeaderValue:   opts.accessToken,
	})
	if err != nil {
		return fmt.Errorf("render Apple Relay profile: %w", err)
	}
	if err := os.WriteFile(path, profile, 0600); err != nil {
		return fmt.Errorf("write Apple Relay profile: %w", err)
	}
	return nil
}

func sanitizeIdentifierPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "gateway"
	}
	return out
}
