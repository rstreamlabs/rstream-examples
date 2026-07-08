// See LICENSE file in the project root for license information.

package egress

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	connectip "github.com/quic-go/connect-ip-go"
	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/authz"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/metrics"
	"github.com/yosida95/uritemplate/v3"
)

type Server struct {
	Policy          Policy
	Auth            authz.Checker
	Metrics         *metrics.CounterSet
	Logger          *slog.Logger
	ConnectIP       IPBackend
	udpProxy        *masque.Proxy
	udpProxyCloseMu sync.Once
}

type IPBackend interface {
	ServeIP(context.Context, *connectip.Conn) error
}

func NewServer(policy Policy, auth authz.Checker, counters *metrics.CounterSet, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if counters == nil {
		counters = metrics.NewCounterSet()
	}
	return &Server{
		Policy:   policy,
		Auth:     auth,
		Metrics:  counters,
		Logger:   logger,
		udpProxy: &masque.Proxy{},
	}
}

func (s *Server) Close() error {
	var err error
	s.udpProxyCloseMu.Do(func() {
		if s.udpProxy != nil {
			err = s.udpProxy.Close()
		}
	})
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proto := requestProtocol(r)
	if !s.Auth.Allow(r) {
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": proto, "outcome": "auth_denied"})
		w.Header().Set("Proxy-Authenticate", `Bearer realm="rstream-private-egress"`)
		http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
		s.Logger.Warn("proxy request rejected by authentication policy", "protocol", proto, "remote", r.RemoteAddr)
		return
	}
	switch {
	case isConnectUDP(r):
		s.handleConnectUDP(w, r)
	case isConnectIP(r):
		s.handleConnectIP(w, r)
	case r.Method == http.MethodConnect:
		s.handleConnect(w, r)
	default:
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": proto, "outcome": "method_denied"})
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	upstream, target, err := s.Policy.DialTCP(r.Context(), r.Host)
	if err != nil {
		s.writePolicyOrDialError(w, "connect", r.Host, err)
		return
	}
	defer upstream.Close()
	stream := &countingStreamConn{
		conn: &httpStreamConn{reader: r.Body, writer: w},
	}
	if r.ProtoMajor <= 1 {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(w, "hijack unavailable", http.StatusInternalServerError)
			return
		}
		downstream, bufrw, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer downstream.Close()
		if _, err := fmt.Fprint(bufrw, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := bufrw.Flush(); err != nil {
			return
		}
		stream.conn = &bufferedConn{Conn: downstream, reader: bufrw.Reader}
	} else {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flush unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Del("Content-Length")
		w.Header().Del("Transfer-Encoding")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		stream.conn = &httpStreamConn{reader: r.Body, writer: w, flusher: flusher}
	}
	relay(stream, upstream)
	s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect", "outcome": "ok"})
	s.Metrics.Add("rstream_private_masque_bytes_total", map[string]string{"protocol": "connect", "direction": "upstream"}, uint64(stream.upstreamBytes))
	s.Metrics.Add("rstream_private_masque_bytes_total", map[string]string{"protocol": "connect", "direction": "downstream"}, uint64(stream.downstreamBytes))
	s.Logger.Info(
		"CONNECT session closed",
		"target", target.Original,
		"resolved", target.Addr.String(),
		"duration_ms", time.Since(start).Milliseconds(),
		"upstream_bytes", stream.upstreamBytes,
		"downstream_bytes", stream.downstreamBytes,
	)
}

func (s *Server) handleConnectUDP(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http3.HTTPStreamer); !ok {
		http.Error(w, "CONNECT-UDP requires HTTP/3", http.StatusHTTPVersionNotSupported)
		return
	}
	template, err := connectUDPTemplate(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req, err := masque.ParseProxyRequest(r, template)
	if err != nil {
		writeParseError(w, err)
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_udp", "outcome": "parse_error"})
		return
	}
	conn, target, err := s.Policy.DialUDP(r.Context(), req.Target)
	if err != nil {
		s.writePolicyOrDialError(w, "connect_udp", req.Target, err)
		return
	}
	defer conn.Close()
	start := time.Now()
	if err := s.udpProxy.ProxyConnectedSocket(w, req, conn); err != nil {
		if !errors.Is(err, net.ErrClosed) && r.Context().Err() == nil {
			s.Logger.Warn("CONNECT-UDP session failed", "target", target.Original, "error", err)
			s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_udp", "outcome": "error"})
			return
		}
	}
	s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_udp", "outcome": "ok"})
	s.Logger.Info(
		"CONNECT-UDP session closed",
		"target", target.Original,
		"resolved", target.Addr.String(),
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func (s *Server) handleConnectIP(w http.ResponseWriter, r *http.Request) {
	if s.ConnectIP == nil {
		http.Error(w, "CONNECT-IP packet backend is not configured", http.StatusNotImplemented)
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_ip", "outcome": "disabled"})
		return
	}
	if _, ok := w.(http3.HTTPStreamer); !ok {
		http.Error(w, "CONNECT-IP requires HTTP/3", http.StatusHTTPVersionNotSupported)
		return
	}
	template, err := connectIPTemplate(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req, err := connectip.ParseRequest(r, template)
	if err != nil {
		writeParseError(w, err)
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_ip", "outcome": "parse_error"})
		return
	}
	conn, err := (&connectip.Proxy{}).Proxy(w, req)
	if err != nil {
		s.Logger.Warn("CONNECT-IP setup failed", "error", err)
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_ip", "outcome": "error"})
		return
	}
	defer conn.Close()
	start := time.Now()
	if err := s.ConnectIP.ServeIP(r.Context(), conn); err != nil && !errors.Is(err, net.ErrClosed) && r.Context().Err() == nil {
		s.Logger.Warn("CONNECT-IP session failed", "error", err)
		s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_ip", "outcome": "error"})
		return
	}
	s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect_ip", "outcome": "ok"})
	s.Logger.Info("CONNECT-IP session closed", "duration_ms", time.Since(start).Milliseconds())
}

func (s *Server) writePolicyOrDialError(w http.ResponseWriter, protocol, target string, err error) {
	status := http.StatusBadGateway
	outcome := "dial_error"
	if isPolicyError(err) {
		status = http.StatusForbidden
		outcome = "policy_denied"
	}
	if strings.Contains(err.Error(), "target must be") || strings.Contains(err.Error(), "target port") || strings.Contains(err.Error(), "target host") {
		status = http.StatusBadRequest
		outcome = "bad_target"
	}
	s.Metrics.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": protocol, "outcome": outcome})
	s.Logger.Warn("proxy request rejected", "protocol", protocol, "target", target, "status", status, "error", err)
	http.Error(w, http.StatusText(status), status)
}

func writeParseError(w http.ResponseWriter, err error) {
	var udpErr *masque.ProxyRequestParseError
	if errors.As(err, &udpErr) {
		http.Error(w, udpErr.Error(), udpErr.HTTPStatus)
		return
	}
	var ipErr *connectip.RequestParseError
	if errors.As(err, &ipErr) {
		http.Error(w, ipErr.Error(), ipErr.HTTPStatus)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func connectUDPTemplate(host string) (*uritemplate.Template, error) {
	return uritemplate.New("https://" + host + "/.well-known/masque/udp/{target_host}/{target_port}/")
}

func connectIPTemplate(host string) (*uritemplate.Template, error) {
	return uritemplate.New("https://" + host + "/connect-ip")
}

func requestProtocol(r *http.Request) string {
	switch {
	case isConnectUDP(r):
		return "connect_udp"
	case isConnectIP(r):
		return "connect_ip"
	case r.Method == http.MethodConnect:
		return "connect"
	default:
		return "http"
	}
}

func isConnectUDP(r *http.Request) bool {
	return r.Method == http.MethodConnect && r.Proto == "connect-udp"
}

func isConnectIP(r *http.Request) bool {
	return r.Method == http.MethodConnect && r.Proto == "connect-ip"
}

func isPolicyError(err error) bool {
	return errors.Is(err, errNoAllowedAddress) || strings.Contains(err.Error(), "has no allowed")
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}

type httpStreamConn struct {
	reader  io.ReadCloser
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (c *httpStreamConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *httpStreamConn) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if n > 0 && c.flusher != nil {
		c.flusher.Flush()
	}
	return n, err
}

func (c *httpStreamConn) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

type countingStreamConn struct {
	conn            io.ReadWriteCloser
	upstreamBytes   int64
	downstreamBytes int64
}

func (c *countingStreamConn) Read(p []byte) (int, error) {
	n, err := c.conn.Read(p)
	c.upstreamBytes += int64(n)
	return n, err
}

func (c *countingStreamConn) Write(p []byte) (int, error) {
	n, err := c.conn.Write(p)
	c.downstreamBytes += int64(n)
	return n, err
}

func (c *countingStreamConn) Close() error { return c.conn.Close() }

func relay(left, right io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = left.Close()
			_ = right.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer func() {
			closeBoth()
			wg.Done()
		}()
		_, _ = io.Copy(right, left)
	}()
	go func() {
		defer func() {
			closeBoth()
			wg.Done()
		}()
		_, _ = io.Copy(left, right)
	}()
	wg.Wait()
}

type DiagnosticIPBackend struct {
	ClientPrefix netip.Prefix
	ServerIP     netip.Addr
	Logger       *slog.Logger
}

func (b DiagnosticIPBackend) ServeIP(ctx context.Context, conn *connectip.Conn) error {
	clientPrefix := b.ClientPrefix
	if !clientPrefix.IsValid() {
		clientPrefix = netip.MustParsePrefix("10.77.0.2/32")
	}
	serverIP := b.ServerIP
	if !serverIP.IsValid() {
		serverIP = netip.MustParseAddr("10.77.0.1")
	}
	if err := conn.AssignAddresses(ctx, []netip.Prefix{clientPrefix}); err != nil {
		return fmt.Errorf("assign CONNECT-IP address: %w", err)
	}
	if err := conn.AdvertiseRoute(ctx, []connectip.IPRoute{{StartIP: serverIP, EndIP: serverIP, IPProtocol: 1}}); err != nil {
		return fmt.Errorf("advertise CONNECT-IP diagnostic route: %w", err)
	}
	buf := make([]byte, 1500)
	for {
		n, err := conn.ReadPacket(buf)
		if err != nil {
			return err
		}
		reply, err := swapIPv4Packet(buf[:n])
		if err != nil {
			if b.Logger != nil {
				b.Logger.Warn("CONNECT-IP diagnostic packet dropped", "error", err)
			}
			continue
		}
		if icmp, err := conn.WritePacket(reply); err != nil {
			return err
		} else if len(icmp) > 0 && b.Logger != nil {
			b.Logger.Warn("CONNECT-IP generated local ICMP response", "bytes", len(icmp))
		}
	}
}

func swapIPv4Packet(packet []byte) ([]byte, error) {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return nil, fmt.Errorf("expected IPv4 packet, got %d bytes", len(packet))
	}
	out := append([]byte(nil), packet...)
	copy(out[12:16], packet[16:20])
	copy(out[16:20], packet[12:16])
	out[8] = 64
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], ipv4HeaderChecksum(out[:20]))
	return out, nil
}

func ipv4HeaderChecksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(header); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func NormalizeAuthority(raw string) (string, error) {
	host, port, err := parseAuthority(raw)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

var _ http.Handler = (*Server)(nil)
