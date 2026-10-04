// local-control is a qualification-only proxy in the producer's Docker network
// namespace. Its port is never published to the host. It exposes WHEP and
// read-only observations; the production local quality listener stays loopback.
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func controlHandler(target *url.URL, transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "qualification upstream unavailable", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		readOnly := request.Method == http.MethodGet && (path == "/healthz" || path == "/api/quality" || path == "/api/status" || strings.HasPrefix(path, "/api/diagnostics/sessions/"))
		whep := (path == "/whep" || strings.HasPrefix(path, "/whep/")) && (request.Method == http.MethodPost || request.Method == http.MethodPatch || request.Method == http.MethodDelete || request.Method == http.MethodOptions)
		if !readOnly && !whep {
			http.NotFound(w, request)
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, 128*1024)
		proxy.ServeHTTP(w, request)
	})
}

func main() {
	target, _ := url.Parse("http://127.0.0.1:8080")
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: time.Second}).DialContext,
		MaxConnsPerHost: 16, MaxIdleConnsPerHost: 4,
		ResponseHeaderTimeout: 8 * time.Second, IdleConnTimeout: 5 * time.Second,
		DisableCompression: true,
	}
	defer transport.CloseIdleConnections()
	server := &http.Server{
		Addr: "0.0.0.0:18080", Handler: controlHandler(target, transport),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 * 1024,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan error, 1)
	go func() { finished <- server.ListenAndServe() }()
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		<-finished
	}
}
