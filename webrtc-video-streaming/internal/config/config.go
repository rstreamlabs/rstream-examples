package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	goyaml "gopkg.in/yaml.v3"
)

type (
	TunnelProvisioningMode string
	MediaMode              string
	VideoCodec             string
	AdaptiveBackend        string
)

const (
	TunnelProvisioningModeLocal  TunnelProvisioningMode = "local"
	TunnelProvisioningModeRemote TunnelProvisioningMode = "remote"
)

const (
	MediaModeShared    MediaMode = "shared"
	MediaModePerViewer MediaMode = "per-viewer"
)

const (
	VideoCodecUnknown VideoCodec = "unknown"
	VideoCodecH264    VideoCodec = "h264"
	VideoCodecAV1     VideoCodec = "av1"
)

const (
	AdaptiveBackendOff     AdaptiveBackend = "off"
	AdaptiveBackendTWCCGCC AdaptiveBackend = "twcc-gcc"
)

const (
	DefaultServerListen        = "127.0.0.1:8080"
	DefaultTunnelName          = "webrtc-video-streaming"
	DefaultTURNTTL             = "1h"
	DefaultProvisioningTimeout = "10s"
	DefaultReconnect           = "5s"
	DefaultBitrateKbps         = 5000
	MinBitrateKbps             = 1500
	MaxBitrateKbps             = 8000
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Web     WebConfig     `yaml:"web"`
	Tunnel  TunnelConfig  `yaml:"tunnel"`
	TURN    TURNConfig    `yaml:"turn"`
	WebRTC  WebRTCConfig  `yaml:"webrtc"`
	Media   MediaConfig   `yaml:"media"`
	Logging LoggingConfig `yaml:"logging"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type WebConfig struct {
	Viewer WebViewerConfig `yaml:"viewer"`
}

type WebViewerConfig struct {
	Enabled bool `yaml:"enabled"`
}

type TunnelConfig struct {
	Enabled      bool                     `yaml:"enabled"`
	Name         string                   `yaml:"name"`
	Auth         TunnelAuthConfig         `yaml:"auth"`
	Transport    TunnelTransportConfig    `yaml:"transport"`
	Provisioning TunnelProvisioningConfig `yaml:"provisioning"`
	Reconnect    TunnelReconnectConfig    `yaml:"reconnect"`
}

type TunnelAuthConfig struct {
	Token   bool `yaml:"token" json:"token"`
	Rstream bool `yaml:"rstream" json:"rstream"`
}

type TunnelTransportConfig struct {
	UseQUIC bool `yaml:"useQuic"`
}

type TunnelReconnectConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Interval string `yaml:"interval"`
}

type TunnelProvisioningConfig struct {
	Mode     TunnelProvisioningMode `yaml:"mode"`
	Endpoint string                 `yaml:"endpoint"`
	Secret   string                 `yaml:"secret"`
	Timeout  string                 `yaml:"timeout"`
}

type TURNConfig struct {
	TTL string `yaml:"ttl"`
}

type WebRTCConfig struct {
	UseTURN            bool                     `yaml:"useTurn"`
	MaxViewers         int                      `yaml:"maxViewers"`
	InitialBitrateKbps int                      `yaml:"initialBitrateKbps"`
	Video              WebRTCVideoConfig        `yaml:"video"`
	Interceptors       WebRTCInterceptorsConfig `yaml:"interceptors"`
	Adaptive           WebRTCAdaptiveConfig     `yaml:"adaptive"`
}

type WebRTCVideoConfig struct {
	MimeType       string  `yaml:"mimeType"`
	ClockRate      uint32  `yaml:"clockRate"`
	PayloadType    uint8   `yaml:"payloadType"`
	RTXPayloadType uint8   `yaml:"rtxPayloadType"`
	SDPFmtpLine    *string `yaml:"sdpFmtpLine"`
	StreamID       string  `yaml:"streamID"`
	TrackID        string  `yaml:"trackID"`
}

type WebRTCInterceptorsConfig struct {
	TWCC               bool  `yaml:"twcc"`
	NACK               bool  `yaml:"nack"`
	RTX                bool  `yaml:"rtx"`
	FlexFEC            bool  `yaml:"flexFEC"`
	FlexFECPayloadType uint8 `yaml:"flexFECPayloadType"`
}

type WebRTCAdaptiveConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Backend AdaptiveBackend            `yaml:"backend"`
	TWCCGCC WebRTCTWCCGCCBackendConfig `yaml:"twccGCC"`
}

type WebRTCTWCCGCCBackendConfig struct {
	MinBitrateKbps      int    `yaml:"minBitrateKbps"`
	MaxBitrateKbps      int    `yaml:"maxBitrateKbps"`
	UpdateInterval      string `yaml:"updateInterval"`
	ChangeThresholdPct  int    `yaml:"changeThresholdPct"`
	MaxIncreasePct      int    `yaml:"maxIncreasePct"`
	MaxIncreaseStepKbps int    `yaml:"maxIncreaseStepKbps"`
}

type MediaConfig struct {
	Pipeline string    `yaml:"pipeline"`
	SinkName string    `yaml:"sinkName"`
	Mode     MediaMode `yaml:"mode"`
}

type LoggingConfig struct {
	Verbose bool `yaml:"verbose"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Listen: DefaultServerListen,
		},
		Web: WebConfig{
			Viewer: WebViewerConfig{
				Enabled: true,
			},
		},
		Tunnel: TunnelConfig{
			Enabled: true,
			Name:    DefaultTunnelName,
			Transport: TunnelTransportConfig{
				UseQUIC: true,
			},
			Reconnect: TunnelReconnectConfig{
				Enabled:  true,
				Interval: DefaultReconnect,
			},
			Provisioning: TunnelProvisioningConfig{
				Mode:    TunnelProvisioningModeLocal,
				Timeout: DefaultProvisioningTimeout,
			},
		},
		TURN: TURNConfig{
			TTL: DefaultTURNTTL,
		},
		WebRTC: WebRTCConfig{
			UseTURN:            true,
			MaxViewers:         0,
			InitialBitrateKbps: DefaultBitrateKbps,
			Video: WebRTCVideoConfig{
				MimeType:       "video/H264",
				ClockRate:      90000,
				PayloadType:    96,
				RTXPayloadType: 97,
				SDPFmtpLine:    stringPtr("packetization-mode=1;profile-level-id=42e01f;level-asymmetry-allowed=1"),
				StreamID:       "rstream-webrtc-video-streaming",
				TrackID:        "video",
			},
			Interceptors: WebRTCInterceptorsConfig{
				TWCC:               true,
				NACK:               true,
				RTX:                true,
				FlexFEC:            false,
				FlexFECPayloadType: 118,
			},
			Adaptive: WebRTCAdaptiveConfig{
				Enabled: false,
				Backend: AdaptiveBackendTWCCGCC,
				TWCCGCC: WebRTCTWCCGCCBackendConfig{
					MinBitrateKbps:      1500,
					MaxBitrateKbps:      MaxBitrateKbps,
					UpdateInterval:      "1s",
					ChangeThresholdPct:  10,
					MaxIncreasePct:      15,
					MaxIncreaseStepKbps: 500,
				},
			},
		},
		Media: MediaConfig{
			SinkName: "video",
			Mode:     MediaModeShared,
			Pipeline: strings.Join([]string{
				"videotestsrc is-live=true pattern=smpte",
				"videoconvert",
				"video/x-raw,width=1920,height=1080,framerate=30/1",
				"x264enc name=encoder tune=zerolatency speed-preset=veryfast bitrate=5000 key-int-max=60 bframes=0 byte-stream=true aud=true",
				"h264parse config-interval=-1",
				"video/x-h264,stream-format=byte-stream,alignment=au,profile=constrained-baseline",
				"appsink name=video emit-signals=true sync=false max-buffers=4 drop=true",
			}, " ! "),
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	expanded := os.ExpandEnv(string(data))
	decoder := goyaml.NewDecoder(bytes.NewReader([]byte(expanded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid configuration YAML: %w", err)
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.Listen) == "" {
		return errors.New("server listen address is required")
	}
	if strings.TrimSpace(c.Media.Pipeline) == "" {
		return errors.New("media pipeline is required")
	}
	if strings.TrimSpace(c.Media.SinkName) == "" {
		return errors.New("media sink name is required")
	}
	switch c.MediaMode() {
	case MediaModeShared, MediaModePerViewer:
	default:
		return fmt.Errorf("invalid media mode %q", c.Media.Mode)
	}
	if _, err := c.TURNTTL(); err != nil {
		return err
	}
	if _, err := c.TunnelProvisioningTimeout(); err != nil {
		return err
	}
	provisioningMode := c.TunnelProvisioningMode()
	switch provisioningMode {
	case TunnelProvisioningModeLocal:
	case TunnelProvisioningModeRemote:
		if strings.TrimSpace(c.Tunnel.Provisioning.Endpoint) == "" {
			return errors.New("tunnel provisioning endpoint is required when mode is remote")
		}
		parsed, err := url.Parse(strings.TrimSpace(c.Tunnel.Provisioning.Endpoint))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid tunnel provisioning endpoint %q", c.Tunnel.Provisioning.Endpoint)
		}
		if strings.TrimSpace(c.Tunnel.Provisioning.Secret) == "" {
			return errors.New("tunnel provisioning secret is required when mode is remote")
		}
	default:
		return fmt.Errorf("invalid tunnel provisioning mode %q", c.Tunnel.Provisioning.Mode)
	}
	if _, err := c.TunnelReconnectInterval(); err != nil {
		return err
	}
	if provisioningMode == TunnelProvisioningModeRemote && c.HasLocalTunnelAuthPolicy() {
		return errors.New("tunnel auth is only configurable when tunnel provisioning mode is local")
	}
	switch c.VideoCodec() {
	case VideoCodecH264, VideoCodecAV1:
	default:
		return fmt.Errorf("unsupported WebRTC video codec %q", c.WebRTC.Video.MimeType)
	}
	if c.WebRTC.Video.ClockRate == 0 {
		return errors.New("webrtc video clockRate is required")
	}
	if strings.TrimSpace(c.WebRTC.Video.StreamID) == "" {
		return errors.New("webrtc video streamID is required")
	}
	if strings.TrimSpace(c.WebRTC.Video.TrackID) == "" {
		return errors.New("webrtc video trackID is required")
	}
	if c.WebRTC.MaxViewers < 0 {
		return errors.New("webrtc maxViewers must be greater than or equal to 0")
	}
	if c.InitialBitrateKbps() <= 0 {
		return errors.New("webrtc initialBitrateKbps must be greater than 0")
	}
	if c.WebRTC.Interceptors.RTX && !c.WebRTC.Interceptors.NACK {
		return errors.New("webrtc interceptors rtx requires nack to be enabled")
	}
	if c.WebRTC.Interceptors.RTX && c.WebRTC.Video.PayloadType == c.RTXPayloadType() {
		return errors.New("webrtc video payloadType and rtxPayloadType must be different")
	}
	if c.WebRTC.Interceptors.FlexFEC {
		fecPayloadType := c.FlexFECPayloadType()
		if fecPayloadType == c.WebRTC.Video.PayloadType || fecPayloadType == c.RTXPayloadType() {
			return errors.New("webrtc flexFECPayloadType must be different from video payload types")
		}
	}
	pipeline := strings.ToLower(c.Media.Pipeline)
	switch c.AdaptiveBackend() {
	case AdaptiveBackendOff:
	case AdaptiveBackendTWCCGCC:
		if !c.WebRTC.Interceptors.TWCC {
			return errors.New("webrtc adaptive backend twcc-gcc requires twcc to be enabled")
		}
		if c.MediaMode() != MediaModePerViewer && c.WebRTC.MaxViewers != 1 {
			return errors.New("webrtc adaptive backend twcc-gcc requires media mode per-viewer or maxViewers = 1")
		}
		if !strings.Contains(pipeline, "name=encoder") {
			return errors.New("webrtc adaptive backend twcc-gcc requires the media pipeline to expose the encoder as name=encoder")
		}
		if !strings.Contains(pipeline, "x264enc") && !strings.Contains(pipeline, "av1enc") {
			return errors.New("webrtc adaptive backend twcc-gcc requires x264enc or av1enc")
		}
		if _, err := c.AdaptiveUpdateInterval(); err != nil {
			return err
		}
		if c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps < MinBitrateKbps {
			return fmt.Errorf("webrtc adaptive twccGCC minBitrateKbps must be greater than or equal to %d", MinBitrateKbps)
		}
		if c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps > MaxBitrateKbps {
			return fmt.Errorf("webrtc adaptive twccGCC minBitrateKbps must be less than or equal to %d", MaxBitrateKbps)
		}
		if c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps < c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps {
			return errors.New("webrtc adaptive twccGCC maxBitrateKbps must be greater than or equal to minBitrateKbps")
		}
		if c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps > MaxBitrateKbps {
			return fmt.Errorf("webrtc adaptive twccGCC maxBitrateKbps must be less than or equal to %d", MaxBitrateKbps)
		}
		if c.InitialBitrateKbps() < c.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps {
			return errors.New("webrtc initialBitrateKbps must be greater than or equal to webrtc adaptive twccGCC minBitrateKbps")
		}
		if c.InitialBitrateKbps() > c.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps {
			return errors.New("webrtc initialBitrateKbps must be less than or equal to webrtc adaptive twccGCC maxBitrateKbps")
		}
	default:
		return fmt.Errorf("invalid webrtc adaptive backend %q", c.WebRTC.Adaptive.Backend)
	}
	switch c.VideoCodec() {
	case VideoCodecH264:
		if !strings.Contains(pipeline, "h264parse") {
			return errors.New("media pipeline must include h264parse when webrtc video mimeType is video/H264")
		}
	case VideoCodecAV1:
		if !strings.Contains(pipeline, "av1parse") {
			return errors.New("media pipeline must include av1parse when webrtc video mimeType is video/AV1")
		}
	}
	return nil
}

func (c Config) HasLocalTunnelAuthPolicy() bool {
	return c.Tunnel.Auth.Token || c.Tunnel.Auth.Rstream
}

func (c Config) TunnelProvisioningMode() TunnelProvisioningMode {
	value := strings.TrimSpace(string(c.Tunnel.Provisioning.Mode))
	if value == "" {
		return TunnelProvisioningModeLocal
	}
	return TunnelProvisioningMode(value)
}

func (c Config) MediaMode() MediaMode {
	value := strings.TrimSpace(string(c.Media.Mode))
	if value == "" {
		return MediaModeShared
	}
	return MediaMode(value)
}

func (c Config) VideoCodec() VideoCodec {
	switch strings.ToLower(strings.TrimSpace(c.WebRTC.Video.MimeType)) {
	case "video/h264":
		return VideoCodecH264
	case "video/av1":
		return VideoCodecAV1
	default:
		return VideoCodecUnknown
	}
}

func (c Config) AdaptiveBackend() AdaptiveBackend {
	if !c.WebRTC.Adaptive.Enabled {
		return AdaptiveBackendOff
	}
	value := strings.TrimSpace(string(c.WebRTC.Adaptive.Backend))
	if value == "" {
		return AdaptiveBackendTWCCGCC
	}
	return AdaptiveBackend(value)
}

func (c Config) RTXPayloadType() uint8 {
	if c.WebRTC.Video.RTXPayloadType != 0 {
		return c.WebRTC.Video.RTXPayloadType
	}
	switch c.VideoCodec() {
	case VideoCodecAV1:
		return 46
	default:
		return 97
	}
}

func (c Config) FlexFECPayloadType() uint8 {
	if c.WebRTC.Interceptors.FlexFECPayloadType != 0 {
		return c.WebRTC.Interceptors.FlexFECPayloadType
	}
	return 118
}

func (c Config) InitialBitrateKbps() int {
	if c.WebRTC.InitialBitrateKbps > 0 {
		return c.WebRTC.InitialBitrateKbps
	}
	return DefaultBitrateKbps
}

func (c Config) AdaptiveUpdateInterval() (time.Duration, error) {
	value := strings.TrimSpace(c.WebRTC.Adaptive.TWCCGCC.UpdateInterval)
	if value == "" {
		return time.Second, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid webrtc adaptive twccGCC updateInterval %q", c.WebRTC.Adaptive.TWCCGCC.UpdateInterval)
	}
	if dur <= 0 {
		return 0, errors.New("webrtc adaptive twccGCC updateInterval must be greater than 0")
	}
	return dur, nil
}

func (c Config) TURNTTL() (time.Duration, error) {
	value := strings.TrimSpace(c.TURN.TTL)
	if value == "" {
		return time.Hour, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid TURN TTL %q", c.TURN.TTL)
	}
	return dur, nil
}

func (c Config) TunnelProvisioningTimeout() (time.Duration, error) {
	value := strings.TrimSpace(c.Tunnel.Provisioning.Timeout)
	if value == "" {
		return 10 * time.Second, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid tunnel provisioning timeout %q", c.Tunnel.Provisioning.Timeout)
	}
	if dur <= 0 {
		return 0, errors.New("tunnel provisioning timeout must be greater than 0")
	}
	return dur, nil
}

func (c Config) TunnelReconnectInterval() (time.Duration, error) {
	value := strings.TrimSpace(c.Tunnel.Reconnect.Interval)
	if value == "" {
		return 5 * time.Second, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid tunnel reconnect interval %q", c.Tunnel.Reconnect.Interval)
	}
	if dur <= 0 {
		return 0, errors.New("tunnel reconnect interval must be greater than 0")
	}
	return dur, nil
}

func stringPtr(value string) *string {
	return &value
}
