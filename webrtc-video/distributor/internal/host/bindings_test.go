package host

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateBindingsSocketLifecycle(t *testing.T) {
	dir := shortTemporaryDirectory(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bindings.sock")
	stop, err := serveBindings(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	if _, err := serveBindings(path, http.NotFoundHandler()); err == nil {
		t.Fatal("overwrote another server's socket")
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr}
	res, err := c.Get("http://distributor/bindings/v1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	stop()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("socket not removed", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := serveBindings(path, http.NotFoundHandler()); err == nil {
		t.Fatal("accepted public parent directory")
	}
}
