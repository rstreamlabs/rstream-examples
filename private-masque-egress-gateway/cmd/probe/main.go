// See LICENSE file in the project root for license information.

// probe verifies a private MASQUE egress gateway through its published rstream
// endpoint. It is intentionally small enough to read, but it exercises the real
// CONNECT and CONNECT-UDP protocol paths.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/yosida95/uritemplate/v3"
	"golang.org/x/net/http2"
)

const payload = "rstream-private-egress-probe"

func main() {
	addr := flag.String("addr", "", "gateway forwarding address, e.g. https://example.rstream.io")
	tcpTarget := flag.String("tcp-target", "", "TCP echo target host:port for plain CONNECT checks")
	udpTarget := flag.String("udp-target", "", "UDP echo target host:port for CONNECT-UDP checks")
	downstream := flag.String("downstream", "all", "plain CONNECT downstream: h1, h2, h3, all, none")
	accessHeader := flag.String("access-header", "X-Egress-Token", "gateway access token header")
	accessToken := flag.String("access-token", "", "gateway access token")
	caFile := flag.String("ca-file", "", "additional PEM CA bundle for gateway verification")
	serverName := flag.String("server-name", "", "gateway TLS verification name and SNI; defaults to address host")
	insecureDemoTLS := flag.Bool("insecure-demo-tls", false, "disable certificate verification for a literal loopback demo only")
	timeout := flag.Duration("timeout", 20*time.Second, "probe timeout")
	flag.Parse()
	if *addr == "" {
		log.Fatal("--addr is required")
	}
	hostport, baseURL, err := normalizeEndpoint(*addr)
	if err != nil {
		log.Fatalf("addr: %v", err)
	}
	tlsConfig, err := newProbeTLSConfig(hostport, *caFile, *serverName, *insecureDemoTLS)
	if err != nil {
		log.Fatalf("TLS configuration: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	header := accessHeaders(*accessHeader, *accessToken)
	switch strings.ToLower(strings.TrimSpace(*downstream)) {
	case "all":
		for _, proto := range []string{"h1", "h2", "h3"} {
			if *tcpTarget == "" {
				log.Fatal("--tcp-target is required when --downstream is not none")
			}
			if err := runPlainConnect(ctx, proto, hostport, *tcpTarget, header, tlsConfig); err != nil {
				log.Fatalf("CONNECT %s: %v", proto, err)
			}
			fmt.Printf("CONNECT %s ok\n", proto)
		}
	case "h1", "h2", "h3":
		if *tcpTarget == "" {
			log.Fatal("--tcp-target is required when --downstream is not none")
		}
		if err := runPlainConnect(ctx, *downstream, hostport, *tcpTarget, header, tlsConfig); err != nil {
			log.Fatalf("CONNECT %s: %v", *downstream, err)
		}
		fmt.Printf("CONNECT %s ok\n", *downstream)
	case "none":
	default:
		log.Fatalf("invalid --downstream %q", *downstream)
	}
	if *udpTarget != "" {
		if err := runConnectUDP(ctx, hostport, baseURL, *udpTarget, header, tlsConfig); err != nil {
			log.Fatalf("CONNECT-UDP: %v", err)
		}
		fmt.Println("CONNECT-UDP ok")
	}
}

func normalizeEndpoint(raw string) (hostport, baseURL string, err error) {
	value := strings.TrimSpace(raw)
	if i := strings.IndexAny(value, " \t\r\n"); i >= 0 {
		value = value[:i]
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", err
	}
	hostport = parsed.Host
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		var addrErr *net.AddrError
		if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
			hostport = net.JoinHostPort(hostport, "443")
		} else {
			return "", "", err
		}
	}
	return hostport, "https://" + hostport, nil
}

func accessHeaders(name, token string) http.Header {
	h := http.Header{}
	name = strings.TrimSpace(name)
	token = strings.TrimSpace(token)
	if name != "" && token != "" {
		h.Set(name, token)
	}
	return h
}

func newProbeTLSConfig(forwarding, caFile, serverName string, insecureDemo bool) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(forwarding)
	if err != nil {
		return nil, err
	}
	if insecureDemo {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("--insecure-demo-tls requires a literal loopback gateway address")
		}
		if caFile != "" || serverName != "" {
			return nil, errors.New("--insecure-demo-tls cannot be combined with --ca-file or --server-name")
		}
	}
	if serverName == "" {
		serverName = host
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, InsecureSkipVerify: insecureDemo}
	if caFile != "" {
		bundle, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read gateway CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system roots: %w", err)
		}
		if !roots.AppendCertsFromPEM(bundle) {
			return nil, errors.New("gateway CA file contains no certificates")
		}
		config.RootCAs = roots
	}
	return config, nil
}

func runPlainConnect(ctx context.Context, downstream, forwarding, target string, header http.Header, tlsConfig *tls.Config) error {
	var conn io.ReadWriteCloser
	var err error
	switch strings.ToLower(downstream) {
	case "h1":
		conn, err = dialH1(ctx, forwarding, target, header, tlsConfig)
	case "h2":
		conn, err = dialH2(ctx, forwarding, target, header, tlsConfig)
	case "h3":
		conn, err = dialH3Connect(ctx, forwarding, target, header, tlsConfig)
	default:
		return fmt.Errorf("unsupported downstream %q", downstream)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	return verifyStreamEcho(conn)
}

func dialH1(ctx context.Context, forwarding, target string, header http.Header, tlsConfig *tls.Config) (io.ReadWriteCloser, error) {
	config := tlsConfig.Clone()
	config.NextProtos = []string{"http/1.1"}
	conn, err := (&tls.Dialer{Config: config}).DialContext(ctx, "tcp", forwarding)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	req := connectRequest(target, header)
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 512)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = conn.Close()
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: br}, nil
	}
	return conn, nil
}

func dialH2(ctx context.Context, forwarding, target string, header http.Header, tlsConfig *tls.Config) (io.ReadWriteCloser, error) {
	config := tlsConfig.Clone()
	config.NextProtos = []string{"h2"}
	pr, pw := io.Pipe()
	tr := &http2.Transport{
		TLSClientConfig: config,
		DialTLSContext: func(ctx context.Context, _, _ string, cfg *tls.Config) (net.Conn, error) {
			return (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", forwarding)
		},
	}
	req := connectRequest(target, header)
	req.Proto = "HTTP/2.0"
	req.Body = pr
	req.ContentLength = -1
	req = req.WithContext(ctx)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = pw.Close()
		_ = pr.Close()
		_ = resp.Body.Close()
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return &h2Stream{reader: resp.Body, writer: pw, transport: tr}, nil
}

func dialH3Connect(ctx context.Context, forwarding, target string, header http.Header, tlsConfig *tls.Config) (io.ReadWriteCloser, error) {
	conn, closeConn, err := dialH3(ctx, forwarding, tlsConfig)
	if err != nil {
		return nil, err
	}
	stream, err := conn.OpenRequestStream(ctx)
	if err != nil {
		closeConn()
		return nil, err
	}
	req := connectRequest(target, header)
	req.Proto = "HTTP/1.1"
	req = req.WithContext(ctx)
	if err := stream.SendRequestHeader(req); err != nil {
		closeConn()
		return nil, err
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		closeConn()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		closeConn()
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return &h3Stream{stream: stream, closeConn: closeConn}, nil
}

func connectRequest(target string, header http.Header) *http.Request {
	h := header.Clone()
	if _, ok := h["User-Agent"]; !ok {
		h.Set("User-Agent", "")
	}
	return &http.Request{
		Method: http.MethodConnect,
		Proto:  "HTTP/1.1",
		Host:   target,
		URL:    &url.URL{Scheme: "https", Host: target},
		Header: h,
	}
}

func runConnectUDP(ctx context.Context, forwarding, baseURL, target string, header http.Header, tlsConfig *tls.Config) error {
	conn, closeConn, err := dialH3(ctx, forwarding, tlsConfig)
	if err != nil {
		return err
	}
	defer closeConn()
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	tpl, err := uritemplate.New(baseURL + "/.well-known/masque/udp/{target_host}/{target_port}/")
	if err != nil {
		return err
	}
	expanded, err := tpl.Expand(uritemplate.Values{
		"target_host": uritemplate.String(host),
		"target_port": uritemplate.String(port),
	})
	if err != nil {
		return err
	}
	u, err := url.Parse(expanded)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.Context().Done():
		return context.Cause(conn.Context())
	case <-conn.ReceivedSettings():
	}
	if !conn.Settings().EnableExtendedConnect || !conn.Settings().EnableDatagrams {
		return errors.New("gateway did not enable Extended CONNECT and HTTP/3 datagrams")
	}
	stream, err := conn.OpenRequestStream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	h := header.Clone()
	h.Set(http3.CapsuleProtocolHeader, "?1")
	if err := stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		Proto:  "connect-udp",
		Host:   u.Host,
		Header: h,
		URL:    u,
	}); err != nil {
		return err
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %s", resp.Status)
	}
	datagram := quicvarint.Append(nil, 0)
	datagram = append(datagram, []byte(payload)...)
	if err := stream.SendDatagram(datagram); err != nil {
		return err
	}
	reply, err := stream.ReceiveDatagram(ctx)
	if err != nil {
		return err
	}
	contextID, n, err := quicvarint.Parse(reply)
	if err != nil {
		return err
	}
	if contextID != 0 {
		return fmt.Errorf("unexpected CONNECT-UDP context ID %d", contextID)
	}
	if !bytes.Equal(reply[n:], []byte(payload)) {
		return fmt.Errorf("UDP echo mismatch: got %q", reply[n:])
	}
	return nil
}

func dialH3(ctx context.Context, forwarding string, tlsConfig *tls.Config) (*http3.ClientConn, func(), error) {
	config := tlsConfig.Clone()
	config.NextProtos = []string{http3.NextProtoH3}
	qconn, err := quic.DialAddr(ctx, forwarding, config, &quic.Config{EnableDatagrams: true})
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = qconn.CloseWithError(0, "probe canceled") })
	h3conn := (&http3.Transport{EnableDatagrams: true}).NewClientConn(qconn)
	closeConn := func() {
		stop()
		_ = h3conn.CloseWithError(0, "")
		_ = qconn.CloseWithError(0, "")
	}
	return h3conn, closeConn, nil
}

func verifyStreamEcho(conn io.ReadWriter) error {
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if !bytes.Equal(buf, []byte(payload)) {
		return fmt.Errorf("TCP echo mismatch: got %q", buf)
	}
	return nil
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

type h2Stream struct {
	reader    io.ReadCloser
	writer    *io.PipeWriter
	transport *http2.Transport
}

func (s *h2Stream) Read(p []byte) (int, error)  { return s.reader.Read(p) }
func (s *h2Stream) Write(p []byte) (int, error) { return s.writer.Write(p) }
func (s *h2Stream) Close() error {
	err1 := s.writer.Close()
	err2 := s.reader.Close()
	s.transport.CloseIdleConnections()
	if err1 != nil {
		return err1
	}
	return err2
}

type h3Stream struct {
	stream    *http3.RequestStream
	closeConn func()
}

func (s *h3Stream) Read(p []byte) (int, error)  { return s.stream.Read(p) }
func (s *h3Stream) Write(p []byte) (int, error) { return s.stream.Write(p) }
func (s *h3Stream) Close() error {
	s.stream.CancelRead(0)
	s.stream.CancelWrite(0)
	_ = s.stream.Close()
	s.closeConn()
	return nil
}
