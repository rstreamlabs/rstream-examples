package host

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func serveBindings(path string, handler http.Handler) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 100 || strings.ContainsRune(path, 0) {
		return nil, errors.New("invalid bindings socket path")
	}
	// Require a private owner-controlled directory. Never delete a pre-existing
	// socket: another distributor may own it; stale entries need explicit cleanup.
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 || dir.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("bindings socket requires a private directory")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("bindings socket path already exists or is inaccessible")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, WriteTimeout: 2 * time.Second, MaxHeaderBytes: 4096}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if server.Shutdown(ctx) != nil {
			_ = server.Close()
		}
		_ = listener.Close()
		<-done
	}, nil
}
