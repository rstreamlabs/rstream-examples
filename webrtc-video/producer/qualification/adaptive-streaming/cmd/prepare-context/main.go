package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	producerconfig "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-go/config"
	"gopkg.in/yaml.v3"
)

func main() {
	contextName := flag.String("context", "", "rstream CLI context to resolve")
	flexFEC := flag.Bool("flex-fec", false, "enable FlexFEC in the qualification producer profiles")
	flexFECMediaPackets := flag.Uint("flex-fec-media-packets", producerconfig.DefaultFlexFECMediaPackets, "media packets in each FlexFEC protection group")
	flexFECRepairPackets := flag.Uint("flex-fec-repair-packets", producerconfig.DefaultFlexFECRepairPackets, "repair packets generated for each FlexFEC protection group")
	outputDirectory := flag.String("output-directory", "", "private runtime directory")
	producerConfig := flag.String("producer-config", "", "producer configuration used to derive the direct reference")
	producerTURNPolicy := flag.String("producer-turn-policy", "disabled", "producer TURN policy: disabled, auto, or relay")
	turnTransport := flag.String("turn-transport", "", "optional TURN transport filter")
	flag.Parse()
	protection := flexFECConfig{
		enabled:       *flexFEC,
		mediaPackets:  *flexFECMediaPackets,
		repairPackets: *flexFECRepairPackets,
	}
	if err := run(*contextName, *outputDirectory, *producerConfig, *producerTURNPolicy, *turnTransport, protection); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type flexFECConfig struct {
	enabled       bool
	mediaPackets  uint
	repairPackets uint
}

func run(contextName, outputDirectory, producerConfigPath, producerTURNPolicy, turnTransport string, flexFEC flexFECConfig) error {
	contextName = strings.TrimSpace(contextName)
	outputDirectory = strings.TrimSpace(outputDirectory)
	if contextName == "" {
		return errors.New("context is required")
	}
	if outputDirectory == "" {
		return errors.New("output directory is required")
	}
	if err := validateFlexFECConfig(flexFEC); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		return fmt.Errorf("create private runtime directory: %w", err)
	}
	if err := os.Chmod(outputDirectory, 0o700); err != nil {
		return fmt.Errorf("secure private runtime directory: %w", err)
	}
	environmentSettings := config.ReadEnv()
	configPath := environmentSettings.ConfigPath
	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve rstream config path: %w", err)
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load rstream config: %w", err)
	}
	resolved, err := config.Resolve(config.ResolveInput{
		Config:                 cfg,
		FlagContext:            contextName,
		EnvAPIURL:              environmentSettings.APIURL,
		EnvContext:             environmentSettings.Context,
		EnvEngine:              environmentSettings.Engine,
		EnvToken:               environmentSettings.Token,
		EnvMTLSCert:            environmentSettings.MTLSCert,
		EnvMTLSKey:             environmentSettings.MTLSKey,
		EnvRegion:              environmentSettings.Region,
		EnvTunnelTransport:     environmentSettings.TunnelTransport,
		EnvUseQUIC:             environmentSettings.UseQUIC,
		EnvControlPlaneHeaders: environmentSettings.ControlPlaneHeaders,
		RequireToken:           true,
		ResolveToken:           true,
	})
	if err != nil {
		return errors.New("resolve selected rstream context: verify that the context exists and has valid project authentication")
	}
	if resolved.Context == nil {
		return errors.New("selected rstream context has no project metadata")
	}
	if strings.TrimSpace(resolved.Context.ProjectEndpoint) == "" {
		return errors.New("selected rstream context has no project endpoint")
	}
	if strings.TrimSpace(resolved.Token) == "" {
		return errors.New("selected rstream context has no authentication token")
	}
	if strings.ContainsAny(resolved.Token, "\r\n\x00") {
		return errors.New("selected rstream context returned an invalid authentication token")
	}
	runtimeContext := *resolved.Context
	runtimeContext.Name = "qualification"
	runtimeContext.APIURL = resolved.APIURL
	runtimeContext.Engine = resolved.Engine
	runtimeContext.Auth = nil
	runtimeContext.Transport = resolved.TransportConfig
	runtimeConfig := config.Config{
		Version: 1,
		Defaults: config.Defaults{
			Context: &config.DefaultContext{Name: runtimeContext.Name},
		},
		Contexts: []config.Context{runtimeContext},
	}
	if len(resolved.ControlPlaneHeaders) > 0 || resolved.Environment != nil {
		runtimeConfig.Environments = []config.Environment{{
			APIURL:  resolved.APIURL,
			Headers: resolved.ControlPlaneHeaders,
		}}
	}
	if err := config.WriteAtomic(filepath.Join(outputDirectory, "config.yaml"), runtimeConfig); err != nil {
		return fmt.Errorf("write credential-free runtime config: %w", err)
	}
	environment := "RSTREAM_AUTHENTICATION_TOKEN=" + resolved.Token + "\n"
	if err := writePrivate(filepath.Join(outputDirectory, "runtime.env"), []byte(environment)); err != nil {
		return fmt.Errorf("write private runtime environment: %w", err)
	}
	if err := writeProducerConfigs(producerConfigPath, outputDirectory, producerTURNPolicy, turnTransport, flexFEC); err != nil {
		return err
	}
	return nil
}

func writeProducerConfigs(sourcePath, outputDirectory, producerTURNPolicy, turnTransport string, flexFEC flexFECConfig) error {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return errors.New("producer config is required")
	}
	cfg, err := producerconfig.Load(sourcePath)
	if err != nil {
		return fmt.Errorf("load producer config: %w", err)
	}
	cfg.WebRTC.Interceptors.FlexFEC = flexFEC.enabled
	cfg.WebRTC.Interceptors.FlexFECMediaPackets = uint32(flexFEC.mediaPackets)
	cfg.WebRTC.Interceptors.FlexFECRepairPackets = uint32(flexFEC.repairPackets)
	switch producerTURNPolicy = strings.ToLower(strings.TrimSpace(producerTURNPolicy)); producerTURNPolicy {
	case "disabled":
		cfg.WebRTC.UseTURN = false
		cfg.WebRTC.ICETransportPolicy = producerconfig.ICETransportPolicyAll
	case "auto":
		cfg.WebRTC.UseTURN = true
		cfg.WebRTC.ICETransportPolicy = producerconfig.ICETransportPolicyAll
	case "relay":
		cfg.WebRTC.UseTURN = true
		cfg.WebRTC.ICETransportPolicy = producerconfig.ICETransportPolicyRelay
	default:
		return fmt.Errorf("invalid producer TURN policy %q", producerTURNPolicy)
	}
	if turnTransport = strings.TrimSpace(turnTransport); turnTransport != "" {
		cfg.TURN.Transports = []string{turnTransport}
	}
	if _, err := cfg.TURNTransports(); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate qualification producer config: %w", err)
	}
	relayData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode relay producer config: %w", err)
	}
	if err := writePrivate(filepath.Join(outputDirectory, "relay-config.yaml"), relayData); err != nil {
		return fmt.Errorf("write relay producer config: %w", err)
	}
	direct := cfg
	direct.Server.Listen = "0.0.0.0:8080"
	direct.Tunnel.Enabled = false
	direct.Tunnel.Reconnect.Enabled = false
	direct.WebRTC.UseTURN = false
	direct.WebRTC.ICETransportPolicy = producerconfig.ICETransportPolicyAll
	directData, err := yaml.Marshal(direct)
	if err != nil {
		return fmt.Errorf("encode direct producer config: %w", err)
	}
	if err := writePrivate(filepath.Join(outputDirectory, "direct-config.yaml"), directData); err != nil {
		return fmt.Errorf("write direct producer config: %w", err)
	}
	return nil
}

func validateFlexFECConfig(protection flexFECConfig) error {
	if !protection.enabled {
		return nil
	}
	if protection.mediaPackets == 0 || protection.mediaPackets > producerconfig.MaxFlexFECPackets {
		return fmt.Errorf("FlexFEC media-packet count must be from 1 through %d", producerconfig.MaxFlexFECPackets)
	}
	if protection.repairPackets == 0 || protection.repairPackets > protection.mediaPackets {
		return errors.New("FlexFEC repair-packet count must be from 1 through the media-packet group size")
	}
	return nil
}

func writePrivate(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
