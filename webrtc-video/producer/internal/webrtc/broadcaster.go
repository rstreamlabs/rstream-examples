package webrtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	rtcmedia "github.com/pion/webrtc/v4/pkg/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/adaptation"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
	turnprovider "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/turn"
	"github.com/rstreamlabs/rstream-go"
)

type SessionStats struct {
	Codec                     string                 `json:"codec"`
	TWCCEnabled               bool                   `json:"twccEnabled"`
	TWCCNegotiated            bool                   `json:"twccNegotiated"`
	NACKEnabled               bool                   `json:"nackEnabled"`
	NACKNegotiated            bool                   `json:"nackNegotiated"`
	RTXEnabled                bool                   `json:"rtxEnabled"`
	RTXNegotiated             bool                   `json:"rtxNegotiated"`
	FlexFECEnabled            bool                   `json:"flexFECEnabled"`
	FlexFECNegotiated         bool                   `json:"flexFECNegotiated"`
	AdaptiveBackend           config.AdaptiveBackend `json:"adaptiveBackend"`
	AdaptiveActive            bool                   `json:"adaptiveActive"`
	EstimatedBitrateBps       int                    `json:"estimatedBitrateBps"`
	EncoderTargetBitrateKbps  int                    `json:"encoderTargetBitrateKbps"`
	LastAppliedBitrateKbps    int                    `json:"lastAppliedBitrateKbps"`
	AdaptiveBitrateUpdates    uint64                 `json:"adaptiveBitrateUpdates"`
	AdaptiveBitrateFailures   uint64                 `json:"adaptiveBitrateFailures"`
	RecoveryKeyFrameRequests  uint64                 `json:"recoveryKeyFrameRequests"`
	RecoveryKeyFrameCoalesced uint64                 `json:"recoveryKeyFrameCoalesced"`
	RecoveryKeyFrameFailures  uint64                 `json:"recoveryKeyFrameFailures"`
	RTCPKeyFrameRequests      uint64                 `json:"rtcpKeyFrameRequests"`
	RTCPMalformedFeedback     uint64                 `json:"rtcpMalformedFeedback"`
	Bandwidth                 *BandwidthStats        `json:"bandwidth,omitempty"`
	ICEPath                   *ICEPathStats          `json:"icePath,omitempty"`
}

type ICEPathStats struct {
	LocalCandidateType      string `json:"localCandidateType"`
	LocalCandidateProtocol  string `json:"localCandidateProtocol"`
	LocalCandidateURL       string `json:"localCandidateURL,omitempty"`
	LocalRelayProtocol      string `json:"localRelayProtocol,omitempty"`
	RemoteCandidateType     string `json:"remoteCandidateType"`
	RemoteCandidateProtocol string `json:"remoteCandidateProtocol"`
}

type BandwidthStats struct {
	LossTargetBitrateBps                 int     `json:"lossTargetBitrateBps"`
	DelayTargetBitrateBps                int     `json:"delayTargetBitrateBps"`
	AverageLoss                          float64 `json:"averageLoss"`
	FlexFECMediaPackets                  uint32  `json:"flexFECMediaPackets"`
	FlexFECRepairPackets                 uint32  `json:"flexFECRepairPackets"`
	DelayMeasurementMs                   float64 `json:"delayMeasurementMs"`
	DelayEstimateMs                      float64 `json:"delayEstimateMs"`
	DelayThresholdMs                     float64 `json:"delayThresholdMs"`
	Usage                                string  `json:"usage"`
	State                                string  `json:"state"`
	LossGuardActive                      bool    `json:"lossGuardActive"`
	LossGuardTargetBitrateBps            int     `json:"lossGuardTargetBitrateBps"`
	LossGuardLastObservedLoss            float64 `json:"lossGuardLastObservedLoss"`
	LossGuardReductions                  uint64  `json:"lossGuardReductions"`
	LossGuardRecoveries                  uint64  `json:"lossGuardRecoveries"`
	PacerTargetBitrateBps                int     `json:"pacerTargetBitrateBps"`
	PacerPacingBitrateBps                int     `json:"pacerPacingBitrateBps"`
	PacerQueuePackets                    int     `json:"pacerQueuePackets"`
	PacerQueueDrops                      uint64  `json:"pacerQueueDrops"`
	PacerQueueDelayMs                    float64 `json:"pacerQueueDelayMs"`
	PacerMaximumDelayMs                  float64 `json:"pacerMaximumDelayMs"`
	PacerMaximumPrimaryDelayMs           float64 `json:"pacerMaximumPrimaryDelayMs"`
	PacerMaximumRepairDelayMs            float64 `json:"pacerMaximumRepairDelayMs"`
	PacerMaximumRetransmissionDelayMs    float64 `json:"pacerMaximumRetransmissionDelayMs"`
	PacerMaximumFECDelayMs               float64 `json:"pacerMaximumFECDelayMs"`
	PacerMaximumSustainedDelayMs         float64 `json:"pacerMaximumSustainedDelayMs"`
	PacerMaximumAdmittedDelayMs          float64 `json:"pacerMaximumAdmittedDelayMs"`
	PacerKeyFrameReserveBytes            int64   `json:"pacerKeyFrameReserveBytes"`
	PacerMediaFrameDrops                 uint64  `json:"pacerMediaFrameDrops"`
	PacerMediaByteDrops                  uint64  `json:"pacerMediaByteDrops"`
	PacerRepairPacketsExpired            uint64  `json:"pacerRepairPacketsExpired"`
	PacerRepairPacketsTrimmed            uint64  `json:"pacerRepairPacketsTrimmed"`
	PacerRetransmissionPacketsExpired    uint64  `json:"pacerRetransmissionPacketsExpired"`
	PacerRetransmissionPacketsCoalesced  uint64  `json:"pacerRetransmissionPacketsCoalesced"`
	PacerRetransmissionPacketsSuppressed uint64  `json:"pacerRetransmissionPacketsSuppressed"`
	PacerRetransmissionRoundTripTimeMs   float64 `json:"pacerRetransmissionRTTMs"`
	PacerRetransmissionRetryIntervalMs   float64 `json:"pacerRetransmissionIntervalMs"`
	PacerFECPacketsExpired               uint64  `json:"pacerFECPacketsExpired"`
	PacerRetransmissionPacketsTrimmed    uint64  `json:"pacerRetransmissionPacketsTrimmed"`
	PacerFECPacketsTrimmed               uint64  `json:"pacerFECPacketsTrimmed"`
	PacerSentPrimary                     uint64  `json:"pacerSentPrimary"`
	PacerSentPrimaryBytes                uint64  `json:"pacerSentPrimaryBytes"`
	PacerSentRepair                      uint64  `json:"pacerSentRepair"`
	PacerSentRetransmission              uint64  `json:"pacerSentRetransmission"`
	PacerSentRetransmissionBytes         uint64  `json:"pacerSentRetransmissionBytes"`
	PacerSentFEC                         uint64  `json:"pacerSentFEC"`
	PacerSentFECBytes                    uint64  `json:"pacerSentFECBytes"`
	PacerPrimarySSRC                     uint32  `json:"pacerPrimarySSRC"`
	PacerRetransmissionSSRC              uint32  `json:"pacerRetransmissionSSRC"`
	PacerForwardErrorCorrectionSSRC      uint32  `json:"pacerForwardErrorCorrectionSSRC"`
	PacerFirstRetransmissionSequence     uint32  `json:"pacerFirstRetransmissionSequence"`
	PacerLastRetransmissionSequence      uint32  `json:"pacerLastRetransmissionSequence"`
	PacerRetransmissionSequenceSamples   uint64  `json:"pacerRetransmissionSequenceSamples"`
	StaleBitrateCallbacks                uint64  `json:"staleBitrateCallbacks"`
	TWCCFeedbackPackets                  uint64  `json:"twccFeedbackPackets"`
	TWCCMalformedFeedback                uint64  `json:"twccMalformedFeedback"`
	TWCCPaddingStatuses                  uint64  `json:"twccPaddingStatuses"`
	TWCCReportedLost                     uint64  `json:"twccReportedLost"`
	TWCCReportedStatuses                 uint64  `json:"twccReportedStatuses"`
}

type Broadcaster struct {
	cfg            config.Config
	logger         *logs.Logger
	sourceFactory  media.Factory
	sharedSource   media.Source
	sharedUsers    int
	turn           *turnprovider.Provider
	peerFactory    *peerConnectionFactory
	codec          webrtc.RTPCodecCapability
	streamID       string
	trackID        string
	useTURN        bool
	mediaMode      config.MediaMode
	maxViewers     int
	sharedInitGate chan struct{}
	mu             sync.Mutex
	sessions       map[string]*Session
	retired        producerTotals
	opening        int
	closed         bool
}

type Session struct {
	id                        string
	logger                    *logs.Logger
	pc                        *webrtc.PeerConnection
	track                     *webrtc.TrackLocalStaticSample
	sender                    *webrtc.RTPSender
	unsubscribe               func()
	release                   func()
	estimator                 bandwidthEstimator
	encoder                   media.EncoderController
	adaptive                  *adaptation.Controller
	close                     sync.Once
	closed                    chan struct{}
	lifecycleMu               sync.Mutex
	mediaReady                chan struct{}
	mediaReadyOnce            sync.Once
	receiverReady             chan struct{}
	receiverReadyOnce         sync.Once
	nativeBootstrapOnce       sync.Once
	nativeReadinessTimeout    time.Duration
	writeNativeTrackProbe     func() error
	onClose                   func(string)
	statsMu                   sync.RWMutex
	stats                     SessionStats
	signalingMu               sync.Mutex
	whepRemoteCandidates      int
	candidateMu               sync.Mutex
	localICE                  candidateCounts
	remoteICE                 candidateCounts
	recoveryKeyFrameRequests  atomic.Uint64
	recoveryKeyFrameCoalesced atomic.Uint64
	recoveryKeyFrameFailures  atomic.Uint64
	rtcpKeyFrameRequests      atomic.Uint64
	rtcpMalformedFeedback     atomic.Uint64
	mediaSSRC                 atomic.Uint32
	malformedRTCPLog          sync.Once
	keyFrameMu                sync.Mutex
	lastKeyFrameRequest       time.Time
	keyFrameRequestTimer      *time.Timer
	keyFrameRequestDue        time.Time
	keyFrameRequestGeneration uint64
	recoveryMu                sync.Mutex
	recovery                  *time.Timer
	whep                      atomic.Bool
	mediaMTXNative            atomic.Bool
	nativeMediaBitrateBps     int
	requiredTransport         *transportNegotiation
	allowMediaMTXNativeOffer  bool
	refreshICEServers         func(context.Context) ([]webrtc.ICEServer, map[string]string, error)
	iceConfigMu               sync.RWMutex
	turnURLs                  map[string]string
}

type candidateCounts struct {
	Host            int
	ServerReflexive int
	PeerReflexive   int
	Relay           int
	Unknown         int
}

const (
	networkRecoveryTimeout  = 30 * time.Second
	keyFrameRequestInterval = 250 * time.Millisecond
	nativeReceiverTimeout   = 8 * time.Second
)

// ErrSessionCapacity identifies a temporary viewer-admission refusal.
var ErrSessionCapacity = errors.New("viewer session capacity exhausted")

func NewBroadcaster(cfg config.Config, sourceFactory media.Factory, turn *turnprovider.Provider, logger *logs.Logger) (*Broadcaster, error) {
	peerFactory, codec, err := newPeerConnectionFactory(cfg)
	if err != nil {
		return nil, err
	}
	return &Broadcaster{
		cfg:            cfg,
		logger:         logger,
		sourceFactory:  sourceFactory,
		turn:           turn,
		peerFactory:    peerFactory,
		codec:          codec,
		streamID:       cfg.WebRTC.Video.StreamID,
		trackID:        cfg.WebRTC.Video.TrackID,
		useTURN:        cfg.WebRTC.UseTURN,
		mediaMode:      cfg.MediaMode(),
		maxViewers:     cfg.WebRTC.MaxViewers,
		sharedInitGate: make(chan struct{}, 1),
		sessions:       make(map[string]*Session),
	}, nil
}

func (b *Broadcaster) OpenSession(ctx context.Context) (*Session, error) {
	if err := b.reserveSession(); err != nil {
		return nil, err
	}
	releaseReservation := true
	defer func() {
		if releaseReservation {
			b.releaseReservation()
		}
	}()
	source, release, err := b.acquireSource(ctx)
	if err != nil {
		return nil, err
	}
	releaseSource := true
	defer func() {
		if releaseSource {
			release()
		}
	}()
	encoderController, _ := sourceEncoderController(source)
	var credentials *rstream.TURNCredentials
	turnURLs := make(map[string]string)
	if b.useTURN {
		credentials, err = b.turn.Credentials(ctx)
		if err != nil {
			b.logger.Warn("Failed to load TURN credentials for viewer session: %v", err)
			return nil, err
		}
		turnURLs, err = turnprovider.URLsByTransport(credentials)
		if err != nil {
			return nil, fmt.Errorf("failed to classify TURN credentials: %w", err)
		}
		b.logger.Info("Viewer session TURN credentials loaded (%d URL(s))", len(credentials.URLs))
	} else {
		b.logger.Info("Viewer session TURN disabled")
	}
	initialBitrateBps := b.cfg.InitialBitrateKbps() * 1000
	if encoderController != nil {
		info := encoderController.Info()
		if info.TargetBitrateKbps > 0 {
			initialBitrateBps = info.TargetBitrateKbps * 1000
		}
	}
	iceConfiguration := turnprovider.ICEConfig(credentials)
	iceConfiguration.BundlePolicy = webrtc.BundlePolicyMaxBundle
	iceConfiguration.SDPSemantics = webrtc.SDPSemanticsUnifiedPlan
	if b.cfg.ICETransportPolicy() == config.ICETransportPolicyRelay {
		iceConfiguration.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	peerConnection, estimator, err := b.peerFactory.NewPeerConnection(initialBitrateBps, iceConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to create the peer connection: %w", err)
	}
	sessionID, err := randomID()
	if err != nil {
		_ = peerConnection.Close()
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(b.codec, b.trackID, b.streamID)
	if err != nil {
		_ = peerConnection.Close()
		return nil, fmt.Errorf("failed to create the video track: %w", err)
	}
	sender, err := peerConnection.AddTrack(track)
	if err != nil {
		_ = peerConnection.Close()
		return nil, fmt.Errorf("failed to attach the video track: %w", err)
	}
	samples, unsubscribe := source.Subscribe()
	session := &Session{
		id:            sessionID,
		logger:        b.logger,
		pc:            peerConnection,
		track:         track,
		sender:        sender,
		unsubscribe:   unsubscribe,
		release:       release,
		estimator:     estimator,
		encoder:       encoderController,
		closed:        make(chan struct{}),
		mediaReady:    make(chan struct{}),
		receiverReady: make(chan struct{}),
		writeNativeTrackProbe: func() error {
			return track.WriteSample(rtcmedia.Sample{
				Data:     []byte(nativeMediaMTXTrackProbe),
				Duration: time.Second / 30,
			})
		},
		requiredTransport:        requiredWHEPTransport(b.cfg),
		allowMediaMTXNativeOffer: b.cfg.Web.WHEP.AllowMediaMTXNativeOffer,
		nativeMediaBitrateBps:    initialBitrateBps,
		turnURLs:                 turnURLs,
		stats: SessionStats{
			Codec:           b.codec.MimeType,
			TWCCEnabled:     b.cfg.WebRTC.Interceptors.TWCC,
			NACKEnabled:     b.cfg.WebRTC.Interceptors.NACK,
			RTXEnabled:      b.cfg.WebRTC.Interceptors.RTX,
			FlexFECEnabled:  b.cfg.WebRTC.Interceptors.FlexFEC,
			AdaptiveBackend: b.cfg.AdaptiveBackend(),
		},
	}
	if b.useTURN {
		session.refreshICEServers = func(ctx context.Context) ([]webrtc.ICEServer, map[string]string, error) {
			credentials, err := b.turn.Credentials(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("refresh viewer TURN credentials: %w", err)
			}
			urls, err := turnprovider.URLsByTransport(credentials)
			if err != nil {
				return nil, nil, fmt.Errorf("classify refreshed TURN credentials: %w", err)
			}
			return turnprovider.ICEConfig(credentials).ICEServers, urls, nil
		}
	}
	session.onClose = func(reason string) {
		count := b.retireSession(session)
		b.logger.Info("Viewer %s closed: %s (active viewers: %d)", session.id, reason, count)
	}
	session.trackSelectedICEPath()
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		init := candidate.ToJSON()
		session.recordLocalICECandidate(init.Candidate)
	})
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		b.logger.Info("Viewer %s peer connection state: %s", session.id, state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			session.handleConnected()
		case webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateFailed:
			session.scheduleNetworkRecovery(state.String())
		case webrtc.PeerConnectionStateClosed:
			session.Close("peer connection closed")
		}
	})
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		b.logger.Info("Viewer %s ICE connection state: %s", session.id, state.String())
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			session.clearNetworkRecovery()
		case webrtc.ICEConnectionStateDisconnected, webrtc.ICEConnectionStateFailed:
			session.scheduleNetworkRecovery("ICE " + state.String())
		}
	})
	if adaptiveController, ok := b.newAdaptiveController(
		encoderController,
		estimator,
		func() { session.requestRecoveryKeyFrame(0) },
	); ok {
		session.adaptive = adaptiveController
		snapshot := adaptiveController.Snapshot()
		session.updateStats(func(stats *SessionStats) {
			stats.EncoderTargetBitrateKbps = snapshot.EncoderTargetBitrateKbps
			stats.LastAppliedBitrateKbps = snapshot.LastAppliedBitrateKbps
		})
	}
	if estimator != nil {
		initialEstimate := estimator.GetTargetBitrate()
		session.updateStats(func(stats *SessionStats) {
			stats.EstimatedBitrateBps = initialEstimate
		})
		if session.adaptive != nil && initialEstimate > 0 {
			session.adaptive.UpdateEstimatedBitrate(initialEstimate)
		}
		if encoderController != nil {
			info := encoderController.Info()
			b.logger.Debug(
				"Viewer %s session has TWCC feedback and dynamic encoder control (%s on %s at %d kbit/s)",
				session.id,
				info.Factory,
				info.Name,
				info.TargetBitrateKbps,
			)
		} else {
			b.logger.Debug("Viewer %s session has TWCC feedback but no dynamic encoder control", session.id)
		}
		estimator.OnTargetBitrateChange(func(bitrate int) {
			b.logger.Debug("Viewer %s TWCC target bitrate updated to %d bps", session.id, bitrate)
			session.updateStats(func(stats *SessionStats) {
				stats.EstimatedBitrateBps = bitrate
			})
			if session.adaptive != nil {
				session.adaptive.UpdateEstimatedBitrate(bitrate)
			}
		})
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		releaseSource = false
		session.Close("broadcaster closed")
		return nil, errors.New("the broadcaster is closed")
	}
	if b.opening > 0 {
		b.opening--
	}
	b.sessions[session.id] = session
	count := len(b.sessions)
	b.mu.Unlock()
	releaseReservation = false
	releaseSource = false
	go session.drainRTCP()
	go session.writeSamples(samples)
	b.logger.Info("Viewer %s connected (active viewers: %d)", session.id, count)
	return session, nil
}

func requiredWHEPTransport(cfg config.Config) *transportNegotiation {
	if !cfg.Web.WHEP.RequireConfiguredFeatures {
		return nil
	}
	return &transportNegotiation{
		twcc:    cfg.WebRTC.Interceptors.TWCC,
		nack:    cfg.WebRTC.Interceptors.NACK,
		rtx:     cfg.WebRTC.Interceptors.RTX,
		flexFEC: cfg.WebRTC.Interceptors.FlexFEC,
	}
}

func (b *Broadcaster) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	sessions := make([]*Session, 0, len(b.sessions))
	for _, session := range b.sessions {
		sessions = append(sessions, session)
	}
	sharedSource := b.sharedSource
	b.sharedSource = nil
	b.sharedUsers = 0
	b.mu.Unlock()
	for _, session := range sessions {
		session.Close("application shutdown")
	}
	if sharedSource != nil {
		return sharedSource.Close()
	}
	return nil
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Done() <-chan struct{} {
	return s.closed
}

func (s *Session) TargetBitrate() (int, bool) {
	if s.mediaMTXNative.Load() && s.nativeMediaBitrateBps > 0 {
		return s.nativeMediaBitrateBps, true
	}
	if s.estimator == nil {
		return 0, false
	}
	return s.estimator.GetTargetBitrate(), true
}

func (s *Session) BandwidthStats() map[string]any {
	if s.estimator == nil {
		return nil
	}
	return s.estimator.GetStats()
}

func (s *Session) EncoderInfo() (media.EncoderInfo, bool) {
	if s.encoder == nil {
		return media.EncoderInfo{}, false
	}
	return s.encoder.Info(), true
}

func (s *Session) SetEncoderTargetBitrateKbps(value int) error {
	if s.encoder == nil {
		return errors.New("dynamic encoder control is unavailable")
	}
	return s.encoder.SetTargetBitrateKbps(value)
}

func (s *Session) HandleWHEPOffer(ctx context.Context, offer string) (string, error) {
	profile, err := inspectWHEPOfferProfile(offer, s.allowMediaMTXNativeOffer)
	if err != nil {
		return "", err
	}
	if profile.mediaMTXNative {
		s.mediaMTXNative.Store(true)
		if fixed, ok := s.estimator.(interface{ UseFixedMediaTargetBitrate(int) }); ok {
			fixed.UseFixedMediaTargetBitrate(s.nativeMediaBitrateBps)
		}
	}
	s.whep.Store(true)
	answer, err := s.createAnswer(ctx, offer, true)
	if err != nil {
		return "", err
	}
	answer, err = prepareWHEPAnswer(answer)
	if err != nil {
		return "", err
	}
	if s.requiredTransport == nil {
		return answer, nil
	}
	missing := s.missingConfiguredTransportFeatures()
	if profile.mediaMTXNative {
		missing = filterNativeMediaMTXOptionalFeatures(missing)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("required WHEP transport features were not negotiated: %s", strings.Join(missing, ", "))
	}
	return answer, nil
}

func filterNativeMediaMTXOptionalFeatures(features []string) []string {
	filtered := features[:0]
	for _, feature := range features {
		if feature != "rtx" && feature != "flexfec" {
			filtered = append(filtered, feature)
		}
	}
	return filtered
}

func (s *Session) missingConfiguredTransportFeatures() []string {
	stats := s.StatsSnapshot()
	missing := make([]string, 0, 4)
	if s.requiredTransport.twcc && !stats.TWCCNegotiated {
		missing = append(missing, "twcc")
	}
	if s.requiredTransport.nack && !stats.NACKNegotiated {
		missing = append(missing, "nack")
	}
	if s.requiredTransport.rtx && !stats.RTXNegotiated {
		missing = append(missing, "rtx")
	}
	if s.requiredTransport.flexFEC && !stats.FlexFECNegotiated {
		missing = append(missing, "flexfec")
	}
	return missing
}

func (s *Session) handleConnected() {
	s.clearNetworkRecovery()
	if s.mediaMTXNative.Load() {
		s.nativeBootstrapOnce.Do(s.startNativeReceiverBootstrap)
		return
	}
	s.releaseMedia()
}

func (s *Session) startNativeReceiverBootstrap() {
	if s.writeNativeTrackProbe == nil || s.receiverReady == nil {
		s.releaseMedia()
		return
	}
	if err := s.writeNativeTrackProbe(); err != nil {
		s.logger.Warn("Viewer %s receiver bootstrap failed: %v", s.id, err)
		s.Close("receiver bootstrap failed")
		return
	}
	go s.waitForNativeReceiver()
}

const nativeMediaMTXTrackProbe = "\x00\x00\x00\x01\x06\x05\x10rstreambootstrap\x80"

func (s *Session) waitForNativeReceiver() {
	timeout := s.nativeReadinessTimeout
	if timeout <= 0 {
		timeout = nativeReceiverTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.receiverReady:
	case <-timer.C:
		s.logger.Warn("Viewer %s receiver readiness feedback timed out after %s", s.id, timeout)
		s.Close("receiver readiness timed out")
		return
	case <-s.closed:
		return
	}
	s.releaseMedia()
}

func (s *Session) releaseMedia() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.isClosed() {
		return
	}
	s.mediaReadyOnce.Do(func() {
		s.requestRecoveryKeyFrame(0)
		if s.mediaReady != nil {
			close(s.mediaReady)
		}
	})
}

func (s *Session) markNativeReceiverReady() {
	if !s.mediaMTXNative.Load() || s.receiverReady == nil {
		return
	}
	s.receiverReadyOnce.Do(func() {
		close(s.receiverReady)
	})
}

func (s *Session) createAnswer(ctx context.Context, offer string, gatherComplete bool) (string, error) {
	if strings.TrimSpace(offer) == "" {
		return "", errors.New("offer SDP is required")
	}
	s.signalingMu.Lock()
	defer s.signalingMu.Unlock()
	// The browser may send more than one offer on the same session when it
	// performs an ICE restart after a network interface or IP change.
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	}); err != nil {
		return "", fmt.Errorf("failed to apply the remote offer: %w", err)
	}
	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create the answer: %w", err)
	}
	var complete <-chan struct{}
	if gatherComplete {
		complete = webrtc.GatheringCompletePromise(s.pc)
	}
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set the local answer: %w", err)
	}
	s.recordTransportNegotiation()
	if !gatherComplete {
		return answer.SDP, nil
	}
	select {
	case <-complete:
	case <-ctx.Done():
		return "", fmt.Errorf("candidate gathering interrupted: %w", ctx.Err())
	case <-s.closed:
		return "", errors.New("the WebRTC session closed while gathering candidates")
	}
	local := s.pc.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return "", errors.New("the complete local answer is unavailable")
	}
	return local.SDP, nil
}

func (s *Session) recordTransportNegotiation() {
	negotiation := transportNegotiationFromParameters(s.sender.GetParameters())
	adaptive := negotiation.twcc && s.adaptive != nil && !s.mediaMTXNative.Load()
	if adaptive {
		s.adaptive.Start()
	}
	s.updateStats(func(stats *SessionStats) {
		stats.TWCCNegotiated = negotiation.twcc
		stats.NACKNegotiated = negotiation.nack
		stats.RTXNegotiated = negotiation.rtx
		stats.FlexFECNegotiated = negotiation.flexFEC
		stats.AdaptiveActive = adaptive
	})
}

type transportNegotiation struct {
	twcc    bool
	nack    bool
	rtx     bool
	flexFEC bool
}

func transportNegotiationFromParameters(parameters webrtc.RTPSendParameters) transportNegotiation {
	negotiation := transportNegotiation{}
	twccExtension := false
	twccFeedback := false
	for _, extension := range parameters.HeaderExtensions {
		if extension.URI == transportCCHeaderExtensionURI {
			twccExtension = true
		}
	}
	for _, codec := range parameters.Codecs {
		switch {
		case strings.EqualFold(codec.MimeType, webrtc.MimeTypeRTX):
			negotiation.rtx = true
		case strings.EqualFold(codec.MimeType, webrtc.MimeTypeFlexFEC), strings.EqualFold(codec.MimeType, webrtc.MimeTypeFlexFEC03):
			negotiation.flexFEC = true
		default:
			for _, feedback := range codec.RTCPFeedback {
				if strings.EqualFold(feedback.Type, "nack") {
					negotiation.nack = true
				}
				if strings.EqualFold(feedback.Type, webrtc.TypeRTCPFBTransportCC) {
					twccFeedback = true
				}
			}
		}
	}
	negotiation.twcc = twccExtension && twccFeedback
	return negotiation
}

func (s *Session) Close(reason string) {
	s.close.Do(func() {
		s.lifecycleMu.Lock()
		close(s.closed)
		s.lifecycleMu.Unlock()
		s.clearNetworkRecovery()
		s.cancelScheduledKeyFrameRequest()
		if s.unsubscribe != nil {
			s.unsubscribe()
		}
		if s.adaptive != nil {
			s.adaptive.Close()
		}
		if s.pc != nil {
			_ = s.pc.Close()
		}
		if s.release != nil {
			s.release()
		}
		if s.onClose != nil {
			s.onClose(reason)
		}
	})
}

func (s *Session) scheduleNetworkRecovery(reason string) {
	if s.isClosed() {
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if s.isClosed() {
		return
	}
	if s.recovery != nil {
		return
	}
	s.logger.Warn(
		"Viewer %s network path changed (%s); waiting up to %s for ICE recovery",
		s.id,
		reason,
		networkRecoveryTimeout,
	)
	s.logger.Info("Viewer %s ICE candidates: %s", s.id, s.iceCandidateSummary())
	s.recovery = time.AfterFunc(networkRecoveryTimeout, func() {
		s.Close("network recovery timed out")
	})
}

func (s *Session) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *Session) clearNetworkRecovery() {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if s.recovery == nil {
		return
	}
	s.recovery.Stop()
	s.recovery = nil
}

func (s *Session) recordLocalICECandidate(candidate string) {
	s.candidateMu.Lock()
	s.localICE.add(candidate)
	s.candidateMu.Unlock()
}

func (s *Session) recordRemoteICECandidate(candidate string) {
	s.candidateMu.Lock()
	s.remoteICE.add(candidate)
	s.candidateMu.Unlock()
}

func (s *Session) iceCandidateSummary() string {
	s.candidateMu.Lock()
	defer s.candidateMu.Unlock()
	return fmt.Sprintf("local %s, remote %s", s.localICE.String(), s.remoteICE.String())
}

func (c *candidateCounts) add(candidate string) {
	switch candidateType(candidate) {
	case "host":
		c.Host++
	case "srflx":
		c.ServerReflexive++
	case "prflx":
		c.PeerReflexive++
	case "relay":
		c.Relay++
	default:
		c.Unknown++
	}
}

func (c candidateCounts) String() string {
	return fmt.Sprintf(
		"host=%d srflx=%d prflx=%d relay=%d unknown=%d",
		c.Host,
		c.ServerReflexive,
		c.PeerReflexive,
		c.Relay,
		c.Unknown,
	)
}

func candidateType(candidate string) string {
	fields := strings.Fields(candidate)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "typ" {
			return fields[i+1]
		}
	}
	return "unknown"
}

func (s *Session) StatsSnapshot() SessionStats {
	s.ensureSelectedICEPath()
	s.statsMu.RLock()
	stats := s.stats
	s.statsMu.RUnlock()
	stats.RecoveryKeyFrameRequests = s.recoveryKeyFrameRequests.Load()
	stats.RecoveryKeyFrameCoalesced = s.recoveryKeyFrameCoalesced.Load()
	stats.RecoveryKeyFrameFailures = s.recoveryKeyFrameFailures.Load()
	stats.RTCPKeyFrameRequests = s.rtcpKeyFrameRequests.Load()
	stats.RTCPMalformedFeedback = s.rtcpMalformedFeedback.Load()
	stats.Bandwidth = snapshotBandwidthStats(s.estimator)
	if s.adaptive == nil {
		return stats
	}
	snapshot := s.adaptive.Snapshot()
	stats.AdaptiveActive = snapshot.Active
	stats.EstimatedBitrateBps = snapshot.EstimatedBitrateBps
	stats.EncoderTargetBitrateKbps = snapshot.EncoderTargetBitrateKbps
	stats.LastAppliedBitrateKbps = snapshot.LastAppliedBitrateKbps
	stats.AdaptiveBitrateUpdates = snapshot.AppliedUpdates
	stats.AdaptiveBitrateFailures = snapshot.FailedUpdates
	return stats
}

func (s *Session) trackSelectedICEPath() {
	transport := s.sender.Transport()
	if transport == nil || transport.ICETransport() == nil {
		return
	}
	transport.ICETransport().OnSelectedCandidatePairChange(func(pair *webrtc.ICECandidatePair) {
		s.updateSelectedICEPath(pair)
	})
}

func (s *Session) ensureSelectedICEPath() {
	s.statsMu.RLock()
	path := s.stats.ICEPath
	complete := path != nil && (path.LocalCandidateType != "relay" || path.LocalRelayProtocol != "")
	s.statsMu.RUnlock()
	if complete {
		return
	}
	if s.sender == nil {
		return
	}
	transport := s.sender.Transport()
	if transport == nil || transport.ICETransport() == nil {
		return
	}
	pair, err := transport.ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return
	}
	s.updateSelectedICEPath(pair)
}

func (s *Session) updateSelectedICEPath(pair *webrtc.ICECandidatePair) {
	path := selectedICEPath(s.pc.GetStats(), pair)
	if path == nil {
		return
	}
	s.iceConfigMu.RLock()
	completeICEPathURL(path, s.turnURLs)
	s.iceConfigMu.RUnlock()
	s.logger.Debug(
		"Viewer %s selected ICE path: local=%s/%s relay-transport=%s remote=%s/%s",
		s.id,
		path.LocalCandidateType,
		path.LocalCandidateProtocol,
		path.LocalRelayProtocol,
		path.RemoteCandidateType,
		path.RemoteCandidateProtocol,
	)
	s.updateStats(func(stats *SessionStats) {
		stats.ICEPath = path
	})
}

func (s *Session) replaceTURNURLs(urls map[string]string) {
	replacement := make(map[string]string, len(urls))
	for transport, endpoint := range urls {
		replacement[transport] = endpoint
	}
	s.iceConfigMu.Lock()
	s.turnURLs = replacement
	s.iceConfigMu.Unlock()
}

func completeICEPathURL(path *ICEPathStats, urls map[string]string) {
	if path != nil && path.LocalCandidateURL == "" {
		path.LocalCandidateURL = urls[path.LocalRelayProtocol]
	}
}

func selectedICEPath(report webrtc.StatsReport, pair *webrtc.ICECandidatePair) *ICEPathStats {
	var local *webrtc.ICECandidateStats
	if pair != nil && pair.Local != nil {
		if stats, ok := report.GetICECandidateStats(pair.Local); ok {
			local = &stats
		}
	}
	return icePathFromCandidates(pair, local)
}

func icePathFromCandidates(pair *webrtc.ICECandidatePair, local *webrtc.ICECandidateStats) *ICEPathStats {
	if pair == nil || pair.Local == nil || pair.Remote == nil {
		return nil
	}
	path := &ICEPathStats{
		LocalCandidateType:      pair.Local.Typ.String(),
		LocalCandidateProtocol:  pair.Local.Protocol.String(),
		RemoteCandidateType:     pair.Remote.Typ.String(),
		RemoteCandidateProtocol: pair.Remote.Protocol.String(),
	}
	if local != nil {
		path.LocalCandidateURL = local.URL
		path.LocalRelayProtocol = local.RelayProtocol
	}
	return path
}

func snapshotBandwidthStats(estimator bandwidthEstimator) *BandwidthStats {
	if estimator == nil {
		return nil
	}
	raw := estimator.GetStats()
	if len(raw) == 0 {
		return nil
	}
	stats := &BandwidthStats{}
	stats.LossTargetBitrateBps, _ = raw["lossTargetBitrate"].(int)
	stats.DelayTargetBitrateBps, _ = raw["delayTargetBitrate"].(int)
	stats.AverageLoss, _ = raw["averageLoss"].(float64)
	stats.FlexFECMediaPackets, _ = raw["flexFECMediaPackets"].(uint32)
	stats.FlexFECRepairPackets, _ = raw["flexFECRepairPackets"].(uint32)
	stats.DelayMeasurementMs, _ = raw["delayMeasurement"].(float64)
	stats.DelayEstimateMs, _ = raw["delayEstimate"].(float64)
	stats.DelayThresholdMs, _ = raw["delayThreshold"].(float64)
	stats.Usage, _ = raw["usage"].(string)
	stats.State, _ = raw["state"].(string)
	stats.LossGuardActive, _ = raw["lossGuardActive"].(bool)
	stats.LossGuardTargetBitrateBps, _ = raw["lossGuardTargetBitrate"].(int)
	stats.LossGuardLastObservedLoss, _ = raw["lossGuardLastObservedLoss"].(float64)
	stats.LossGuardReductions, _ = raw["lossGuardReductions"].(uint64)
	stats.LossGuardRecoveries, _ = raw["lossGuardRecoveries"].(uint64)
	stats.PacerTargetBitrateBps, _ = raw["pacerTargetBitrateBps"].(int)
	stats.PacerPacingBitrateBps, _ = raw["pacerPacingBitrateBps"].(int)
	stats.PacerQueuePackets, _ = raw["pacerQueuePackets"].(int)
	stats.PacerQueueDrops, _ = raw["pacerQueueDrops"].(uint64)
	stats.PacerQueueDelayMs, _ = raw["pacerQueueDelayMilliseconds"].(float64)
	stats.PacerMaximumDelayMs, _ = raw["pacerMaximumQueueDelayMilliseconds"].(float64)
	stats.PacerMaximumPrimaryDelayMs, _ = raw["pacerMaximumPrimaryResidenceMilliseconds"].(float64)
	stats.PacerMaximumRepairDelayMs, _ = raw["pacerMaximumRepairResidenceMilliseconds"].(float64)
	stats.PacerMaximumRetransmissionDelayMs, _ = raw["pacerMaximumRetransmissionResidenceMilliseconds"].(float64)
	stats.PacerMaximumFECDelayMs, _ = raw["pacerMaximumForwardErrorCorrectionResidenceMilliseconds"].(float64)
	stats.PacerMaximumSustainedDelayMs, _ = raw["pacerMaximumSustainedDelayMilliseconds"].(float64)
	stats.PacerMaximumAdmittedDelayMs, _ = raw["pacerMaximumAdmittedSustainedDelayMilliseconds"].(float64)
	stats.PacerKeyFrameReserveBytes, _ = raw["pacerKeyFrameReserveBytes"].(int64)
	stats.PacerMediaFrameDrops, _ = raw["pacerMediaFramesDropped"].(uint64)
	stats.PacerMediaByteDrops, _ = raw["pacerMediaBytesDropped"].(uint64)
	stats.PacerRepairPacketsExpired, _ = raw["pacerRepairPacketsExpired"].(uint64)
	stats.PacerRepairPacketsTrimmed, _ = raw["pacerRepairPacketsTrimmed"].(uint64)
	stats.PacerRetransmissionPacketsExpired, _ = raw["pacerRetransmissionPacketsExpired"].(uint64)
	stats.PacerRetransmissionPacketsCoalesced, _ = raw["pacerRetransmissionPacketsCoalesced"].(uint64)
	stats.PacerRetransmissionPacketsSuppressed, _ = raw["pacerRetransmissionPacketsSuppressed"].(uint64)
	stats.PacerRetransmissionRoundTripTimeMs, _ = raw["pacerRetransmissionRoundTripTimeMilliseconds"].(float64)
	stats.PacerRetransmissionRetryIntervalMs, _ = raw["pacerRetransmissionMinimumIntervalMilliseconds"].(float64)
	stats.PacerFECPacketsExpired, _ = raw["pacerForwardErrorCorrectionPacketsExpired"].(uint64)
	stats.PacerRetransmissionPacketsTrimmed, _ = raw["pacerRetransmissionPacketsTrimmed"].(uint64)
	stats.PacerFECPacketsTrimmed, _ = raw["pacerForwardErrorCorrectionPacketsTrimmed"].(uint64)
	stats.PacerSentPrimary, _ = raw["pacerSentPrimary"].(uint64)
	stats.PacerSentPrimaryBytes, _ = raw["pacerSentPrimaryBytes"].(uint64)
	stats.PacerSentRepair, _ = raw["pacerSentRepair"].(uint64)
	stats.PacerSentRetransmission, _ = raw["pacerSentRetransmission"].(uint64)
	stats.PacerSentRetransmissionBytes, _ = raw["pacerSentRetransmissionBytes"].(uint64)
	stats.PacerSentFEC, _ = raw["pacerSentForwardErrorCorrection"].(uint64)
	stats.PacerSentFECBytes, _ = raw["pacerSentForwardErrorCorrectionBytes"].(uint64)
	stats.PacerPrimarySSRC, _ = raw["pacerPrimarySSRC"].(uint32)
	stats.PacerRetransmissionSSRC, _ = raw["pacerRetransmissionSSRC"].(uint32)
	stats.PacerForwardErrorCorrectionSSRC, _ = raw["pacerForwardErrorCorrectionSSRC"].(uint32)
	stats.PacerFirstRetransmissionSequence, _ = raw["pacerFirstRetransmissionSequence"].(uint32)
	stats.PacerLastRetransmissionSequence, _ = raw["pacerLastRetransmissionSequence"].(uint32)
	stats.PacerRetransmissionSequenceSamples, _ = raw["pacerRetransmissionSequenceSamples"].(uint64)
	stats.StaleBitrateCallbacks, _ = raw["staleBitrateCallbacks"].(uint64)
	stats.TWCCFeedbackPackets, _ = raw["twccFeedbackPackets"].(uint64)
	stats.TWCCMalformedFeedback, _ = raw["twccMalformedFeedback"].(uint64)
	stats.TWCCPaddingStatuses, _ = raw["twccPaddingStatuses"].(uint64)
	stats.TWCCReportedLost, _ = raw["twccReportedLost"].(uint64)
	stats.TWCCReportedStatuses, _ = raw["twccReportedStatuses"].(uint64)
	return stats
}

func (s *Session) updateStats(update func(*SessionStats)) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	update(&s.stats)
}

func (s *Session) drainRTCP() {
	buffer := make([]byte, 1500)
	for {
		select {
		case <-s.closed:
			return
		default:
		}
		n, _, err := s.sender.Read(buffer)
		if err != nil {
			return
		}
		packets, err := rtcp.Unmarshal(buffer[:n])
		if err != nil {
			s.recordMalformedRTCP(err)
			continue
		}
		s.handleRTCPPackets(packets)
	}
}

func (s *Session) recordMalformedRTCP(err error) {
	s.rtcpMalformedFeedback.Add(1)
	s.malformedRTCPLog.Do(func() {
		s.logger.Warn("Viewer %s sent malformed RTCP feedback: %v", s.id, err)
	})
}

func (s *Session) handleRTCPPackets(packets []rtcp.Packet) {
	arrival := time.Now()
	mediaSSRC := s.outboundMediaSSRC()
	for _, packet := range packets {
		switch value := packet.(type) {
		case *rtcp.PictureLossIndication:
			if value.MediaSSRC != mediaSSRC {
				continue
			}
			s.markNativeReceiverReady()
			s.rtcpKeyFrameRequests.Add(1)
			s.requestKeyFrame()
		case *rtcp.FullIntraRequest:
			if value.MediaSSRC != mediaSSRC {
				continue
			}
			s.markNativeReceiverReady()
			s.rtcpKeyFrameRequests.Add(1)
			s.requestKeyFrame()
		case *rtcp.ReceiverReport:
			s.observeRoundTripTime(value.Reports, mediaSSRC, arrival)
		case *rtcp.SenderReport:
			s.observeRoundTripTime(value.Reports, mediaSSRC, arrival)
		}
	}
}

func (s *Session) outboundMediaSSRC() uint32 {
	if mediaSSRC := s.mediaSSRC.Load(); mediaSSRC != 0 {
		return mediaSSRC
	}
	if s.sender == nil {
		return 0
	}
	parameters := s.sender.GetParameters()
	if len(parameters.Encodings) == 0 {
		return 0
	}
	mediaSSRC := uint32(parameters.Encodings[0].SSRC)
	if mediaSSRC != 0 {
		s.mediaSSRC.CompareAndSwap(0, mediaSSRC)
	}
	return mediaSSRC
}

func (s *Session) observeRoundTripTime(
	reports []rtcp.ReceptionReport,
	mediaSSRC uint32,
	arrival time.Time,
) {
	if mediaSSRC == 0 {
		return
	}
	var observed time.Duration
	found := false
	for _, report := range reports {
		if report.SSRC != mediaSSRC {
			continue
		}
		roundTripTime, ok := receptionReportRoundTripTime(report, arrival)
		if ok && (!found || roundTripTime < observed) {
			observed = roundTripTime
			found = true
		}
	}
	if !found {
		return
	}
	observer, ok := s.estimator.(interface{ observeRoundTripTime(time.Duration) })
	if ok {
		observer.observeRoundTripTime(observed)
	}
}

func receptionReportRoundTripTime(
	report rtcp.ReceptionReport,
	arrival time.Time,
) (time.Duration, bool) {
	if report.LastSenderReport == 0 {
		return 0, false
	}
	elapsed := compactNTPTime(arrival) - report.LastSenderReport - report.Delay
	if elapsed > math.MaxInt32 {
		return 0, false
	}
	seconds := time.Duration(elapsed >> 16)
	fraction := time.Duration(elapsed & 0xffff)
	return seconds*time.Second + fraction*time.Second/(1<<16), true
}

func compactNTPTime(value time.Time) uint32 {
	const ntpEpochOffset = 2_208_988_800
	seconds := uint64(value.Unix() + ntpEpochOffset)
	fraction := uint64(value.Nanosecond()) * (1 << 32) / uint64(time.Second)
	return uint32(((seconds << 32) | fraction) >> 16)
}

func (s *Session) writeSamples(samples <-chan media.AccessUnit) {
	ready := s.mediaReady == nil
	started := ready
	for {
		select {
		case <-s.closed:
			return
		case unit, ok := <-samples:
			if !ok {
				s.logger.Warn("Viewer %s media source stopped", s.id)
				s.Close("media source stopped")
				return
			}
			if !ready {
				select {
				case <-s.mediaReady:
					ready = true
				default:
					continue
				}
			}
			if !started {
				if !unit.KeyFrame {
					continue
				}
				started = true
			}
			decision := mediaFrameAdmission{admitted: true}
			if admission, ok := s.estimator.(interface {
				AdmitMediaFrame(int, bool) mediaFrameAdmission
			}); ok && !s.mediaMTXNative.Load() {
				decision = admission.AdmitMediaFrame(
					len(unit.Data),
					unit.KeyFrame,
				)
				if decision.recoveryComplete {
					s.cancelScheduledKeyFrameRequest()
				}
				if decision.requestKeyFrame {
					s.requestRecoveryKeyFrame(decision.requestRetryAfter)
				}
				if !decision.admitted {
					continue
				}
			}
			err := s.track.WriteSample(rtcmedia.Sample{
				Data:     unit.Data,
				Duration: unit.Duration,
			})
			decision.completePacketization()
			if err != nil {
				s.logger.Warn("Viewer %s media write failed: %v", s.id, err)
				s.Close("media write failed")
				return
			}
		}
	}
}

func (s *Session) requestKeyFrame() {
	s.scheduleKeyFrameRequest(0, false)
}

func (s *Session) requestRecoveryKeyFrame(delay time.Duration) {
	s.scheduleKeyFrameRequest(delay, true)
}

func (s *Session) scheduleKeyFrameRequest(delay time.Duration, deferIfLimited bool) {
	if s.isClosed() {
		return
	}
	now := time.Now()
	due := now.Add(max(delay, 0))
	s.keyFrameMu.Lock()
	earliest := s.lastKeyFrameRequest.Add(keyFrameRequestInterval)
	if earliest.After(due) {
		due = earliest
	}
	if !deferIfLimited && delay <= 0 && due.After(now) {
		s.keyFrameMu.Unlock()
		s.recoveryKeyFrameCoalesced.Add(1)
		return
	}
	if due.After(now) {
		if s.keyFrameRequestTimer != nil && !due.Before(s.keyFrameRequestDue) {
			s.keyFrameMu.Unlock()
			s.recoveryKeyFrameCoalesced.Add(1)
			return
		}
		if s.keyFrameRequestTimer != nil {
			s.keyFrameRequestTimer.Stop()
		}
		s.keyFrameRequestGeneration++
		generation := s.keyFrameRequestGeneration
		s.keyFrameRequestDue = due
		s.keyFrameRequestTimer = time.AfterFunc(time.Until(due), func() {
			s.fireScheduledKeyFrameRequest(generation)
		})
		s.keyFrameMu.Unlock()
		return
	}
	s.cancelScheduledKeyFrameRequestLocked()
	s.lastKeyFrameRequest = now
	s.keyFrameMu.Unlock()
	s.issueKeyFrameRequest()
}

func (s *Session) fireScheduledKeyFrameRequest(generation uint64) {
	s.keyFrameMu.Lock()
	if generation != s.keyFrameRequestGeneration || s.keyFrameRequestTimer == nil {
		s.keyFrameMu.Unlock()
		return
	}
	s.keyFrameRequestTimer = nil
	s.keyFrameRequestDue = time.Time{}
	s.lastKeyFrameRequest = time.Now()
	s.keyFrameMu.Unlock()
	if s.isClosed() {
		return
	}
	s.issueKeyFrameRequest()
}

func (s *Session) cancelScheduledKeyFrameRequest() {
	s.keyFrameMu.Lock()
	s.cancelScheduledKeyFrameRequestLocked()
	s.keyFrameMu.Unlock()
}

func (s *Session) cancelScheduledKeyFrameRequestLocked() {
	if s.keyFrameRequestTimer == nil {
		return
	}
	s.keyFrameRequestTimer.Stop()
	s.keyFrameRequestTimer = nil
	s.keyFrameRequestDue = time.Time{}
	s.keyFrameRequestGeneration++
}

func (s *Session) issueKeyFrameRequest() {
	s.recoveryKeyFrameRequests.Add(1)
	requester, ok := s.encoder.(media.KeyFrameRequester)
	if !ok {
		s.recoveryKeyFrameFailures.Add(1)
		s.logger.Warn("Viewer %s encoder cannot request a recovery key frame", s.id)
		return
	}
	if err := requester.RequestKeyFrame(); err != nil {
		s.recoveryKeyFrameFailures.Add(1)
		s.logger.Warn("Viewer %s key-frame request failed: %v", s.id, err)
	}
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("failed to create a random session ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func sourceEncoderController(source media.Source) (media.EncoderController, bool) {
	controller, ok := source.(media.ControllableSource)
	if !ok {
		return nil, false
	}
	return controller.EncoderController()
}

func (b *Broadcaster) newAdaptiveController(
	encoder media.EncoderController,
	estimator bandwidthEstimator,
	requestRecoveryKeyFrame func(),
) (*adaptation.Controller, bool) {
	if encoder == nil || estimator == nil {
		return nil, false
	}
	backend, interval, err := adaptation.NewBackend(b.cfg)
	if err != nil {
		b.logger.Warn("Adaptive bitrate setup failed: %v", err)
		return nil, false
	}
	if backend == nil {
		return nil, false
	}
	return adaptation.NewController(
		b.logger,
		encoder,
		backend,
		interval,
		estimator.GetTargetBitrate,
		func() float64 {
			loss, _ := estimator.GetStats()["averageLoss"].(float64)
			return loss
		},
		requestRecoveryKeyFrame,
	), true
}

func (b *Broadcaster) acquireSource(ctx context.Context) (media.Source, func(), error) {
	switch b.mediaMode {
	case config.MediaModePerViewer:
		source, err := b.sourceFactory.New()
		if err != nil {
			return nil, nil, err
		}
		if err := source.Start(ctx); err != nil {
			_ = source.Close()
			return nil, nil, err
		}
		return source, func() {
			if err := source.Close(); err != nil {
				b.logger.Warn("GStreamer pipeline shutdown failed: %v", err)
			}
		}, nil
	default:
		return b.acquireSharedSource(ctx)
	}
}

func (b *Broadcaster) acquireSharedSource(ctx context.Context) (media.Source, func(), error) {
	select {
	case b.sharedInitGate <- struct{}{}:
		defer func() { <-b.sharedInitGate }()
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("shared media source initialization interrupted: %w", ctx.Err())
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, errors.New("the broadcaster is closed")
	}
	source := b.sharedSource
	if source != nil {
		b.sharedUsers++
		b.mu.Unlock()
		return source, func() {
			b.releaseSharedSource(source)
		}, nil
	}
	b.mu.Unlock()
	var err error
	source, err = b.sourceFactory.New()
	if err != nil {
		return nil, nil, err
	}
	if err := source.Start(ctx); err != nil {
		_ = source.Close()
		return nil, nil, err
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = source.Close()
		return nil, nil, errors.New("the broadcaster is closed")
	}
	b.sharedSource = source
	b.sharedUsers = 1
	b.mu.Unlock()
	return source, func() {
		b.releaseSharedSource(source)
	}, nil
}

func (b *Broadcaster) releaseSharedSource(source media.Source) {
	b.mu.Lock()
	if b.sharedSource != source {
		b.mu.Unlock()
		return
	}
	if b.sharedUsers > 0 {
		b.sharedUsers--
	}
	shouldClose := b.sharedUsers == 0 && b.sharedSource == source
	if shouldClose {
		b.sharedSource = nil
	}
	b.mu.Unlock()
	if shouldClose {
		if err := source.Close(); err != nil {
			b.logger.Warn("GStreamer pipeline shutdown failed: %v", err)
		}
	}
}

func (b *Broadcaster) reserveSession() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("the broadcaster is closed")
	}
	if b.maxViewers > 0 && len(b.sessions)+b.opening >= b.maxViewers {
		return fmt.Errorf("%w: limit is %d concurrent viewer(s)", ErrSessionCapacity, b.maxViewers)
	}
	b.opening++
	return nil
}

func (b *Broadcaster) releaseReservation() {
	b.mu.Lock()
	if b.opening > 0 {
		b.opening--
	}
	b.mu.Unlock()
}
