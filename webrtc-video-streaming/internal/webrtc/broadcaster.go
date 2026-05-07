package webrtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	rtcmedia "github.com/pion/webrtc/v4/pkg/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/adaptation"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/media"
	turnprovider "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/turn"
	"github.com/rstreamlabs/rstream-go"
)

type SignalMessage struct {
	Type          string        `json:"type"`
	ViewerID      string        `json:"viewerId,omitempty"`
	SDP           string        `json:"sdp,omitempty"`
	Candidate     string        `json:"candidate,omitempty"`
	SDPMid        *string       `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16       `json:"sdpMLineIndex,omitempty"`
	Message       string        `json:"message,omitempty"`
	Stats         *SessionStats `json:"stats,omitempty"`
}

type SessionStats struct {
	Codec                    string                 `json:"codec"`
	TWCCEnabled              bool                   `json:"twccEnabled"`
	NACKEnabled              bool                   `json:"nackEnabled"`
	RTXEnabled               bool                   `json:"rtxEnabled"`
	FlexFECEnabled           bool                   `json:"flexFECEnabled"`
	AdaptiveBackend          config.AdaptiveBackend `json:"adaptiveBackend"`
	AdaptiveActive           bool                   `json:"adaptiveActive"`
	EstimatedBitrateBps      int                    `json:"estimatedBitrateBps"`
	EncoderTargetBitrateKbps int                    `json:"encoderTargetBitrateKbps"`
	LastAppliedBitrateKbps   int                    `json:"lastAppliedBitrateKbps"`
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
	opening       int
	closed        bool
}

type Session struct {
	id          string
	logger      *logs.Logger
	pc          *webrtc.PeerConnection
	track       *webrtc.TrackLocalStaticSample
	sender      *webrtc.RTPSender
	unsubscribe func()
	release     func()
	estimator   bandwidthEstimator
	encoder     media.EncoderController
	adaptive    *adaptation.Controller
	send        func(SignalMessage) error
	close       sync.Once
	closed      chan struct{}
	onClose     func(string)
	statsMu     sync.RWMutex
	stats       SessionStats
	recoveryMu  sync.Mutex
	recovery    *time.Timer
}

const networkRecoveryTimeout = 30 * time.Second

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
	if b.useTURN {
		credentials, err = b.turn.Credentials(ctx)
		if err != nil {
			return nil, err
		}
	}
	initialBitrateBps := b.cfg.InitialBitrateKbps() * 1000
	if encoderController != nil {
		info := encoderController.Info()
		if info.TargetBitrateKbps > 0 {
			initialBitrateBps = info.TargetBitrateKbps * 1000
		}
	}
	peerConnection, estimator, err := b.peerFactory.NewPeerConnection(initialBitrateBps, turnprovider.ICEConfig(credentials))
	if err != nil {
		return nil, fmt.Errorf("failed to create the peer connection: %w", err)
	}
	sessionID, err := randomID()
	if err != nil {
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
		b.mu.Lock()
		delete(b.sessions, session.id)
		count := len(b.sessions)
		b.mu.Unlock()
		b.logger.Info("Viewer %s closed: %s (active viewers: %d)", session.id, reason, count)
	}
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		init := candidate.ToJSON()
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
	if adaptiveController, ok := b.newAdaptiveController(encoderController); ok {
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
	// The browser may send more than one offer on the same session when it
	// performs an ICE restart after a network interface or IP change.
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	}); err != nil {
		return fmt.Errorf("failed to apply the remote offer: %w", err)
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

func (s *Session) AddICECandidate(candidate string, sdpMid *string, sdpMLineIndex *uint16) error {
	if strings.TrimSpace(candidate) == "" {
		return errors.New("candidate is required")
	}
	return s.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate:     candidate,
		SDPMid:        trimmedStringPtr(sdpMid),
		SDPMLineIndex: sdpMLineIndex,
	})
}

func (s *Session) Close(reason string) {
	s.close.Do(func() {
		s.clearNetworkRecovery()
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
		s.recovery.Stop()
	}
	s.logger.Warn(
		"Viewer %s network path changed (%s); waiting up to %s for ICE recovery",
		s.id,
		reason,
		networkRecoveryTimeout,
	)
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

func (s *Session) StatsSnapshot() SessionStats {
	s.statsMu.RLock()
	stats := s.stats
	s.statsMu.RUnlock()
	if s.adaptive == nil {
		return stats
	}
	snapshot := s.adaptive.Snapshot()
	stats.AdaptiveActive = snapshot.Active
	stats.EstimatedBitrateBps = snapshot.EstimatedBitrateBps
	stats.EncoderTargetBitrateKbps = snapshot.EncoderTargetBitrateKbps
	stats.LastAppliedBitrateKbps = snapshot.LastAppliedBitrateKbps
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
		if _, _, err := s.sender.Read(buffer); err != nil {
			return
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
			if err := s.track.WriteSample(rtcmedia.Sample{
				Data:     unit.Data,
				Duration: unit.Duration,
			}); err != nil {
				s.logger.Warn("Viewer %s media write failed: %v", s.id, err)
				s.Close("media write failed")
				return
			}
		}
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

func (b *Broadcaster) newAdaptiveController(encoder media.EncoderController) (*adaptation.Controller, bool) {
	if encoder == nil {
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
	return adaptation.NewController(b.logger, encoder, backend, interval), true
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
