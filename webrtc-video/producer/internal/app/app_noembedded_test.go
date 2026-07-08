//go:build noembeddedweb

package app

import (
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestNewRejectsEnabledViewerWithoutEmbeddedAssets(t *testing.T) {
	cfg := config.Default()
	cfg.Web.Viewer.Enabled = true
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected viewer-enabled config to fail without embedded assets")
	}
	if !strings.Contains(err.Error(), "built without the embedded viewer UI") {
		t.Fatalf("expected embedded viewer error, got %v", err)
	}
}
