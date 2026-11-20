// See LICENSE file in the project root for license information.

package egress

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/authz"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/metrics"
	"github.com/rstreamlabs/rstream-examples/private-masque-egress-gateway/internal/tlsutil"
	"github.com/yosida95/uritemplate/v3"
)

const testPayload = "private-egress-test"

func TestServerPlainConnectOverH3(t *testing.T) {
	tcpTarget := startTCPEcho(t)
	h3addr := startTestGateway(t)
	conn, cleanup := dialTestH3(t, h3addr)
	defer cleanup()
	stream, err := conn.OpenRequestStream(t.Context())
	if err != nil {
		t.Fatalf("OpenRequestStream: %v", err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  "HTTP/1.1",
		Host:   tcpTarget,
		URL:    &url.URL{Scheme: "https", Host: tcpTarget},
		Header: http.Header{"X-Egress-Token": []string{"secret"}},
	}
	if err := stream.SendRequestHeader(req); err != nil {
		t.Fatalf("SendRequestHeader: %v", err)
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if _, err := stream.Write([]byte(testPayload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	buf := make([]byte, len(testPayload))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(buf, []byte(testPayload)) {
		t.Fatalf("echo = %q", buf)
	}
}

func TestServerConnectUDPOverH3(t *testing.T) {
	udpTarget := startUDPEcho(t)
	h3addr := startTestGateway(t)
	conn, cleanup := dialTestH3(t, h3addr)
	defer cleanup()
	select {
	case <-conn.ReceivedSettings():
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	stream, err := conn.OpenRequestStream(t.Context())
	if err != nil {
		t.Fatalf("OpenRequestStream: %v", err)
	}
	host, port, err := net.SplitHostPort(udpTarget)
	if err != nil {
		t.Fatal(err)
	}
	tpl := uritemplate.MustNew("https://" + h3addr + "/.well-known/masque/udp/{target_host}/{target_port}/")
	expanded, err := tpl.Expand(uritemplate.Values{
		"target_host": uritemplate.String(host),
		"target_port": uritemplate.String(port),
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(expanded)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		Proto:  "connect-udp",
		Host:   u.Host,
		URL:    u,
		Header: http.Header{
			http3.CapsuleProtocolHeader: []string{"?1"},
			"X-Egress-Token":            []string{"secret"},
		},
	}); err != nil {
		t.Fatalf("SendRequestHeader: %v", err)
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	dgram := quicvarint.Append(nil, 0)
	dgram = append(dgram, []byte(testPayload)...)
	if err := stream.SendDatagram(dgram); err != nil {
		t.Fatalf("SendDatagram: %v", err)
	}
	got, err := stream.ReceiveDatagram(t.Context())
	if err != nil {
		t.Fatalf("ReceiveDatagram: %v", err)
	}
	contextID, n, err := quicvarint.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if contextID != 0 || !bytes.Equal(got[n:], []byte(testPayload)) {
		t.Fatalf("datagram = context %d payload %q", contextID, got[n:])
	}
}

func TestServerRejectsMissingAccessToken(t *testing.T) {
	tcpTarget := startTCPEcho(t)
	h3addr := startTestGateway(t)
	conn, cleanup := dialTestH3(t, h3addr)
	defer cleanup()
	stream, err := conn.OpenRequestStream(t.Context())
	if err != nil {
		t.Fatalf("OpenRequestStream: %v", err)
	}
	if err := stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		Proto:  "HTTP/1.1",
		Host:   tcpTarget,
		URL:    &url.URL{Scheme: "https", Host: tcpTarget},
	}); err != nil {
		t.Fatalf("SendRequestHeader: %v", err)
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %s", resp.Status)
	}
}

func startTestGateway(t *testing.T) string {
	t.Helper()
	policy := DefaultPolicy()
	policy.AllowLoopback = true
	counterSet := metrics.NewCounterSet()
	server := NewServer(policy, authz.New("X-Egress-Token", "secret"), counterSet, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tlsConfig, err := tlsutil.SelfSignedH3Config()
	if err != nil {
		t.Fatalf("TLS config: %v", err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	h3 := &http3.Server{Handler: server, TLSConfig: tlsConfig, EnableDatagrams: true}
	errCh := make(chan error, 1)
	go func() { errCh <- h3.Serve(pc) }()
	t.Cleanup(func() {
		_ = h3.Close()
		_ = pc.Close()
		_ = server.Close()
		select {
		case <-errCh:
		default:
		}
	})
	return pc.LocalAddr().String()
}

func dialTestH3(t *testing.T, addr string) (*http3.ClientConn, func()) {
	t.Helper()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	qconn, err := quic.DialAddr(t.Context(), addr, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{http3.NextProtoH3},
		ServerName:         host,
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatalf("dial H3: %v", err)
	}
	conn := (&http3.Transport{EnableDatagrams: true}).NewClientConn(qconn)
	cleanup := func() {
		_ = conn.CloseWithError(0, "")
		_ = qconn.CloseWithError(0, "")
	}
	return conn, cleanup
}

func startTCPEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

func startUDPEcho(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		_ = conn.Close()
	})
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			_, _ = conn.WriteTo(buf[:n], addr)
		}
	}()
	return conn.LocalAddr().String()
}
