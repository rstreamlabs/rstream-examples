// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func probeFixture(t *testing.T, protocol string) (string, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway.test"}, DNSNames: []string{"gateway.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	identity := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 405)
			return
		}
		if protocol == "h1" {
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = rw.Flush()
			_, _ = io.Copy(conn, rw)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		buffer := make([]byte, len(payload))
		if _, err := io.ReadFull(r.Body, buffer); err != nil {
			return
		}
		_, _ = w.Write(buffer)
		w.(http.Flusher).Flush()
	})
	if protocol == "h3" {
		packet, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &http3.Server{Handler: handler, TLSConfig: &tls.Config{Certificates: []tls.Certificate{identity}}}
		done := make(chan error, 1)
		go func() { done <- server.Serve(packet) }()
		t.Cleanup(func() { _ = server.Close(); _ = packet.Close(); <-done })
		return packet.LocalAddr().String(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = protocol == "h2"
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server.Listener.Addr().String(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestProbeRejectsUntrustedGateway(t *testing.T) {
	for _, protocol := range []string{"h1", "h2", "h3"} {
		t.Run(protocol, func(t *testing.T) {
			address, _ := probeFixture(t, protocol)
			config, err := newProbeTLSConfig(address, "", "gateway.test", false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := runPlainConnect(ctx, protocol, address, "echo.test:1234", http.Header{}, config); err == nil {
				t.Fatal("probe accepted an untrusted gateway certificate")
			}
		})
	}
}

func TestProbeVerifiesGatewayIdentityAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{"h1", "h2", "h3"} {
		t.Run(protocol, func(t *testing.T) {
			address, ca := probeFixture(t, protocol)
			caFile := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(caFile, ca, 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"gateway.test", "other.test"} {
				config, err := newProbeTLSConfig(address, caFile, name, false)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err = runPlainConnect(ctx, protocol, address, "echo.test:1234", http.Header{}, config)
				cancel()
				if (err == nil) != (name == "gateway.test") {
					t.Fatalf("name %q: %v", name, err)
				}
			}
		})
	}
}

func TestProbeDemoTLSIsExplicitAndLoopbackOnly(t *testing.T) {
	for _, address := range []string{"gateway.example:443", "192.0.2.1:443", "localhost:443"} {
		if _, err := newProbeTLSConfig(address, "", "", true); err == nil {
			t.Fatalf("unbounded demo accepted: %s", address)
		}
	}
	address, _ := probeFixture(t, "h1")
	config, err := newProbeTLSConfig(address, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runPlainConnect(ctx, "h1", address, "echo.test:1234", http.Header{}, config); err != nil {
		t.Fatal(err)
	}
	for _, caFile := range []string{"/missing/ca.pem", filepath.Join(t.TempDir(), "invalid.pem")} {
		if filepath.Base(caFile) == "invalid.pem" {
			if err := os.WriteFile(caFile, []byte("not a certificate"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := newProbeTLSConfig(address, caFile, "", false); err == nil {
			t.Fatal("invalid CA accepted")
		}
	}
}
