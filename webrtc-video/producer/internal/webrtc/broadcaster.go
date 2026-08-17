package webrtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

type SignalMessage struct {
	Type             string        `json:"type"`
	ViewerID         string        `json:"viewerId,omitempty"`
	SDP              string        `json:"sdp,omitempty"`
	Candidate        string        `json:"candidate,omitempty"`
	SDPMid           *string       `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16       `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string       `json:"usernameFragment,omitempty"`
	Message          string        `json:"message,omitempty"`
	Stats            *SessionStats `json:"stats,omitempty"`
}

type SessionStats struct {
	Codec                     string                 `json:"codec"`
	TWCCEnabled               bool                   `json:"twccEnabled"`
	NACKEnabled               bool                   `json:"nackEnabled"`
	RTXEnabled                bool                   `json:"rtxEnabled"`
	FlexFECEnabled            bool                   `json:"flexFECEnabled"`
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
	LossTargetBitrateBps         int     `json:"lossTargetBitrateBps"`
	DelayTargetBitrateBps        int     `json:"delayTargetBitrateBps"`
	AverageLoss                  float64 `json:"averageLoss"`
	FlexFECMediaPackets          uint32  `json:"flexFECMediaPackets"`
	FlexFECRepairPackets         uint32  `json:"flexFECRepairPackets"`
	DelayMeasurementMs           float64 `json:"delayMeasurementMs"`
	DelayEstimateMs              float64 `json:"delayEstimateMs"`
	DelayThresholdMs             float64 `json:"delayThresholdMs"`
	Usage                        string  `json:"usage"`
	State                        string  `json:"state"`
	LossGuardActive              bool    `json:"lossGuardActive"`
	LossGuardTargetBitrateBps    int     `json:"lossGuardTargetBitrateBps"`
	LossGuardLastObservedLoss    float64 `json:"lossGuardLastObservedLoss"`
	LossGuardReductions          uint64  `json:"lossGuardReductions"`
	LossGuardRecoveries          uint64  `json:"lossGuardRecoveries"`
	PacerTargetBitrateBps        int     `json:"pacerTargetBitrateBps"`
	PacerPacingBitrateBps        int     `json:"pacerPacingBitrateBps"`
	PacerQueuePackets            int     `json:"pacerQueuePackets"`
	PacerQueueDrops              uint64  `json:"pacerQueueDrops"`
	PacerQueueDelayMs            float64 `json:"pacerQueueDelayMs"`
	PacerMaximumDelayMs          float64 `json:"pacerMaximumDelayMs"`
	PacerMaximumPrimaryDelayMs   float64 `json:"pacerMaximumPrimaryDelayMs"`
	PacerMaximumRepairDelayMs    float64 `json:"pacerMaximumRepairDelayMs"`
	PacerMaximumRTXDelayMs       float64 `json:"pacerMaximumRTXDelayMs"`
	PacerMaximumFECDelayMs       float64 `json:"pacerMaximumFECDelayMs"`
	PacerMaximumSustainedDelayMs float64 `json:"pacerMaximumSustainedDelayMs"`
	PacerMaximumAdmittedDelayMs  float64 `json:"pacerMaximumAdmittedDelayMs"`
	PacerKeyFrameReserveBytes    int64   `json:"pacerKeyFrameReserveBytes"`
	PacerMediaFrameDrops         uint64  `json:"pacerMediaFrameDrops"`
	PacerMediaByteDrops          uint64  `json:"pacerMediaByteDrops"`
	PacerRepairPacketsExpired    uint64  `json:"pacerRepairPacketsExpired"`
	PacerRepairPacketsTrimmed    uint64  `json:"pacerRepairPacketsTrimmed"`
	PacerRTXPacketsExpired       uint64  `json:"pacerRTXPacketsExpired"`
	PacerRTXPacketsCoalesced     uint64  `json:"pacerRTXPacketsCoalesced"`
	PacerFECPacketsExpired       uint64  `json:"pacerFECPacketsExpired"`
	PacerRTXPacketsTrimmed       uint64  `json:"pacerRTXPacketsTrimmed"`
	PacerFECPacketsTrimmed       uint64  `json:"pacerFECPacketsTrimmed"`
	PacerSentPrimary             uint64  `json:"pacerSentPrimary"`
	PacerSentPrimaryBytes        uint64  `json:"pacerSentPrimaryBytes"`
	PacerSentRepair              uint64  `json:"pacerSentRepair"`
	PacerSentRTX                 uint64  `json:"pacerSentRTX"`
	PacerSentRTXBytes            uint64  `json:"pacerSentRTXBytes"`
	PacerSentFEC                 uint64  `json:"pacerSentFEC"`
	PacerSentFECBytes            uint64  `json:"pacerSentFECBytes"`
	StaleBitrateCallbacks        uint64  `json:"staleBitrateCallbacks"`
	TWCCFeedbackPackets          uint64  `json:"twccFeedbackPackets"`
	TWCCMalformedFeedback        uint64  `json:"twccMalformedFeedback"`
	TWCCPaddingStatuses          uint64  `json:"twccPaddingStatuses"`
	TWCCReportedLost             uint64  `json:"twccReportedLost"`
	TWCCReportedStatuses         uint64  `json:"twccReportedStatuses"`
}

type Broadcaster struct {
	cfg           config.Config
	logger        *logs.Logger
	sourceFactory media.Factory
	sharedSource  media.Source
	sharedUsers   int
	turn          *turnprovider.Provider
	peerFactory   *peerConnectionFactory
	codec         webrtc.RTPCodecCapability
	streamID      string
	trackID       string
	useTURN       bool
	mediaMode     config.MediaMode
	maxViewers    int
	mu            sync.Mutex
	sessions      map[string]*Session
	retired       producerTotals
	opening       int
	closed        bool
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
	send                      func(SignalMessage) error
	close                     sync.Once
	closed                    chan struct{}
	onClose                   func(string)
	statsMu                   sync.RWMutex
	stats                     SessionStats
	signalingMu               sync.Mutex
	pendingICE                []webrtc.ICECandidateInit
	pendingBytes              int
	candidateMu               sync.Mutex
	localICE                  candidateCounts
	remoteICE                 candidateCounts
	recoveryKeyFrameRequests  atomic.Uint64
	recoveryKeyFrameCoalesced atomic.Uint64
	recoveryKeyFrameFailures  atomic.Uint64
	rtcpKeyFrameRequests      atomic.Uint64
	rtcpMalformedFeedback     atomic.Uint64
	malformedRTCPLog          sync.Once
	keyFrameMu                sync.Mutex
	lastKeyFrameRequest       time.Time
	keyFrameRequestTimer      *time.Timer
	keyFrameRequestDue        time.Time
	keyFrameRequestGeneration uint64
	recoveryMu                sync.Mutex
	recovery                  *time.Timer
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
	networkRecoveryTimeout      = 30 * time.Second
	keyFrameRequestInterval     = 250 * time.Millisecond
	maxPendingICECandidates     = 64
	maxPendingICECandidateBytes = 64 * 1024
)

func NewBroadcaster(cfg config.Config, sourceFactory media.Factory, turn *turnprovider.Provider, logger *logs.Logger) (*Broadcaster, error) {
	peerFactory, codec, err := newPeerConnectionFactory(cfg)
	if err != nil {
		return nil, err
	}
	return &Broadcaster{
		cfg:           cfg,
		logger:        logger,
		sourceFactory: sourceFactory,
		turn:          turn,
		peerFactory:   peerFactory,
		codec:         codec,
		streamID:      cfg.WebRTC.Video.StreamID,
		trackID:       cfg.WebRTC.Video.TrackID,
		useTURN:       cfg.WebRTC.UseTURN,
		mediaMode:     cfg.MediaMode(),
		maxViewers:    cfg.WebRTC.MaxViewers,
		sessions:      make(map[string]*Session),
	}, nil
}

func (b *Broadcaster) OpenSession(ctx context.Context, send func(SignalMessage) error) (*Session, error) {
	if err := b.reserveSession(); err != nil {
		return nil, err
	}
	releaseReservation := true
	defer func() {
		if releaseReservation {
			b.releaseReservation()
		}
	}()
	source, release, err := b.acquireSource()
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
		id:          sessionID,
		logger:      b.logger,
		pc:          peerConnection,
		track:       track,
		sender:      sender,
		unsubscribe: unsubscribe,
		release:     release,
		estimator:   estimator,
		encoder:     encoderController,
		send:        send,
		closed:      make(chan struct{}),
		turnURLs:    turnURLs,
		stats: SessionStats{
			Codec:           b.codec.MimeType,
			TWCCEnabled:     b.cfg.WebRTC.Interceptors.TWCC,
			NACKEnabled:     b.cfg.WebRTC.Interceptors.NACK,
			RTXEnabled:      b.cfg.WebRTC.Interceptors.RTX,
			FlexFECEnabled:  b.cfg.WebRTC.Interceptors.FlexFEC,
			AdaptiveBackend: b.cfg.AdaptiveBackend(),
		},
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
		if err := send(SignalMessage{
			Type:          "webrtc.candidate",
			Candidate:     init.Candidate,
			SDPMid:        trimmedStringPtr(init.SDPMid),
			SDPMLineIndex: init.SDPMLineIndex,
		}); err != nil {
			b.logger.Warn("Viewer %s signaling write failed: %v", session.id, err)
			session.Close("signaling write failed")
		}
	})
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		b.logger.Info("Viewer %s peer connection state: %s", session.id, state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			session.clearNetworkRecovery()
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
		session.requestKeyFrame,
	); ok {
		session.adaptive = adaptiveController
		snapshot := adaptiveController.Snapshot()
		session.updateStats(func(stats *SessionStats) {
			stats.AdaptiveActive = snapshot.Active
			stats.EncoderTargetBitrateKbps = snapshot.EncoderTargetBitrateKbps
			stats.LastAppliedBitrateKbps = snapshot.LastAppliedBitrateKbps
		})
		adaptiveController.Start()
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
	go session.pushStats()
	b.logger.Info("Viewer %s connected (active viewers: %d)", session.id, count)
	return session, nil
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

func (s *Session) HandleOffer(offer string) error {
	if strings.TrimSpace(offer) == "" {
		return errors.New("offer SDP is required")
	}
	s.signalingMu.Lock()
	defer s.signalingMu.Unlock()
	// The browser may send more than one offer on the same session when it
	// performs an ICE restart after a network interface or IP change.
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	}); err != nil {
		return fmt.Errorf("failed to apply the remote offer: %w", err)
	}
	if err := s.flushPendingICECandidates(); err != nil {
		return err
	}
	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create the answer: %w", err)
	}
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set the local answer: %w", err)
	}
	if err := s.send(SignalMessage{
		Type: "webrtc.answer",
		SDP:  answer.SDP,
	}); err != nil {
		return err
	}
	return nil
}

func (s *Session) AddICECandidate(
	candidate string,
	sdpMid *string,
	sdpMLineIndex *uint16,
	usernameFragment *string,
) error {
	if strings.TrimSpace(candidate) == "" {
		return errors.New("candidate is required")
	}
	s.recordRemoteICECandidate(candidate)
	init := webrtc.ICECandidateInit{
		Candidate:        candidate,
		SDPMid:           trimmedStringPtr(sdpMid),
		SDPMLineIndex:    sdpMLineIndex,
		UsernameFragment: trimmedStringPtr(usernameFragment),
	}
	s.signalingMu.Lock()
	defer s.signalingMu.Unlock()
	if s.pc.RemoteDescription() == nil {
		if len(s.pendingICE) >= maxPendingICECandidates {
			return errors.New("too many pending ICE candidates")
		}
		if s.pendingBytes+len(candidate) > maxPendingICECandidateBytes {
			return errors.New("too many pending ICE candidates")
		}
		s.pendingICE = append(s.pendingICE, init)
		s.pendingBytes += len(candidate)
		return nil
	}
	return s.pc.AddICECandidate(init)
}

func (s *Session) flushPendingICECandidates() error {
	for len(s.pendingICE) > 0 {
		candidate := s.pendingICE[0]
		s.pendingICE = s.pendingICE[1:]
		candidateBytes := len(candidate.Candidate)
		if s.pendingBytes >= candidateBytes {
			s.pendingBytes -= candidateBytes
		} else {
			s.pendingBytes = 0
		}
		if err := s.pc.AddICECandidate(candidate); err != nil {
			s.pendingICE = nil
			s.pendingBytes = 0
			return fmt.Errorf("failed to apply a buffered ICE candidate: %w", err)
		}
	}
	s.pendingBytes = 0
	return nil
}

func (s *Session) Close(reason string) {
	s.close.Do(func() {
		s.clearNetworkRecovery()
		s.cancelScheduledKeyFrameRequest()
		close(s.closed)
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
	completeICEPathURL(path, s.turnURLs)
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
	stats.PacerMaximumRTXDelayMs, _ = raw["pacerMaximumRetransmissionResidenceMilliseconds"].(float64)
	stats.PacerMaximumFECDelayMs, _ = raw["pacerMaximumForwardErrorCorrectionResidenceMilliseconds"].(float64)
	stats.PacerMaximumSustainedDelayMs, _ = raw["pacerMaximumSustainedDelayMilliseconds"].(float64)
	stats.PacerMaximumAdmittedDelayMs, _ = raw["pacerMaximumAdmittedSustainedDelayMilliseconds"].(float64)
	stats.PacerKeyFrameReserveBytes, _ = raw["pacerKeyFrameReserveBytes"].(int64)
	stats.PacerMediaFrameDrops, _ = raw["pacerMediaFramesDropped"].(uint64)
	stats.PacerMediaByteDrops, _ = raw["pacerMediaBytesDropped"].(uint64)
	stats.PacerRepairPacketsExpired, _ = raw["pacerRepairPacketsExpired"].(uint64)
	stats.PacerRepairPacketsTrimmed, _ = raw["pacerRepairPacketsTrimmed"].(uint64)
	stats.PacerRTXPacketsExpired, _ = raw["pacerRetransmissionPacketsExpired"].(uint64)
	stats.PacerRTXPacketsCoalesced, _ = raw["pacerRetransmissionPacketsCoalesced"].(uint64)
	stats.PacerFECPacketsExpired, _ = raw["pacerForwardErrorCorrectionPacketsExpired"].(uint64)
	stats.PacerRTXPacketsTrimmed, _ = raw["pacerRetransmissionPacketsTrimmed"].(uint64)
	stats.PacerFECPacketsTrimmed, _ = raw["pacerForwardErrorCorrectionPacketsTrimmed"].(uint64)
	stats.PacerSentPrimary, _ = raw["pacerSentPrimary"].(uint64)
	stats.PacerSentPrimaryBytes, _ = raw["pacerSentPrimaryBytes"].(uint64)
	stats.PacerSentRepair, _ = raw["pacerSentRepair"].(uint64)
	stats.PacerSentRTX, _ = raw["pacerSentRetransmission"].(uint64)
	stats.PacerSentRTXBytes, _ = raw["pacerSentRetransmissionBytes"].(uint64)
	stats.PacerSentFEC, _ = raw["pacerSentForwardErrorCorrection"].(uint64)
	stats.PacerSentFECBytes, _ = raw["pacerSentForwardErrorCorrectionBytes"].(uint64)
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

func (s *Session) pushStats() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.ensureSelectedICEPath()
			if err := s.send(SignalMessage{
				Type:  "session.stats",
				Stats: ptrSessionStats(s.StatsSnapshot()),
			}); err != nil {
				s.logger.Warn("Viewer %s stats write failed: %v", s.id, err)
				s.Close("signaling write failed")
				return
			}
		case <-s.closed:
			return
		}
	}
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
	for _, packet := range packets {
		switch packet.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			s.rtcpKeyFrameRequests.Add(1)
			s.requestKeyFrame()
		}
	}
}

func (s *Session) writeSamples(samples <-chan media.AccessUnit) {
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
			decision := mediaFrameAdmission{admitted: true}
			if admission, ok := s.estimator.(interface {
				AdmitMediaFrame(int, bool) mediaFrameAdmission
			}); ok {
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
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("failed to create a random session ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func trimmedStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
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

func ptrSessionStats(stats SessionStats) *SessionStats {
	return &stats
}

func (b *Broadcaster) acquireSource() (media.Source, func(), error) {
	switch b.mediaMode {
	case config.MediaModePerViewer:
		source, err := b.sourceFactory.New()
		if err != nil {
			return nil, nil, err
		}
		if err := source.Start(); err != nil {
			_ = source.Close()
			return nil, nil, err
		}
		return source, func() {
			if err := source.Close(); err != nil {
				b.logger.Warn("GStreamer pipeline shutdown failed: %v", err)
			}
		}, nil
	default:
		return b.acquireSharedSource()
	}
}

func (b *Broadcaster) acquireSharedSource() (media.Source, func(), error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, errors.New("the broadcaster is closed")
	}
	source := b.sharedSource
	if source == nil {
		var err error
		source, err = b.sourceFactory.New()
		if err != nil {
			b.mu.Unlock()
			return nil, nil, err
		}
		b.sharedSource = source
	}
	b.sharedUsers++
	b.mu.Unlock()
	if err := source.Start(); err != nil {
		b.mu.Lock()
		if b.sharedUsers > 0 {
			b.sharedUsers--
		}
		if b.sharedSource == source {
			b.sharedSource = nil
		}
		b.mu.Unlock()
		_ = source.Close()
		return nil, nil, err
	}
	return source, func() {
		b.releaseSharedSource(source)
	}, nil
}

func (b *Broadcaster) releaseSharedSource(source media.Source) {
	b.mu.Lock()
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
		return fmt.Errorf("the server is limited to %d concurrent viewer(s)", b.maxViewers)
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
