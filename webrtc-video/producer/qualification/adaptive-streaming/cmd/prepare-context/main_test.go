package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	producerconfig "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-go/config"
	"gopkg.in/yaml.v3"
)

func TestRunBuildsPrivateQualificationContextFromEnvironment(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "rstream.yaml")
	contextConfig := config.Config{Version: 1, Defaults: config.Defaults{Context: &config.DefaultContext{Name: "source"}}, Contexts: []config.Context{{Name: "source", APIURL: "https://rstream.io", ProjectEndpoint: "project-endpoint", Engine: "project-endpoint.example:443"}}}
	if err := config.WriteAtomic(configPath, contextConfig); err != nil {
		t.Fatalf("write rstream config: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "qualification-token")
	sourcePath := filepath.Join(directory, "producer.yaml")
	producerData, err := yaml.Marshal(producerconfig.Default())
	if err != nil {
		t.Fatalf("marshal producer config: %v", err)
	}
	if err := os.WriteFile(sourcePath, producerData, 0o600); err != nil {
		t.Fatalf("write producer config: %v", err)
	}
	outputDirectory := filepath.Join(directory, "runtime")
	if err := run("source", outputDirectory, sourcePath, "relay", "udp", flexFECConfig{enabled: true, mediaPackets: 5, repairPackets: 1}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	runtimeEnvironment, err := os.ReadFile(filepath.Join(outputDirectory, "runtime.env"))
	if err != nil {
		t.Fatalf("read runtime environment: %v", err)
	}
	if string(runtimeEnvironment) != "RSTREAM_AUTHENTICATION_TOKEN=qualification-token\n" {
		t.Fatalf("runtime environment = %q", runtimeEnvironment)
	}
	runtimeConfig, err := config.Load(filepath.Join(outputDirectory, "config.yaml"))
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if len(runtimeConfig.Contexts) != 1 || runtimeConfig.Contexts[0].Name != "qualification" || runtimeConfig.Contexts[0].ProjectEndpoint != "project-endpoint" || runtimeConfig.Contexts[0].Auth != nil {
		t.Fatalf("runtime config retained credentials or lost project metadata: %#v", runtimeConfig)
	}
	for _, name := range []string{"config.yaml", "runtime.env", "relay-config.yaml", "direct-config.yaml"} {
		info, err := os.Stat(filepath.Join(outputDirectory, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %o, want private", name, info.Mode().Perm())
		}
	}
}

func TestRunRejectsMissingRequiredInputs(t *testing.T) {
	for _, test := range []struct {
		name    string
		context string
		output  string
		want    string
	}{
		{name: "context", output: t.TempDir(), want: "context is required"},
		{name: "output", context: "source", want: "output directory is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.context, test.output, "unused", "disabled", "", flexFECConfig{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunDoesNotExposeSelectedContextNameInErrors(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "rstream.yaml")
	if err := config.WriteAtomic(configPath, config.Config{Version: 1}); err != nil {
		t.Fatalf("write rstream config: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	contextName := "customer-production-eu-west"
	err := run(
		contextName,
		filepath.Join(directory, "runtime"),
		"unused",
		"disabled",
		"",
		flexFECConfig{},
	)
	if err == nil {
		t.Fatal("expected context resolution to fail")
	}
	if strings.Contains(err.Error(), contextName) {
		t.Fatalf("context resolution error exposed the selected context name: %v", err)
	}
}

func TestValidateFlexFECConfigRejectsInvalidProtection(t *testing.T) {
	for _, test := range []struct {
		name       string
		protection flexFECConfig
		want       string
	}{
		{name: "zero media group", protection: flexFECConfig{enabled: true, repairPackets: 1}, want: "media-packet count"},
		{name: "oversized media group", protection: flexFECConfig{enabled: true, mediaPackets: producerconfig.MaxFlexFECPackets + 1, repairPackets: 1}, want: "media-packet count"},
		{name: "zero repair group", protection: flexFECConfig{enabled: true, mediaPackets: 5}, want: "repair-packet count"},
		{name: "repair group exceeds media group", protection: flexFECConfig{enabled: true, mediaPackets: 5, repairPackets: 6}, want: "repair-packet count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateFlexFECConfig(test.protection)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFlexFECConfig() error = %v, want %q", err, test.want)
			}
		})
	}
	if err := validateFlexFECConfig(flexFECConfig{}); err != nil {
		t.Fatalf("disabled FlexFEC validation error = %v", err)
	}
}

func TestWritePrivateCreatesExclusivePrivateFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "runtime.env")
	data := []byte("RSTREAM_AUTHENTICATION_TOKEN=secret\n")
	if err := writePrivate(path, data); err != nil {
		t.Fatalf("write private file: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read private file: %v", err)
	}
	if string(written) != string(data) {
		t.Fatalf("unexpected private file contents: %q", written)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat private file: %v", err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("private file permissions = %o, want 600", permissions)
		}
	}
	if err := writePrivate(path, []byte("replacement")); err == nil {
		t.Fatal("expected an existing private file to be rejected")
	}
	written, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved private file: %v", err)
	}
	if string(written) != string(data) {
		t.Fatal("exclusive write changed the existing private file")
	}
}

func TestWriteProducerConfigsPreservesMediaAndBuildsIsolatedReference(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.yaml")
	cfg := producerconfig.Default()
	cfg.WebRTC.InitialBitrateKbps = 4321
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal source config: %v", err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}
	protection := flexFECConfig{enabled: true, mediaPackets: 5, repairPackets: 2}
	if err := writeProducerConfigs(source, directory, "relay", "tls", protection); err != nil {
		t.Fatalf("write producer configs: %v", err)
	}
	relay, err := producerconfig.Load(filepath.Join(directory, "relay-config.yaml"))
	if err != nil {
		t.Fatalf("load relay config: %v", err)
	}
	if !relay.Tunnel.Enabled || !relay.WebRTC.UseTURN || !relay.WebRTC.Interceptors.FlexFEC {
		t.Fatal("relay profile did not retain rstream transport or enable requested FlexFEC")
	}
	if relay.FlexFECMediaPackets() != 5 || relay.FlexFECRepairPackets() != 2 {
		t.Fatalf("relay FlexFEC protection = %d/%d, want 2 repairs per 5 media packets", relay.FlexFECRepairPackets(), relay.FlexFECMediaPackets())
	}
	if relay.ICETransportPolicy() != producerconfig.ICETransportPolicyRelay {
		t.Fatalf("relay ICE transport policy = %q, want relay", relay.ICETransportPolicy())
	}
	if len(relay.TURN.Transports) != 1 || relay.TURN.Transports[0] != "tls" {
		t.Fatalf("relay TURN transports = %v, want [tls]", relay.TURN.Transports)
	}
	direct, err := producerconfig.Load(filepath.Join(directory, "direct-config.yaml"))
	if err != nil {
		t.Fatalf("load direct config: %v", err)
	}
	if direct.Server.Listen != "0.0.0.0:8080" {
		t.Fatalf("direct listen = %q", direct.Server.Listen)
	}
	if direct.Tunnel.Enabled || direct.Tunnel.Reconnect.Enabled || direct.WebRTC.UseTURN {
		t.Fatal("direct reference retained an external tunnel or TURN dependency")
	}
	if !direct.WebRTC.Interceptors.FlexFEC {
		t.Fatal("direct reference changed the requested protection profile")
	}
	if direct.FlexFECMediaPackets() != 5 || direct.FlexFECRepairPackets() != 2 {
		t.Fatalf("direct FlexFEC protection = %d/%d, want 2 repairs per 5 media packets", direct.FlexFECRepairPackets(), direct.FlexFECMediaPackets())
	}
	if direct.Media.Pipeline != cfg.Media.Pipeline {
		t.Fatal("direct reference changed the media pipeline")
	}
	if direct.WebRTC.InitialBitrateKbps != cfg.WebRTC.InitialBitrateKbps {
		t.Fatal("direct reference changed the initial bitrate")
	}
}

func TestWriteProducerConfigsRejectsInvalidTURNTransport(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.yaml")
	data, err := yaml.Marshal(producerconfig.Default())
	if err != nil {
		t.Fatalf("marshal source config: %v", err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}
	if err := writeProducerConfigs(source, directory, "auto", "quic", flexFECConfig{}); err == nil {
		t.Fatal("expected unsupported TURN transport to fail")
	}
}

func TestWriteProducerConfigsCanDisableProducerTURN(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.yaml")
	data, err := yaml.Marshal(producerconfig.Default())
	if err != nil {
		t.Fatalf("marshal source config: %v", err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}
	if err := writeProducerConfigs(source, directory, "disabled", "", flexFECConfig{}); err != nil {
		t.Fatalf("write producer configs: %v", err)
	}
	relay, err := producerconfig.Load(filepath.Join(directory, "relay-config.yaml"))
	if err != nil {
		t.Fatalf("load relay config: %v", err)
	}
	if relay.WebRTC.UseTURN {
		t.Fatal("producer TURN remained enabled")
	}
	if relay.ICETransportPolicy() != producerconfig.ICETransportPolicyAll {
		t.Fatalf("disabled producer TURN policy = %q, want all", relay.ICETransportPolicy())
	}
}

func TestWriteProducerConfigsRejectsInvalidProducerTURNPolicy(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.yaml")
	data, err := yaml.Marshal(producerconfig.Default())
	if err != nil {
		t.Fatalf("marshal source config: %v", err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}
	if err := writeProducerConfigs(source, directory, "sometimes", "", flexFECConfig{}); err == nil {
		t.Fatal("expected unsupported producer TURN policy to fail")
	}
}
