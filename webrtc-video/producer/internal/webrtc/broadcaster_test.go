package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

type fakeSourceFactory struct{}

type fakeBandwidthEstimator struct {
	stats map[string]any
}

type rejectingBandwidthEstimator struct {
	admitted chan media.AccessUnit
}

type recordingKeyFrameRequester struct {
	requested chan struct{}
	err       error
}

type nonRequestingEncoder struct{}

func (f fakeBandwidthEstimator) GetTargetBitrate() int {
	return 0
}

func (f fakeBandwidthEstimator) OnTargetBitrateChange(func(int)) {}

func (f fakeBandwidthEstimator) GetStats() map[string]any {
	return f.stats
}

func (r *rejectingBandwidthEstimator) GetTargetBitrate() int {
	return 0
}

func (r *rejectingBandwidthEstimator) OnTargetBitrateChange(func(int)) {}

func (r *rejectingBandwidthEstimator) GetStats() map[string]any {
	return nil
}

func (r *rejectingBandwidthEstimator) AdmitMediaFrame(size int, keyFrame bool) mediaFrameAdmission {
	r.admitted <- media.AccessUnit{Data: make([]byte, size), KeyFrame: keyFrame}
	return mediaFrameAdmission{requestKeyFrame: true}
}

func (r *recordingKeyFrameRequester) Info() media.EncoderInfo {
	return media.EncoderInfo{}
}

func (r *recordingKeyFrameRequester) SetTargetBitrateKbps(int) error {
	return nil
}

func (r *recordingKeyFrameRequester) RequestKeyFrame() error {
	if r.requested != nil {
		r.requested <- struct{}{}
	}
	return r.err
}

func (nonRequestingEncoder) Info() media.EncoderInfo {
	return media.EncoderInfo{}
}

func (nonRequestingEncoder) SetTargetBitrateKbps(int) error {
	return nil
}

func (fakeSourceFactory) New() (media.Source, error) {
	return &fakeSource{subs: make(map[chan media.AccessUnit]struct{})}, nil
}

type fakeSource struct {
	subs map[chan media.AccessUnit]struct{}
}

type closedSourceFactory struct{}

type closedSource struct{}

func (closedSourceFactory) New() (media.Source, error) {
	return closedSource{}, nil
}

func (closedSource) Start() error {
	return nil
}

func (closedSource) Stop() error {
	return nil
}

func (closedSource) Subscribe() (<-chan media.AccessUnit, func()) {
	ch := make(chan media.AccessUnit)
	close(ch)
	return ch, func() {}
}

func (closedSource) Close() error {
	return nil
}

func (s *fakeSource) Start() error {
	return nil
}

func (s *fakeSource) Stop() error {
	return nil
}

func (s *fakeSource) Subscribe() (<-chan media.AccessUnit, func()) {
	ch := make(chan media.AccessUnit, 1)
	s.subs[ch] = struct{}{}
	return ch, func() {
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *fakeSource) Close() error {
	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
	return nil
}

func TestSnapshotBandwidthStatsPreservesControllerSignals(t *testing.T) {
	stats := snapshotBandwidthStats(fakeBandwidthEstimator{stats: map[string]any{
		"lossTargetBitrate":    1_500_000,
		"averageLoss":          0.025,
		"flexFECMediaPackets":  uint32(5),
		"flexFECRepairPackets": uint32(2),
		"delayTargetBitrate":   2_400_000,
		"delayMeasurement":     12.5,
		"delayEstimate":        10.25,
		"delayThreshold":       15.75,
		"usage":                "overusing",
		"state":                "decrease",
	}})
	if stats == nil {
		t.Fatal("expected bandwidth diagnostics")
	}
	if stats.LossTargetBitrateBps != 1_500_000 || stats.DelayTargetBitrateBps != 2_400_000 {
		t.Fatalf("unexpected bitrate diagnostics: %+v", stats)
	}
	if stats.AverageLoss != 0.025 || stats.DelayMeasurementMs != 12.5 {
		t.Fatalf("unexpected loss or delay diagnostics: %+v", stats)
	}
	if stats.FlexFECMediaPackets != 5 || stats.FlexFECRepairPackets != 2 {
		t.Fatalf("unexpected FlexFEC diagnostics: %+v", stats)
	}
	if stats.Usage != "overusing" || stats.State != "decrease" {
		t.Fatalf("unexpected controller state diagnostics: %+v", stats)
	}
}

func TestSessionDropsRejectedAccessUnitBeforePacketization(t *testing.T) {
	estimator := &rejectingBandwidthEstimator{admitted: make(chan media.AccessUnit, 1)}
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		estimator: estimator,
		encoder:   encoder,
		closed:    make(chan struct{}),
	}
	samples := make(chan media.AccessUnit, 1)
	done := make(chan struct{})
	go func() {
		session.writeSamples(samples)
		close(done)
	}()
	samples <- media.AccessUnit{Data: []byte{1, 2, 3}, KeyFrame: true}
	select {
	case admitted := <-estimator.admitted:
		if len(admitted.Data) != 3 || !admitted.KeyFrame {
			t.Fatalf("unexpected admission request: %+v", admitted)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not consult media admission")
	}
	select {
	case <-encoder.requested:
	case <-time.After(time.Second):
		t.Fatal("session did not request a recovery key frame")
	}
	stats := session.StatsSnapshot()
	if stats.RecoveryKeyFrameRequests != 1 || stats.RecoveryKeyFrameFailures != 0 {
		t.Fatalf("unexpected key-frame recovery stats: %+v", stats)
	}
	close(session.closed)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session writer did not stop")
	}
}

func TestSessionReportsRecoveryKeyFrameFailures(t *testing.T) {
	logger := logs.NewLogger(logs.NewHub(8), false)
	requestError := errors.New("request rejected")
	for _, test := range []struct {
		name    string
		encoder media.EncoderController
	}{
		{name: "unsupported encoder", encoder: nonRequestingEncoder{}},
		{name: "rejected request", encoder: &recordingKeyFrameRequester{err: requestError}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &Session{id: "viewer", encoder: test.encoder, logger: logger}
			session.requestKeyFrame()
			stats := session.StatsSnapshot()
			if stats.RecoveryKeyFrameRequests != 1 || stats.RecoveryKeyFrameFailures != 1 {
				t.Fatalf("unexpected key-frame recovery stats: %+v", stats)
			}
		})
	}
}

func TestSessionCoalescesRecoveryKeyFrameRequests(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 128)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
	}
	session.requestKeyFrame()
	session.requestKeyFrame()
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("encoder requests = %d, want 1", got)
	}
	stats := session.StatsSnapshot()
	if stats.RecoveryKeyFrameRequests != 1 || stats.RecoveryKeyFrameCoalesced != 1 {
		t.Fatalf("unexpected coalesced recovery stats: %+v", stats)
	}
	session.keyFrameMu.Lock()
	session.lastKeyFrameRequest = time.Now().Add(-keyFrameRequestInterval)
	session.keyFrameMu.Unlock()
	session.requestKeyFrame()
	if got := len(encoder.requested); got != 2 {
		t.Fatalf("encoder requests after interval = %d, want 2", got)
	}
}

func TestSessionCoalescesConcurrentRecoveryKeyFrameRequests(t *testing.T) {
	const callers = 100
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, callers)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
	}
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			session.requestKeyFrame()
		}()
	}
	wait.Wait()
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("encoder requests = %d, want 1", got)
	}
	stats := session.StatsSnapshot()
	if stats.RecoveryKeyFrameRequests != 1 || stats.RecoveryKeyFrameCoalesced != callers-1 {
		t.Fatalf("unexpected concurrent recovery stats: %+v", stats)
	}
}

func TestSessionSchedulesAndCancelsRecoveryKeyFrameRetries(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 2)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
		closed:  make(chan struct{}),
	}
	session.requestRecoveryKeyFrame(10 * time.Millisecond)
	select {
	case <-encoder.requested:
	case <-time.After(time.Second):
		t.Fatal("scheduled key-frame retry did not fire")
	}
	session.requestRecoveryKeyFrame(50 * time.Millisecond)
	session.cancelScheduledKeyFrameRequest()
	select {
	case <-encoder.requested:
		t.Fatal("cancelled key-frame retry fired")
	case <-time.After(75 * time.Millisecond):
	}
	stats := session.StatsSnapshot()
	if stats.RecoveryKeyFrameRequests != 1 {
		t.Fatalf("recovery key-frame requests = %d, want 1", stats.RecoveryKeyFrameRequests)
	}
}

func TestSessionDefersNewRecoveryEpisodeWithinRequestInterval(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 2)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
		closed:  make(chan struct{}),
	}
	session.requestKeyFrame()
	<-encoder.requested
	session.requestRecoveryKeyFrame(0)
	if got := len(encoder.requested); got != 0 {
		t.Fatalf("premature deferred encoder requests = %d, want 0", got)
	}
	select {
	case <-encoder.requested:
	case <-time.After(time.Second):
		t.Fatal("rate-limited recovery episode was discarded instead of deferred")
	}
	stats := session.StatsSnapshot()
	if stats.RecoveryKeyFrameRequests != 2 {
		t.Fatalf("recovery key-frame requests = %d, want 2", stats.RecoveryKeyFrameRequests)
	}
}

func TestSessionCloseCancelsRecoveryKeyFrameRetry(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
		closed:  make(chan struct{}),
	}
	session.requestRecoveryKeyFrame(25 * time.Millisecond)
	session.Close("test complete")
	select {
	case <-encoder.requested:
		t.Fatal("key-frame retry fired after session close")
	case <-time.After(75 * time.Millisecond):
	}
}

func TestSessionForwardsRTCPKeyFrameFeedbackToEncoder(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 2)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
	}
	session.handleRTCPPackets([]rtcp.Packet{
		&rtcp.PictureLossIndication{},
		&rtcp.ReceiverReport{},
		&rtcp.FullIntraRequest{},
	})
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("encoder requests = %d, want 1 coalesced request", got)
	}
	stats := session.StatsSnapshot()
	if stats.RTCPKeyFrameRequests != 2 || stats.RecoveryKeyFrameCoalesced != 1 {
		t.Fatalf("unexpected RTCP key-frame stats: %+v", stats)
	}
}

func TestSessionCountsMalformedRTCPAndLogsOnlyOnce(t *testing.T) {
	hub := logs.NewHub(8)
	session := &Session{
		id:     "viewer",
		logger: logs.NewLogger(hub, false),
	}
	session.recordMalformedRTCP(errors.New("first invalid compound packet"))
	session.recordMalformedRTCP(errors.New("second invalid compound packet"))
	stats := session.StatsSnapshot()
	if stats.RTCPMalformedFeedback != 2 {
		t.Fatalf("malformed RTCP feedback = %d, want 2", stats.RTCPMalformedFeedback)
	}
	entries := hub.Recent()
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "first invalid compound packet") {
		t.Fatalf("unexpected malformed RTCP logs: %+v", entries)
	}
}

func TestICEPathFromCandidatesIncludesRelayTransport(t *testing.T) {
	path := icePathFromCandidates(webrtc.NewICECandidatePair(
		&webrtc.ICECandidate{
			Protocol: webrtc.ICEProtocolUDP,
			Typ:      webrtc.ICECandidateTypeRelay,
		},
		&webrtc.ICECandidate{
			Protocol: webrtc.ICEProtocolUDP,
			Typ:      webrtc.ICECandidateTypeRelay,
		},
	), &webrtc.ICECandidateStats{
		URL:           "turns:turn.example.com:5349?transport=tcp",
		RelayProtocol: "tls",
	})
	if path == nil {
		t.Fatal("expected an ICE path")
	}
	if path.LocalCandidateType != "relay" || path.RemoteCandidateType != "relay" {
		t.Fatalf("unexpected candidate types: %+v", path)
	}
	if path.LocalCandidateURL != "turns:turn.example.com:5349?transport=tcp" {
		t.Fatalf("unexpected candidate URL: %q", path.LocalCandidateURL)
	}
	if path.LocalRelayProtocol != "tls" {
		t.Fatalf("unexpected relay protocol: %q", path.LocalRelayProtocol)
	}
}

func TestICEPathFromCandidatesRejectsIncompletePair(t *testing.T) {
	if path := icePathFromCandidates(nil, nil); path != nil {
		t.Fatalf("expected no path for a nil pair, got %+v", path)
	}
	if path := icePathFromCandidates(&webrtc.ICECandidatePair{}, nil); path != nil {
		t.Fatalf("expected no path for an incomplete pair, got %+v", path)
	}
}

func TestCompleteICEPathURLUsesValidatedCredentialIndex(t *testing.T) {
	path := &ICEPathStats{LocalRelayProtocol: "dtls"}
	completeICEPathURL(path, map[string]string{
		"dtls": "turns:turn.example.com:5349?transport=udp",
	})
	if path.LocalCandidateURL != "turns:turn.example.com:5349?transport=udp" {
		t.Fatalf("unexpected completed candidate URL: %q", path.LocalCandidateURL)
	}
	path.LocalCandidateURL = "turns:reported.example.com:5349?transport=udp"
	completeICEPathURL(path, map[string]string{
		"dtls": "turns:fallback.example.com:5349?transport=udp",
	})
	if path.LocalCandidateURL != "turns:reported.example.com:5349?transport=udp" {
		t.Fatalf("candidate stats URL was overwritten: %q", path.LocalCandidateURL)
	}
}

func TestBroadcasterHonorsMaxViewers(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.MaxViewers = 1
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the first session: %v", err)
	}
	defer session.Close("test cleanup")
	if _, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	}); err == nil {
		t.Fatal("expected the second viewer to be rejected")
	}
}

func TestBroadcasterRemovesSessionWhenSourceStopsDuringOpen(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, closedSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("expected the session to close when the source stops")
	}
	deadline := time.Now().Add(time.Second)
	for {
		broadcaster.mu.Lock()
		count := len(broadcaster.sessions)
		broadcaster.mu.Unlock()
		if count == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected no active sessions, got %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionBuffersICECandidatesBeforeOffer(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	defer session.Close("test cleanup")
	mid := "0"
	line := uint16(0)
	if err := session.AddICECandidate(
		"candidate:1 1 udp 2122260223 127.0.0.1 12345 typ host",
		&mid,
		&line,
		nil,
	); err != nil {
		t.Fatalf("expected early ICE candidate to be buffered, got %v", err)
	}
	if got := len(session.pendingICE); got != 1 {
		t.Fatalf("expected one buffered candidate, got %d", got)
	}
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("failed to create offer peer: %v", err)
	}
	defer func() {
		_ = offerer.Close()
	}()
	if _, err := offerer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("failed to add offer transceiver: %v", err)
	}
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("failed to create offer: %v", err)
	}
	if err := offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("failed to set local offer: %v", err)
	}
	if err := session.HandleOffer(offer.SDP); err != nil {
		t.Fatalf("expected offer to flush buffered candidates, got %v", err)
	}
	if got := len(session.pendingICE); got != 0 {
		t.Fatalf("expected no buffered candidates after offer, got %d", got)
	}
}

func TestSessionLimitsBufferedICECandidatesBeforeOffer(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	defer session.Close("test cleanup")
	mid := "0"
	line := uint16(0)
	for i := 0; i < maxPendingICECandidates; i++ {
		candidate := "candidate:1 1 udp 2122260223 127.0.0.1 12345 typ host"
		if err := session.AddICECandidate(candidate, &mid, &line, nil); err != nil {
			t.Fatalf("expected candidate %d to be buffered, got %v", i, err)
		}
	}
	if err := session.AddICECandidate(
		"candidate:2 1 udp 2122260223 127.0.0.1 12346 typ host",
		&mid,
		&line,
		nil,
	); err == nil {
		t.Fatal("expected excess pending ICE candidate to be rejected")
	}
}

func TestSessionLimitsBufferedICECandidateBytesBeforeOffer(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("failed to create the broadcaster: %v", err)
	}
	defer func() {
		_ = broadcaster.Close()
	}()
	session, err := broadcaster.OpenSession(context.Background(), func(SignalMessage) error {
		return nil
	})
	if err != nil {
		t.Fatalf("failed to open the session: %v", err)
	}
	defer session.Close("test cleanup")
	mid := "0"
	line := uint16(0)
	candidate := "candidate:1 1 udp 2122260223 127.0.0.1 12345 typ host " +
		strings.Repeat("x", maxPendingICECandidateBytes)
	if err := session.AddICECandidate(candidate, &mid, &line, nil); err == nil {
		t.Fatal("expected oversized pending ICE candidate to be rejected")
	}
}
