package app

import (
	"context"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func TestLocalTunnelLabelsAreCopiedForEachConnection(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Labels = map[string]string{"device-name": "Front camera"}
	app := &App{cfg: cfg}
	first, err := app.resolveTunnelOpenOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.Labels["device-name"] = "Changed"
	second, err := app.resolveTunnelOpenOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Labels["device-name"] != "Front camera" || second.Provisioned {
		t.Fatal("local tunnel options must copy configured labels without entering remote provisioning")
	}
}
