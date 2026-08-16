package config

import (
	"bytes"
	"errors"
	"fmt"
	"math"
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
	ICETransportPolicy     string
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
	ICETransportPolicyAll   ICETransportPolicy = "all"
	ICETransportPolicyRelay ICETransportPolicy = "relay"
)

const (
	DefaultServerListen          = "127.0.0.1:8080"
	DefaultTunnelName            = "webrtc-video-producer"
	DefaultTURNTTL               = "10m"
	DefaultProvisioningTimeout   = "10s"
	DefaultReconnect             = "5s"
	DefaultBitrateKbps           = 5000
	MinBitrateKbps               = 500
	MaxBitrateKbps               = 8000
	RealTimePacingFactor         = 1.5
	DefaultFlexFECMediaPackets   = 5
	DefaultFlexFECRepairPackets  = 1
	MaxFlexFECPackets            = 110
	DefaultIncreaseHoldAfterLoss = "5s"
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
	Mode    string `yaml:"mode"`
	UseQUIC *bool  `yaml:"useQuic,omitempty"`
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
	TTL        string   `yaml:"ttl"`
	Transports []string `yaml:"transports"`
}

type WebRTCConfig struct {
	UseTURN            bool                     `yaml:"useTurn"`
	ICETransportPolicy ICETransportPolicy       `yaml:"iceTransportPolicy"`
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
	TWCC                 bool   `yaml:"twcc"`
	NACK                 bool   `yaml:"nack"`
	RTX                  bool   `yaml:"rtx"`
	FlexFEC              bool   `yaml:"flexFEC"`
	FlexFECPayloadType   uint8  `yaml:"flexFECPayloadType"`
	FlexFECMediaPackets  uint32 `yaml:"flexFECMediaPackets"`
	FlexFECRepairPackets uint32 `yaml:"flexFECRepairPackets"`
}

type WebRTCAdaptiveConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Backend AdaptiveBackend            `yaml:"backend"`
	TWCCGCC WebRTCTWCCGCCBackendConfig `yaml:"twccGCC"`
}

type WebRTCTWCCGCCBackendConfig struct {
	MinBitrateKbps        int     `yaml:"minBitrateKbps"`
	MaxBitrateKbps        int     `yaml:"maxBitrateKbps"`
	UpdateInterval        string  `yaml:"updateInterval"`
	ChangeThresholdPct    int     `yaml:"changeThresholdPct"`
	DecreaseThresholdPct  int     `yaml:"decreaseThresholdPct"`
	MaxIncreasePct        int     `yaml:"maxIncreasePct"`
	MaxIncreaseStepKbps   int     `yaml:"maxIncreaseStepKbps"`
	MaxIncreaseLossPct    float64 `yaml:"maxIncreaseLossPct"`
	IncreaseHoldAfterLoss string  `yaml:"increaseHoldAfterLoss"`
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
			Enabled:   true,
			Name:      DefaultTunnelName,
			Transport: TunnelTransportConfig{},
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
			ICETransportPolicy: ICETransportPolicyAll,
			MaxViewers:         0,
			InitialBitrateKbps: DefaultBitrateKbps,
			Video: WebRTCVideoConfig{
				MimeType:       "video/H264",
				ClockRate:      90000,
				PayloadType:    96,
				RTXPayloadType: 97,
				SDPFmtpLine:    stringPtr("packetization-mode=1;profile-level-id=42e01f;level-asymmetry-allowed=1"),
				StreamID:       "rstream-webrtc-video-producer",
				TrackID:        "video",
			},
			Interceptors: WebRTCInterceptorsConfig{
				TWCC:                 true,
				NACK:                 true,
				RTX:                  true,
				FlexFEC:              false,
				FlexFECPayloadType:   118,
				FlexFECMediaPackets:  DefaultFlexFECMediaPackets,
				FlexFECRepairPackets: DefaultFlexFECRepairPackets,
			},
			Adaptive: WebRTCAdaptiveConfig{
				Enabled: false,
				Backend: AdaptiveBackendTWCCGCC,
				TWCCGCC: WebRTCTWCCGCCBackendConfig{
					MinBitrateKbps:        2000,
					MaxBitrateKbps:        MaxBitrateKbps,
					UpdateInterval:        "1s",
					ChangeThresholdPct:    10,
					DecreaseThresholdPct:  5,
					MaxIncreasePct:        15,
					MaxIncreaseStepKbps:   500,
					MaxIncreaseLossPct:    1,
					IncreaseHoldAfterLoss: DefaultIncreaseHoldAfterLoss,
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
	if _, err := c.TURNTransports(); err != nil {
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
	switch c.TunnelTransportMode() {
	case "auto", "tls", "quic":
	default:
		return fmt.Errorf("invalid tunnel transport mode %q", c.Tunnel.Transport.Mode)
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
	switch c.ICETransportPolicy() {
	case ICETransportPolicyAll:
	case ICETransportPolicyRelay:
		if !c.WebRTC.UseTURN {
			return errors.New("webrtc iceTransportPolicy relay requires useTurn to be enabled")
		}
	default:
		return fmt.Errorf("invalid webrtc iceTransportPolicy %q", c.WebRTC.ICETransportPolicy)
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
		mediaPackets := c.FlexFECMediaPackets()
		repairPackets := c.FlexFECRepairPackets()
		if mediaPackets > MaxFlexFECPackets {
			return fmt.Errorf("webrtc flexFECMediaPackets must be at most %d", MaxFlexFECPackets)
		}
		if repairPackets > MaxFlexFECPackets {
			return fmt.Errorf("webrtc flexFECRepairPackets must be at most %d", MaxFlexFECPackets)
		}
		if repairPackets > mediaPackets {
			return errors.New("webrtc flexFECRepairPackets must be less than or equal to flexFECMediaPackets")
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
		if c.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct < 0 ||
			c.WebRTC.Adaptive.TWCCGCC.ChangeThresholdPct > 100 {
			return errors.New("webrtc adaptive twccGCC changeThresholdPct must be between 0 and 100")
		}
		if c.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct < 0 ||
			c.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct > 100 {
			return errors.New("webrtc adaptive twccGCC decreaseThresholdPct must be between 0 and 100")
		}
		maximumDecreaseThreshold := c.MaxSafeAdaptiveDecreaseThresholdPct()
		if c.WebRTC.Adaptive.TWCCGCC.DecreaseThresholdPct > maximumDecreaseThreshold {
			return fmt.Errorf(
				"webrtc adaptive twccGCC decreaseThresholdPct must be at most %d with the configured pacing and FlexFEC ratio",
				maximumDecreaseThreshold,
			)
		}
		if c.WebRTC.Adaptive.TWCCGCC.MaxIncreasePct <= 0 ||
			c.WebRTC.Adaptive.TWCCGCC.MaxIncreasePct > 100 {
			return errors.New("webrtc adaptive twccGCC maxIncreasePct must be greater than 0 and at most 100")
		}
		if c.WebRTC.Adaptive.TWCCGCC.MaxIncreaseStepKbps <= 0 {
			return errors.New("webrtc adaptive twccGCC maxIncreaseStepKbps must be greater than 0")
		}
		if math.IsNaN(c.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct) ||
			math.IsInf(c.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct, 0) ||
			c.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct < 0 ||
			c.WebRTC.Adaptive.TWCCGCC.MaxIncreaseLossPct > 100 {
			return errors.New("webrtc adaptive twccGCC maxIncreaseLossPct must be between 0 and 100")
		}
		if _, err := c.AdaptiveIncreaseHoldAfterLoss(); err != nil {
			return err
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

func (c Config) TunnelTransportMode() string {
	if mode := strings.ToLower(strings.TrimSpace(c.Tunnel.Transport.Mode)); mode != "" {
		return mode
	}
	if c.Tunnel.Transport.UseQUIC != nil {
		if *c.Tunnel.Transport.UseQUIC {
			return "quic"
		}
		return "tls"
	}
	return "auto"
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

func (c Config) ICETransportPolicy() ICETransportPolicy {
	value := strings.ToLower(strings.TrimSpace(string(c.WebRTC.ICETransportPolicy)))
	if value == "" {
		return ICETransportPolicyAll
	}
	return ICETransportPolicy(value)
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

func (c Config) FlexFECMediaPackets() uint32 {
	if c.WebRTC.Interceptors.FlexFECMediaPackets != 0 {
		return c.WebRTC.Interceptors.FlexFECMediaPackets
	}
	return DefaultFlexFECMediaPackets
}

func (c Config) FlexFECRepairPackets() uint32 {
	if c.WebRTC.Interceptors.FlexFECRepairPackets != 0 {
		return c.WebRTC.Interceptors.FlexFECRepairPackets
	}
	return DefaultFlexFECRepairPackets
}

func (c Config) MaxSafeAdaptiveDecreaseThresholdPct() int {
	protectedWireFactor := 1.0
	if c.WebRTC.Interceptors.FlexFEC {
		mediaPackets := float64(c.FlexFECMediaPackets())
		repairPackets := float64(c.FlexFECRepairPackets())
		if mediaPackets > 0 && repairPackets > 0 {
			protectedWireFactor = (mediaPackets + repairPackets) / mediaPackets
		}
	}
	pacingEnvelopeFactor := math.Max(RealTimePacingFactor, protectedWireFactor)
	availableFraction := 1 - protectedWireFactor/pacingEnvelopeFactor
	if availableFraction <= 0 {
		return 0
	}
	return int(math.Floor(availableFraction * 100))
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

func (c Config) AdaptiveIncreaseHoldAfterLoss() (time.Duration, error) {
	value := strings.TrimSpace(c.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss)
	if value == "" {
		value = DefaultIncreaseHoldAfterLoss
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid webrtc adaptive twccGCC increaseHoldAfterLoss %q", c.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss)
	}
	if dur < 0 {
		return 0, errors.New("webrtc adaptive twccGCC increaseHoldAfterLoss must not be negative")
	}
	return dur, nil
}

func (c Config) TURNTTL() (time.Duration, error) {
	value := strings.TrimSpace(c.TURN.TTL)
	if value == "" {
		return time.ParseDuration(DefaultTURNTTL)
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid TURN TTL %q", c.TURN.TTL)
	}
	return dur, nil
}

func (c Config) TURNTransports() ([]string, error) {
	if len(c.TURN.Transports) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(c.TURN.Transports))
	transports := make([]string, 0, len(c.TURN.Transports))
	for _, raw := range c.TURN.Transports {
		transport := strings.ToLower(strings.TrimSpace(raw))
		switch transport {
		case "udp", "tcp", "dtls", "tls":
		default:
			return nil, fmt.Errorf("invalid TURN transport %q", raw)
		}
		if _, ok := seen[transport]; ok {
			continue
		}
		seen[transport] = struct{}{}
		transports = append(transports, transport)
	}
	return transports, nil
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
