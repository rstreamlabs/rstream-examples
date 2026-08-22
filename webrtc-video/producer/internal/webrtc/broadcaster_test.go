package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

type fakeSourceFactory struct{}

type fakeBandwidthEstimator struct {
	stats         map[string]any
	recoveryDelay time.Duration
}

type rejectingBandwidthEstimator struct {
	admitted chan media.AccessUnit
}

type roundTripObservingEstimator struct {
	observed chan time.Duration
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

func (f fakeBandwidthEstimator) recoveryKeyFrameDelay() time.Duration {
	return f.recoveryDelay
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

func (r *roundTripObservingEstimator) GetTargetBitrate() int {
	return 0
}

func (r *roundTripObservingEstimator) OnTargetBitrateChange(func(int)) {}

func (r *roundTripObservingEstimator) GetStats() map[string]any {
	return nil
}

func (r *roundTripObservingEstimator) observeRoundTripTime(roundTripTime time.Duration) {
	r.observed <- roundTripTime
}

func TestReceptionReportRoundTripTime(t *testing.T) {
	arrival := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	report := rtcp.ReceptionReport{
		LastSenderReport: compactNTPTime(arrival.Add(-175 * time.Millisecond)),
		Delay:            compactNTPDuration(25 * time.Millisecond),
	}
	roundTripTime, ok := receptionReportRoundTripTime(report, arrival)
	if !ok {
		t.Fatal("valid reception report did not produce an RTT")
	}
	if delta := roundTripTime - 150*time.Millisecond; delta < -time.Millisecond || delta > time.Millisecond {
		t.Fatalf("round-trip time = %v, want 150ms", roundTripTime)
	}
	if _, ok := receptionReportRoundTripTime(rtcp.ReceptionReport{}, arrival); ok {
		t.Fatal("report without a last sender report produced an RTT")
	}
	invalid := rtcp.ReceptionReport{
		LastSenderReport: compactNTPTime(arrival.Add(-10 * time.Millisecond)),
		Delay:            compactNTPDuration(100 * time.Millisecond),
	}
	if _, ok := receptionReportRoundTripTime(invalid, arrival); ok {
		t.Fatal("report with a delay beyond its arrival interval produced an RTT")
	}
}

func TestSessionUsesShortestValidReceptionReportRTT(t *testing.T) {
	const mediaSSRC = 42
	arrival := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	estimator := &roundTripObservingEstimator{observed: make(chan time.Duration, 1)}
	session := &Session{estimator: estimator}
	session.observeRoundTripTime([]rtcp.ReceptionReport{
		{
			SSRC:             mediaSSRC,
			LastSenderReport: compactNTPTime(arrival.Add(-250 * time.Millisecond)),
			Delay:            compactNTPDuration(50 * time.Millisecond),
		},
		{
			SSRC:             mediaSSRC + 1,
			LastSenderReport: compactNTPTime(arrival.Add(-125 * time.Millisecond)),
			Delay:            compactNTPDuration(25 * time.Millisecond),
		},
		{
			SSRC:             mediaSSRC,
			LastSenderReport: compactNTPTime(arrival.Add(-175 * time.Millisecond)),
			Delay:            compactNTPDuration(25 * time.Millisecond),
		},
	}, mediaSSRC, arrival)
	select {
	case observed := <-estimator.observed:
		if delta := observed - 150*time.Millisecond; delta < -time.Millisecond || delta > time.Millisecond {
			t.Fatalf("observed RTT = %v, want 150ms", observed)
		}
	default:
		t.Fatal("session did not forward the reception report RTT")
	}
}

func TestSessionUsesCachedOutboundMediaSSRCWithoutSenderLookup(t *testing.T) {
	session := &Session{}
	session.mediaSSRC.Store(42)
	if mediaSSRC := session.outboundMediaSSRC(); mediaSSRC != 42 {
		t.Fatalf("outbound media SSRC = %d, want 42", mediaSSRC)
	}
}

func compactNTPDuration(value time.Duration) uint32 {
	return uint32(uint64(value) * (1 << 16) / uint64(time.Second))
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

func TestSessionCloseCannotLeaveANetworkRecoveryTimer(t *testing.T) {
	logger := logs.NewLogger(logs.NewHub(16), false)
	const iterations = 1000
	for iteration := 0; iteration < iterations; iteration++ {
		session := &Session{id: "test", logger: logger, closed: make(chan struct{})}
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			session.scheduleNetworkRecovery("test")
		}()
		go func() {
			defer workers.Done()
			<-start
			session.Close("test")
		}()
		close(start)
		workers.Wait()
		session.recoveryMu.Lock()
		recovery := session.recovery
		session.recoveryMu.Unlock()
		if recovery != nil {
			recovery.Stop()
			t.Fatalf("iteration %d retained a recovery timer after close", iteration)
		}
	}
}

func (fakeSourceFactory) New() (media.Source, error) {
	return &fakeSource{subs: make(map[chan media.AccessUnit]struct{})}, nil
}

type fakeSource struct {
	subs map[chan media.AccessUnit]struct{}
}

type lifecycleTestSource struct {
	startEntered chan struct{}
	startRelease <-chan struct{}
	startErr     error
	startOnce    sync.Once
	mu           sync.Mutex
	starts       int
	closes       int
}

type sequenceSourceFactory struct {
	mu      sync.Mutex
	sources []*lifecycleTestSource
	calls   int
}

func (f *sequenceSourceFactory) New() (media.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.sources) {
		return nil, errors.New("test source sequence exhausted")
	}
	source := f.sources[f.calls]
	f.calls++
	return source, nil
}

func (s *lifecycleTestSource) Start(context.Context) error {
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	s.startOnce.Do(func() {
		if s.startEntered != nil {
			close(s.startEntered)
		}
	})
	if s.startRelease != nil {
		<-s.startRelease
	}
	return s.startErr
}

func (s *lifecycleTestSource) Stop() error {
	return nil
}

func (s *lifecycleTestSource) Subscribe() (<-chan media.AccessUnit, func()) {
	return make(chan media.AccessUnit), func() {}
}

func (s *lifecycleTestSource) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *lifecycleTestSource) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts, s.closes
}

type closedSourceFactory struct{}

type closedSource struct{}

func (closedSourceFactory) New() (media.Source, error) {
	return closedSource{}, nil
}

func (closedSource) Start(context.Context) error {
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

func (s *fakeSource) Start(context.Context) error {
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

func TestSharedSourceInitializationDoesNotBlockBroadcasterClose(t *testing.T) {
	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	source := &lifecycleTestSource{startEntered: startEntered, startRelease: startRelease}
	factory := &sequenceSourceFactory{sources: []*lifecycleTestSource{source}}
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	broadcaster, err := NewBroadcaster(cfg, factory, nil, logs.NewLogger(logs.NewHub(16), false))
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	acquired := make(chan error, 1)
	go func() {
		_, _, err := broadcaster.acquireSharedSource(context.Background())
		acquired <- err
	}()
	<-startEntered
	closed := make(chan error, 1)
	go func() { closed <- broadcaster.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close broadcaster: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("broadcaster close blocked behind source startup")
	}
	close(startRelease)
	if err := <-acquired; err == nil || err.Error() != "the broadcaster is closed" {
		t.Fatalf("acquire error = %v, want broadcaster closed", err)
	}
	starts, closes := source.counts()
	if starts != 1 || closes != 1 {
		t.Fatalf("source lifecycle = %d start(s), %d close(s); want 1, 1", starts, closes)
	}
}

func TestSharedSourceInitializationFailureIsRetriedSerially(t *testing.T) {
	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	startFailure := errors.New("pipeline startup failed")
	failedSource := &lifecycleTestSource{startEntered: startEntered, startRelease: startRelease, startErr: startFailure}
	replacementSource := &lifecycleTestSource{}
	factory := &sequenceSourceFactory{sources: []*lifecycleTestSource{failedSource, replacementSource}}
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	broadcaster, err := NewBroadcaster(cfg, factory, nil, logs.NewLogger(logs.NewHub(16), false))
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	t.Cleanup(func() { _ = broadcaster.Close() })
	first := make(chan error, 1)
	second := make(chan error, 1)
	secondRelease := make(chan func(), 1)
	go func() {
		_, _, err := broadcaster.acquireSharedSource(context.Background())
		first <- err
	}()
	<-startEntered
	go func() {
		_, release, err := broadcaster.acquireSharedSource(context.Background())
		if err == nil {
			secondRelease <- release
		}
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("second acquire bypassed active initialization: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(startRelease)
	if err := <-first; !errors.Is(err, startFailure) {
		t.Fatalf("first acquire error = %v, want %v", err, startFailure)
	}
	if err := <-second; err != nil {
		t.Fatalf("replacement acquire: %v", err)
	}
	(<-secondRelease)()
	failedStarts, failedCloses := failedSource.counts()
	replacementStarts, replacementCloses := replacementSource.counts()
	if failedStarts != 1 || failedCloses != 1 {
		t.Fatalf("failed source lifecycle = %d start(s), %d close(s); want 1, 1", failedStarts, failedCloses)
	}
	if replacementStarts != 1 || replacementCloses != 1 {
		t.Fatalf("replacement lifecycle = %d start(s), %d close(s); want 1, 1", replacementStarts, replacementCloses)
	}
}

func TestSharedSourceInitializationWaitHonorsCancellation(t *testing.T) {
	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	source := &lifecycleTestSource{startEntered: startEntered, startRelease: startRelease}
	factory := &sequenceSourceFactory{sources: []*lifecycleTestSource{source}}
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	broadcaster, err := NewBroadcaster(cfg, factory, nil, logs.NewLogger(logs.NewHub(16), false))
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	type acquisition struct {
		release func()
		err     error
	}
	first := make(chan acquisition, 1)
	go func() {
		_, release, acquireErr := broadcaster.acquireSharedSource(context.Background())
		first <- acquisition{release: release, err: acquireErr}
	}()
	<-startEntered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = broadcaster.acquireSharedSource(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v, want context cancellation", err)
	}
	close(startRelease)
	result := <-first
	if result.err != nil {
		t.Fatalf("first acquire: %v", result.err)
	}
	result.release()
	if err := broadcaster.Close(); err != nil {
		t.Fatalf("close broadcaster: %v", err)
	}
	factory.mu.Lock()
	calls := factory.calls
	factory.mu.Unlock()
	if calls != 1 {
		t.Fatalf("source factory calls = %d, want 1", calls)
	}
}

func TestStaleSharedSourceReleaseCannotDecrementReplacementUsers(t *testing.T) {
	stale := &lifecycleTestSource{}
	replacement := &lifecycleTestSource{}
	broadcaster := &Broadcaster{sharedSource: replacement, sharedUsers: 1}
	broadcaster.releaseSharedSource(stale)
	broadcaster.mu.Lock()
	users := broadcaster.sharedUsers
	current := broadcaster.sharedSource
	broadcaster.mu.Unlock()
	if users != 1 || current != replacement {
		t.Fatalf("replacement state changed after stale release: users=%d current=%p", users, current)
	}
	_, staleCloses := stale.counts()
	_, replacementCloses := replacement.counts()
	if staleCloses != 0 || replacementCloses != 0 {
		t.Fatalf("stale release closed a source: stale=%d replacement=%d", staleCloses, replacementCloses)
	}
}

func TestWHEPTrickleICEAndRestartKeepOnePeerConnection(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.Adaptive.Enabled = false
	cfg.Web.WHEP.RequireConfiguredFeatures = false
	logger := logs.NewLogger(logs.NewHub(32), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	defer func() { _ = broadcaster.Close() }()
	session, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("open WHEP session: %v", err)
	}
	defer session.Close("test complete")
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{BundlePolicy: webrtc.BundlePolicyMaxBundle})
	if err != nil {
		t.Fatalf("create WHEP client: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add WHEP receive transceiver: %v", err)
	}
	connected := make(chan struct{})
	var connectedOnce sync.Once
	client.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			connectedOnce.Do(func() { close(connected) })
		}
	})
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create WHEP offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set WHEP local offer: %v", err)
	}
	whepOffer := addRTCPMuxOnly(offer.SDP)
	offerCtx, cancelOffer := context.WithTimeout(context.Background(), 5*time.Second)
	answer, err := session.HandleWHEPOffer(offerCtx, whepOffer)
	cancelOffer()
	if err != nil {
		t.Fatalf("handle WHEP offer: %v", err)
	}
	if !strings.Contains(answer, "a=candidate:") {
		t.Fatalf("WHEP answer does not contain gathered ICE candidates:\n%s", answer)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatalf("set WHEP remote answer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("WHEP client candidate gathering timed out")
	}
	clientCandidates, err := iceFragmentFromAnswer(client.LocalDescription().SDP)
	if err != nil {
		t.Fatalf("encode client ICE candidates: %v", err)
	}
	candidateCtx, cancelCandidates := context.WithTimeout(context.Background(), 5*time.Second)
	response, err := session.HandleWHEPICE(candidateCtx, clientCandidates, false)
	cancelCandidates()
	if err != nil {
		t.Fatalf("trickle client ICE candidates: %v", err)
	} else if response != "" {
		t.Fatalf("trickle response = %q, want empty", response)
	}
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("WHEP peer connection did not connect")
	}
	initialRemote := client.RemoteDescription().SDP
	restartOffer, err := client.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		t.Fatalf("create WHEP ICE restart offer: %v", err)
	}
	restartGathered := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(restartOffer); err != nil {
		t.Fatalf("set WHEP ICE restart offer: %v", err)
	}
	select {
	case <-restartGathered:
	case <-time.After(5 * time.Second):
		t.Fatal("WHEP ICE restart candidate gathering timed out")
	}
	restartRequest, err := iceFragmentFromAnswer(client.LocalDescription().SDP)
	if err != nil {
		t.Fatalf("encode WHEP ICE restart request: %v", err)
	}
	serverRemoteBefore := session.pc.RemoteDescription().SDP
	cancelledCtx, cancelRestart := context.WithCancel(context.Background())
	cancelRestart()
	if _, err := session.HandleWHEPICE(cancelledCtx, restartRequest, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ICE restart error = %v, want context cancellation", err)
	}
	if session.isClosed() {
		t.Fatal("cancelled WHEP ICE restart closed the producer session")
	}
	if got := session.pc.RemoteDescription().SDP; got != serverRemoteBefore {
		t.Fatal("cancelled WHEP ICE restart mutated the existing ICE session")
	}
	restartCtx, cancelFinalRestart := context.WithTimeout(context.Background(), 15*time.Second)
	restartAnswer, err := session.HandleWHEPICE(restartCtx, restartRequest, true)
	cancelFinalRestart()
	if err != nil {
		t.Fatalf("handle WHEP ICE restart: %v", err)
	}
	if strings.TrimSpace(restartAnswer) == "" {
		t.Fatal("WHEP ICE restart returned no answer fragment")
	}
	answerFragment, err := parseWHEPICEFragment(restartAnswer)
	if err != nil {
		t.Fatalf("parse WHEP ICE restart answer: %v", err)
	}
	updatedAnswer, err := replaceICECredentials(initialRemote, answerFragment.ufrag, answerFragment.pwd)
	if err != nil {
		t.Fatalf("apply WHEP ICE restart answer credentials: %v", err)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: updatedAnswer}); err != nil {
		t.Fatalf("set WHEP ICE restart answer: %v", err)
	}
	for _, candidate := range answerFragment.candidates {
		if err := client.AddICECandidate(candidate); err != nil {
			t.Fatalf("apply WHEP ICE restart answer candidate: %v", err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for client.ConnectionState() != webrtc.PeerConnectionStateConnected {
		if time.Now().After(deadline) {
			t.Fatalf("WHEP peer did not recover after ICE restart: %s", client.ConnectionState())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if session.isClosed() {
		t.Fatal("WHEP ICE restart replaced or closed the producer session")
	}
}

func TestRefreshWHEPICERenewsConfigurationWithoutHoldingSessionLocks(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.Adaptive.Enabled = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	defer func() { _ = broadcaster.Close() }()
	session, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close("test complete")
	refreshes := 0
	session.refreshICEServers = func(ctx context.Context) ([]webrtc.ICEServer, map[string]string, error) {
		refreshes++
		return []webrtc.ICEServer{{URLs: []string{"stun:relay.example:3478"}}}, map[string]string{"udp": "turn:relay.example:3478?transport=udp"}, ctx.Err()
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.RefreshWHEPICE(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh error = %v, want context cancellation", err)
	}
	if refreshes != 0 {
		t.Fatalf("cancelled refresh calls = %d, want 0", refreshes)
	}
	if err := session.RefreshWHEPICE(context.Background()); err != nil {
		t.Fatalf("refresh ICE configuration: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshes)
	}
	configuration := session.pc.GetConfiguration()
	if len(configuration.ICEServers) != 1 || configuration.ICEServers[0].URLs[0] != "stun:relay.example:3478" {
		t.Fatalf("refreshed ICE configuration = %+v", configuration.ICEServers)
	}
	path := &ICEPathStats{LocalRelayProtocol: "udp"}
	session.iceConfigMu.RLock()
	completeICEPathURL(path, session.turnURLs)
	session.iceConfigMu.RUnlock()
	if path.LocalCandidateURL != "turn:relay.example:3478?transport=udp" {
		t.Fatalf("refreshed TURN URL = %q", path.LocalCandidateURL)
	}
}

func TestRefreshWHEPICEFetchesCredentialsBeforeSerializingPeerMutation(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.Adaptive.Enabled = false
	logger := logs.NewLogger(logs.NewHub(16), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	defer func() { _ = broadcaster.Close() }()
	session, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close("test complete")
	fetched := make(chan struct{})
	session.refreshICEServers = func(context.Context) ([]webrtc.ICEServer, map[string]string, error) {
		close(fetched)
		return nil, nil, nil
	}
	session.signalingMu.Lock()
	done := make(chan error, 1)
	go func() { done <- session.RefreshWHEPICE(context.Background()) }()
	select {
	case <-fetched:
	case <-time.After(time.Second):
		session.signalingMu.Unlock()
		t.Fatal("credential fetch waited for the signaling lock")
	}
	select {
	case err := <-done:
		session.signalingMu.Unlock()
		t.Fatalf("peer mutation bypassed signaling serialization: %v", err)
	default:
	}
	session.signalingMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("refresh ICE configuration: %v", err)
	}
}

func TestWHEPOfferAllowsOnlyTheBoundedMediaMTXTransportDowngrade(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.UseTURN = false
	cfg.WebRTC.Adaptive.Enabled = false
	cfg.WebRTC.Interceptors.FlexFEC = true
	cfg.WebRTC.Interceptors.FlexFECMediaPackets = 5
	cfg.WebRTC.Interceptors.FlexFECRepairPackets = 1
	cfg.Web.WHEP.AllowMediaMTXNativeOffer = true
	logger := logs.NewLogger(logs.NewHub(32), false)
	broadcaster, err := NewBroadcaster(cfg, fakeSourceFactory{}, nil, logger)
	if err != nil {
		t.Fatalf("create broadcaster: %v", err)
	}
	defer func() { _ = broadcaster.Close() }()
	session, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("open WHEP session: %v", err)
	}
	defer session.Close("test complete")
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		PayloadType: 106,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		t.Fatalf("register MediaMTX H264 codec: %v", err)
	}
	interceptors := &interceptor.Registry{}
	if err := webrtc.ConfigureNack(mediaEngine, interceptors); err != nil {
		t.Fatalf("configure MediaMTX NACK: %v", err)
	}
	if err := webrtc.ConfigureTWCCSender(mediaEngine, interceptors); err != nil {
		t.Fatalf("configure MediaMTX TWCC: %v", err)
	}
	client, err := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptors),
	).NewPeerConnection(webrtc.Configuration{BundlePolicy: webrtc.BundlePolicyMaxBundle})
	if err != nil {
		t.Fatalf("create MediaMTX-style WHEP client: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add MediaMTX video transceiver: %v", err)
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create MediaMTX WHEP offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set MediaMTX WHEP offer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("MediaMTX WHEP client candidate gathering timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	strictSession, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("open strict WHEP session: %v", err)
	}
	strictAnswer, strictErr := strictSession.HandleWHEPOffer(ctx, addRTCPMuxOnly(client.LocalDescription().SDP))
	if strictSession.mediaMTXNative.Load() {
		t.Fatal("strict WHEP offer activated the MediaMTX native source policy")
	}
	strictSession.Close("strict downgrade test complete")
	if strictErr == nil || !strings.Contains(strictErr.Error(), "rtx, flexfec") {
		t.Fatalf("strict WHEP downgrade error = %v, want missing RTX and FlexFEC", strictErr)
	}
	if strictAnswer != "" {
		t.Fatalf("rejected strict WHEP answer = %q, want empty", strictAnswer)
	}
	answer, err := session.HandleWHEPOffer(ctx, client.LocalDescription().SDP)
	if err != nil {
		t.Fatalf("handle bounded MediaMTX native WHEP offer: %v", err)
	}
	stats := session.StatsSnapshot()
	if !stats.TWCCNegotiated || !stats.NACKNegotiated {
		t.Fatalf("MediaMTX session did not negotiate TWCC and NACK: %+v", stats)
	}
	if stats.RTXNegotiated || stats.FlexFECNegotiated {
		t.Fatalf("MediaMTX session unexpectedly negotiated RTX or FlexFEC: %+v", stats)
	}
	if !session.mediaMTXNative.Load() {
		t.Fatal("MediaMTX offer did not activate the native source policy")
	}
	if stats.AdaptiveActive {
		t.Fatal("MediaMTX native source activated per-viewer adaptive encoding")
	}
	if target, ok := session.TargetBitrate(); !ok || target != cfg.InitialBitrateKbps()*1000 {
		t.Fatalf("MediaMTX native target bitrate = %d, available = %t", target, ok)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatalf("apply MediaMTX native WHEP answer: %v", err)
	}
}

func TestTransportNegotiationRequiresTWCCFeedbackAndHeaderExtension(t *testing.T) {
	parameters := webrtc.RTPSendParameters{RTPParameters: webrtc.RTPParameters{
		Codecs: []webrtc.RTPCodecParameters{
			{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: webrtc.TypeRTCPFBTransportCC}}}},
			{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX}},
			{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeFlexFEC03}},
		},
		HeaderExtensions: []webrtc.RTPHeaderExtensionParameter{{URI: transportCCHeaderExtensionURI}},
	}}
	negotiation := transportNegotiationFromParameters(parameters)
	if !negotiation.twcc || !negotiation.nack || !negotiation.rtx || !negotiation.flexFEC {
		t.Fatalf("complete transport negotiation = %+v", negotiation)
	}
	parameters.HeaderExtensions = nil
	negotiation = transportNegotiationFromParameters(parameters)
	if negotiation.twcc {
		t.Fatal("TWCC reported as negotiated without its RTP header extension")
	}
}

func TestSnapshotBandwidthStatsPreservesControllerSignals(t *testing.T) {
	stats := snapshotBandwidthStats(fakeBandwidthEstimator{stats: map[string]any{
		"lossTargetBitrate":         1_500_000,
		"averageLoss":               0.025,
		"flexFECMediaPackets":       uint32(5),
		"flexFECRepairPackets":      uint32(2),
		"delayTargetBitrate":        2_400_000,
		"delayMeasurement":          12.5,
		"delayEstimate":             10.25,
		"delayThreshold":            15.75,
		"usage":                     "overusing",
		"state":                     "decrease",
		"lossGuardActive":           true,
		"lossGuardTargetBitrate":    2_000_000,
		"lossGuardLastObservedLoss": 0.2,
		"lossGuardReductions":       uint64(3),
		"lossGuardRecoveries":       uint64(1),
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
	if !stats.LossGuardActive || stats.LossGuardTargetBitrateBps != 2_000_000 ||
		stats.LossGuardLastObservedLoss != 0.2 || stats.LossGuardReductions != 3 ||
		stats.LossGuardRecoveries != 1 {
		t.Fatalf("unexpected loss guard diagnostics: %+v", stats)
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

func TestSessionWaitsForConnectionAndKeyFrameBeforePacketization(t *testing.T) {
	estimator := &rejectingBandwidthEstimator{admitted: make(chan media.AccessUnit, 1)}
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:         "viewer",
		logger:     logs.NewLogger(logs.NewHub(8), false),
		estimator:  estimator,
		encoder:    encoder,
		closed:     make(chan struct{}),
		mediaReady: make(chan struct{}),
	}
	samples := make(chan media.AccessUnit, 3)
	done := make(chan struct{})
	go func() {
		session.writeSamples(samples)
		close(done)
	}()
	samples <- media.AccessUnit{Data: []byte{1}, KeyFrame: true}
	select {
	case <-estimator.admitted:
		t.Fatal("session packetized media before the peer connection was ready")
	case <-time.After(25 * time.Millisecond):
	}
	session.handleConnected()
	select {
	case <-encoder.requested:
	case <-time.After(time.Second):
		t.Fatal("connected session did not request its initial key frame")
	}
	samples <- media.AccessUnit{Data: []byte{2, 2}, KeyFrame: false}
	samples <- media.AccessUnit{Data: []byte{3, 3, 3}, KeyFrame: true}
	select {
	case admitted := <-estimator.admitted:
		if len(admitted.Data) != 3 || !admitted.KeyFrame {
			t.Fatalf("first admitted access unit = %+v, want the post-connect key frame", admitted)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not admit the first post-connect key frame")
	}
	session.Close("test complete")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session writer did not stop")
	}
}

func TestSessionWriterStopsWhileWaitingForConnection(t *testing.T) {
	session := &Session{
		id:         "viewer",
		logger:     logs.NewLogger(logs.NewHub(8), false),
		closed:     make(chan struct{}),
		mediaReady: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		session.writeSamples(make(chan media.AccessUnit))
		close(done)
	}()
	session.Close("test complete")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session writer did not stop before connection")
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

func TestSessionDefersCongestionRecoveryKeyFrameUntilPacerCanAdmitIt(t *testing.T) {
	const delay = 50 * time.Millisecond
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:        "viewer",
		encoder:   encoder,
		estimator: fakeBandwidthEstimator{recoveryDelay: delay},
		logger:    logs.NewLogger(logs.NewHub(8), false),
		closed:    make(chan struct{}),
	}
	defer session.Close("test complete")
	session.requestCongestionRecoveryKeyFrame()
	select {
	case <-encoder.requested:
		t.Fatal("congestion recovery key frame ignored the pacer delay")
	case <-time.After(delay / 2):
	}
	select {
	case <-encoder.requested:
	case <-time.After(2 * delay):
		t.Fatal("congestion recovery key frame did not fire after the pacer delay")
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

func TestWHEPSessionRequestsKeyFrameImmediatelyAfterConnection(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:      "viewer",
		encoder: encoder,
		logger:  logs.NewLogger(logs.NewHub(8), false),
		closed:  make(chan struct{}),
	}
	defer session.Close("test complete")
	session.whep.Store(true)
	session.handleConnected()
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("startup key-frame requests = %d, want 1 before handleConnected returns", got)
	}
	if stats := session.StatsSnapshot(); stats.RecoveryKeyFrameRequests != 1 {
		t.Fatalf("recovery key-frame requests = %d, want 1", stats.RecoveryKeyFrameRequests)
	}
}

func TestDirectSessionRequestsKeyFrameImmediatelyAfterConnection(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:         "viewer",
		encoder:    encoder,
		logger:     logs.NewLogger(logs.NewHub(8), false),
		closed:     make(chan struct{}),
		mediaReady: make(chan struct{}),
	}
	defer session.Close("test complete")
	session.handleConnected()
	select {
	case <-session.mediaReady:
	default:
		t.Fatal("direct connection did not release the media writer")
	}
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("direct startup key-frame requests = %d, want 1", got)
	}
}

func TestMediaMTXNativeSessionWaitsForReceiverFeedbackBeforeMedia(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	trackProbe := make(chan struct{}, 1)
	session := &Session{
		id:            "viewer",
		encoder:       encoder,
		logger:        logs.NewLogger(logs.NewHub(8), false),
		closed:        make(chan struct{}),
		mediaReady:    make(chan struct{}),
		receiverReady: make(chan struct{}),
		writeNativeTrackProbe: func() error {
			trackProbe <- struct{}{}
			return nil
		},
	}
	session.mediaMTXNative.Store(true)
	session.mediaSSRC.Store(42)
	defer session.Close("test complete")
	session.handleConnected()
	select {
	case <-trackProbe:
	case <-time.After(time.Second):
		t.Fatal("native session did not send the receiver track probe")
	}
	select {
	case <-session.mediaReady:
		t.Fatal("native session released media before receiver feedback")
	default:
	}
	session.handleRTCPPackets([]rtcp.Packet{
		&rtcp.ReceiverReport{Reports: []rtcp.ReceptionReport{{SSRC: 42}}},
		&rtcp.TransportLayerCC{MediaSSRC: 42},
		&rtcp.PictureLossIndication{MediaSSRC: 43},
	})
	select {
	case <-session.mediaReady:
		t.Fatal("native session accepted transport feedback as receiver readiness")
	default:
	}
	session.handleRTCPPackets([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: 42}})
	select {
	case <-session.mediaReady:
	case <-time.After(time.Second):
		t.Fatal("native session did not release media after receiver feedback")
	}
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("native startup key-frame requests = %d, want 1", got)
	}
}

func TestMediaMTXNativeTrackProbeIsOneNonVCLH264Packet(t *testing.T) {
	payloads := (&codecs.H264Payloader{}).Payload(1200, []byte(nativeMediaMTXTrackProbe))
	if len(payloads) != 1 {
		t.Fatalf("native MediaMTX track probe RTP payloads = %d, want 1", len(payloads))
	}
	if len(payloads[0]) == 0 || payloads[0][0]&0x1f != 6 {
		t.Fatalf("native MediaMTX track probe payload = %x, want an H264 SEI NALU", payloads[0])
	}
}

func TestMediaMTXNativeSessionClosesAfterReceiverReadinessTimeout(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:                     "viewer",
		encoder:                encoder,
		logger:                 logs.NewLogger(logs.NewHub(8), false),
		closed:                 make(chan struct{}),
		mediaReady:             make(chan struct{}),
		receiverReady:          make(chan struct{}),
		nativeReadinessTimeout: 10 * time.Millisecond,
		writeNativeTrackProbe:  func() error { return nil },
	}
	session.mediaMTXNative.Store(true)
	session.handleConnected()
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("native session remained open after the bounded readiness wait")
	}
	select {
	case <-session.mediaReady:
		t.Fatal("native session released media after the receiver readiness timeout")
	default:
	}
	if got := len(encoder.requested); got != 0 {
		t.Fatalf("timed-out native session key-frame requests = %d, want 0", got)
	}
}

func TestMediaMTXNativeSessionCloseCancelsReceiverReadinessWait(t *testing.T) {
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 1)}
	session := &Session{
		id:                     "viewer",
		encoder:                encoder,
		logger:                 logs.NewLogger(logs.NewHub(8), false),
		closed:                 make(chan struct{}),
		mediaReady:             make(chan struct{}),
		receiverReady:          make(chan struct{}),
		nativeReadinessTimeout: 10 * time.Millisecond,
		writeNativeTrackProbe:  func() error { return nil },
	}
	session.mediaMTXNative.Store(true)
	session.handleConnected()
	session.Close("test complete")
	time.Sleep(2 * session.nativeReadinessTimeout)
	select {
	case <-session.mediaReady:
		t.Fatal("closed native session released media")
	default:
	}
	if got := len(encoder.requested); got != 0 {
		t.Fatalf("closed native session key-frame requests = %d, want 0", got)
	}
}

func TestMediaMTXNativeSessionClosesWhenReceiverBootstrapFails(t *testing.T) {
	bootstrapError := errors.New("track probe unavailable")
	session := &Session{
		id:            "viewer",
		logger:        logs.NewLogger(logs.NewHub(8), false),
		closed:        make(chan struct{}),
		mediaReady:    make(chan struct{}),
		receiverReady: make(chan struct{}),
		writeNativeTrackProbe: func() error {
			return bootstrapError
		},
	}
	session.mediaMTXNative.Store(true)
	session.handleConnected()
	select {
	case <-session.closed:
	default:
		t.Fatal("native session remained open after receiver bootstrap failure")
	}
	select {
	case <-session.mediaReady:
		t.Fatal("native session released media after receiver bootstrap failure")
	default:
	}
}

func TestMediaMTXNativeSessionBootstrapsOnlyOnceForConcurrentConnectedCallbacks(t *testing.T) {
	const callers = 100
	var trackProbeWrites atomic.Uint32
	session := &Session{
		id:            "viewer",
		logger:        logs.NewLogger(logs.NewHub(8), false),
		closed:        make(chan struct{}),
		mediaReady:    make(chan struct{}),
		receiverReady: make(chan struct{}),
		writeNativeTrackProbe: func() error {
			trackProbeWrites.Add(1)
			return nil
		},
	}
	session.mediaMTXNative.Store(true)
	defer session.Close("test complete")
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			session.handleConnected()
		}()
	}
	close(start)
	workers.Wait()
	if got := trackProbeWrites.Load(); got != 1 {
		t.Fatalf("native receiver track probe writes = %d, want 1", got)
	}
}

func TestSessionHandlesConcurrentConnectedCallbacks(t *testing.T) {
	const callers = 100
	encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, callers)}
	session := &Session{
		id:         "viewer",
		encoder:    encoder,
		logger:     logs.NewLogger(logs.NewHub(8), false),
		closed:     make(chan struct{}),
		mediaReady: make(chan struct{}),
	}
	defer session.Close("test complete")
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			session.handleConnected()
		}()
	}
	close(start)
	workers.Wait()
	select {
	case <-session.mediaReady:
	default:
		t.Fatal("concurrent connected callbacks did not release the media writer")
	}
	if got := len(encoder.requested); got != 1 {
		t.Fatalf("concurrent startup key-frame requests = %d, want 1 coalesced request", got)
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
	session, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("failed to open the first session: %v", err)
	}
	if _, err := broadcaster.OpenSession(context.Background()); !errors.Is(err, ErrSessionCapacity) {
		t.Fatalf("second viewer error = %v, want ErrSessionCapacity", err)
	}
	session.Close("release viewer slot")
	replacement, err := broadcaster.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("failed to reuse the released viewer slot: %v", err)
	}
	replacement.Close("test cleanup")
	stats := broadcaster.MetricsSnapshot()
	if stats.ActiveSessions != 0 || stats.OpeningSessions != 0 {
		t.Fatalf("viewer slots after replacement close = active %d, opening %d; want zero", stats.ActiveSessions, stats.OpeningSessions)
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
	session, err := broadcaster.OpenSession(context.Background())
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
