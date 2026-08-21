package bridge

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/repair"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/source"
)

type peerStateHarness struct {
	mu       sync.Mutex
	state    webrtc.PeerConnectionState
	callback func(webrtc.PeerConnectionState)
}

type resolverHarness struct {
	endpoint source.Endpoint
	err      error
	errs     []error
	called   chan struct{}
	calls    int
	path     string
	purpose  source.ResolutionPurpose
}

type authorizationUpdaterHarness struct {
	authorization string
	endpoint      *url.URL
	err           error
	updates       chan struct{}
}

type sourceSessionHarness struct {
	authorizationUpdaterHarness
	restarts chan struct{}
	target   *url.URL
	servers  []webrtc.ICEServer
}

type rtcpWriterHarness struct {
	packets []rtcp.Packet
	err     error
}

func TestRunClassifiesInvalidResolverIdentityAsPermanent(t *testing.T) {
	resolverURL, err := url.Parse("https://platform.example/api/video/distributor/source")
	if err != nil {
		t.Fatalf("parse resolver URL: %v", err)
	}
	_, err = run(context.Background(), config.Config{
		Path:               "devices/00000000-0000-4000-8000-000000000000",
		ResolverURL:        resolverURL,
		ResolverPrivateKey: "not-a-private-key",
		ResolverInstance:   "mediamtx-one",
		ResolverIssuer:     "rstream-video-distributor",
		ResolverAudience:   "rstream-video-source-resolver",
	}, runOptions{})
	if err == nil || !source.IsPermanent(err) {
		t.Fatalf("run error = %v, want permanent resolver-identity failure", err)
	}
}

func TestSourceConfigurationCopiesICEServers(t *testing.T) {
	servers := []source.ICEServer{{
		URLs:       []string{"turn:turn.example:3478?transport=udp"},
		Username:   "viewer",
		Credential: "secret",
	}}
	configuration := sourceConfiguration(servers)
	servers[0].URLs[0] = "turn:mutated.example:3478"
	if len(configuration.ICEServers) != 1 {
		t.Fatalf("ICE server count = %d, want 1", len(configuration.ICEServers))
	}
	if got := configuration.ICEServers[0].URLs[0]; got != "turn:turn.example:3478?transport=udp" {
		t.Fatalf("ICE URL = %q, want independent copy", got)
	}
	if configuration.ICEServers[0].Username != "viewer" || configuration.ICEServers[0].Credential != "secret" {
		t.Fatalf("ICE credentials = %+v", configuration.ICEServers[0])
	}
}

func TestKeyframeRequestsRewriteSourceSSRCWithoutMutatingInput(t *testing.T) {
	const sourceSSRC = uint32(9001)
	pli := &rtcp.PictureLossIndication{SenderSSRC: 10, MediaSSRC: 20}
	fir := &rtcp.FullIntraRequest{
		SenderSSRC: 30,
		MediaSSRC:  40,
		FIR:        []rtcp.FIREntry{{SSRC: 50, SequenceNumber: 1}, {SSRC: 60, SequenceNumber: 2}},
	}
	report := &rtcp.ReceiverReport{SSRC: 70}
	forward := keyframeRequests([]rtcp.Packet{pli, fir, report}, sourceSSRC)
	if len(forward) != 2 {
		t.Fatalf("forwarded packet count = %d, want 2", len(forward))
	}
	forwardPLI, ok := forward[0].(*rtcp.PictureLossIndication)
	if !ok || forwardPLI.MediaSSRC != sourceSSRC || forwardPLI.SenderSSRC != pli.SenderSSRC {
		t.Fatalf("forwarded PLI = %#v", forward[0])
	}
	forwardFIR, ok := forward[1].(*rtcp.FullIntraRequest)
	if !ok || forwardFIR.MediaSSRC != sourceSSRC || forwardFIR.SenderSSRC != fir.SenderSSRC {
		t.Fatalf("forwarded FIR = %#v", forward[1])
	}
	for index, entry := range forwardFIR.FIR {
		if entry.SSRC != sourceSSRC || entry.SequenceNumber != fir.FIR[index].SequenceNumber {
			t.Fatalf("forwarded FIR entry %d = %+v", index, entry)
		}
	}
	if fir.MediaSSRC != 40 || fir.FIR[0].SSRC != 50 || fir.FIR[1].SSRC != 60 {
		t.Fatalf("input FIR was mutated: %+v", fir)
	}
}

func TestRequestSourceKeyFrameTargetsTheActiveSource(t *testing.T) {
	writer := &rtcpWriterHarness{}
	if err := requestSourceKeyFrame(writer, 9001); err != nil {
		t.Fatalf("request source key frame: %v", err)
	}
	if len(writer.packets) != 1 {
		t.Fatalf("RTCP packets = %d, want 1", len(writer.packets))
	}
	pli, ok := writer.packets[0].(*rtcp.PictureLossIndication)
	if !ok || pli.MediaSSRC != 9001 {
		t.Fatalf("source key frame request = %#v", writer.packets[0])
	}
	wantErr := errors.New("synthetic RTCP failure")
	if err := requestSourceKeyFrame(&rtcpWriterHarness{err: wantErr}, 9001); !errors.Is(err, wantErr) {
		t.Fatalf("source key frame error = %v, want %v", err, wantErr)
	}
}

func TestStripSourceExtensionsPreservesMediaIdentity(t *testing.T) {
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   42,
			Timestamp:        90000,
			SSRC:             7,
			Extension:        true,
			ExtensionProfile: 0xbede,
			Extensions:       []rtp.Extension{{}},
		},
		Payload: []byte{1, 2, 3},
	}
	stripSourceExtensions(packet)
	if packet.Extension || packet.ExtensionProfile != 0 || len(packet.Extensions) != 0 {
		t.Fatalf("RTP extensions were retained: %+v", packet.Header)
	}
	if packet.SequenceNumber != 42 || packet.Timestamp != 90000 || packet.SSRC != 7 || len(packet.Payload) != 3 {
		t.Fatalf("media identity changed: %+v", packet)
	}
}

func TestWatchPeerConnectionReportsFailureAndCancellation(t *testing.T) {
	failed := &peerStateHarness{state: webrtc.PeerConnectionStateConnected}
	failedResult := make(chan error, 1)
	go func() { failedResult <- watchPeerConnection(context.Background(), failed) }()
	failed.setState(webrtc.PeerConnectionStateFailed)
	select {
	case err := <-failedResult:
		if err == nil || err.Error() != "peer connection entered failed state" {
			t.Fatalf("failure result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer failure was not reported")
	}
	canceled := &peerStateHarness{state: webrtc.PeerConnectionStateConnected}
	ctx, cancel := context.WithCancel(context.Background())
	canceledResult := make(chan error, 1)
	go func() { canceledResult <- watchPeerConnection(ctx, canceled) }()
	cancel()
	canceled.setState(webrtc.PeerConnectionStateClosed)
	select {
	case err := <-canceledResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer monitor did not stop after cancellation")
	}
}

func TestSuperviseWorkersMarksIncompleteShutdownAsFatal(t *testing.T) {
	source, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create source peer: %v", err)
	}
	destination, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		_ = source.Close()
		t.Fatalf("create destination peer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan workerResult, baseWorkerCount)
	results <- workerResult{name: "failed worker", err: errors.New("source stopped")}
	var sourceFEC atomic.Uint64
	var invalidFEC atomic.Uint64
	var damagedSourceFramesDropped atomic.Uint64
	var damagedSourcePacketsDropped atomic.Uint64
	var sourceICERestarts atomic.Uint64
	var sourceCredentialRefreshFailures atomic.Uint64
	shutdown := func() error {
		if source.ConnectionState() == webrtc.PeerConnectionStateClosed || destination.ConnectionState() == webrtc.PeerConnectionStateClosed {
			t.Fatal("peer connection closed before remote session shutdown")
		}
		return errors.Join(source.Close(), destination.Close())
	}
	_, err = superviseWorkers(ctx, cancel, results, baseWorkerCount, &sourceFEC, &invalidFEC, &damagedSourceFramesDropped, &damagedSourcePacketsDropped, &sourceICERestarts, &sourceCredentialRefreshFailures, shutdown, 10*time.Millisecond)
	if !errors.Is(err, ErrWorkerShutdownTimeout) {
		t.Fatalf("supervise error = %v, want worker shutdown timeout", err)
	}
	if source.ConnectionState() != webrtc.PeerConnectionStateClosed || destination.ConnectionState() != webrtc.PeerConnectionStateClosed {
		t.Fatal("peer connections remained open after worker shutdown timeout")
	}
}

func TestDecodeSourceWithoutFlexFECForwardsMediaAndRTX(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan decoderEvent, 2)
	packets := make(chan repair.Packet, 2)
	var sourceFEC atomic.Uint64
	var invalidFEC atomic.Uint64
	result := make(chan error, 1)
	go func() {
		result <- decodeSource(ctx, nil, events, packets, &sourceFEC, &invalidFEC, nil)
	}()
	mediaPacket := &rtp.Packet{Header: rtp.Header{SequenceNumber: 10}}
	rtxPacket := &rtp.Packet{Header: rtp.Header{SequenceNumber: 11}}
	events <- decoderEvent{packet: mediaPacket, receivedAt: time.Unix(1, 0), media: true}
	events <- decoderEvent{packet: rtxPacket, receivedAt: time.Unix(2, 0), media: true, rtx: true}
	first := <-packets
	second := <-packets
	if first.RTP != mediaPacket || first.RecoveredRTX {
		t.Fatalf("media packet = %+v", first)
	}
	if second.RTP != rtxPacket || !second.RecoveredRTX {
		t.Fatalf("RTX packet = %+v", second)
	}
	if sourceFEC.Load() != 0 || invalidFEC.Load() != 0 {
		t.Fatalf("FEC counters = source %d invalid %d, want 0 and 0", sourceFEC.Load(), invalidFEC.Load())
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("decoder result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("decoder did not stop after cancellation")
	}
}

func TestRefreshSessionCredentialsRenewsTheBoundSourceAndDestination(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	endpoint := &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=edge-old"}
	iceExpiresAt := now.Add(5 * time.Minute)
	current := source.Endpoint{URL: endpoint, ExpiresAt: now.Add(10 * time.Second), ICEExpiresAt: iceExpiresAt, ICEServers: []source.ICEServer{{URLs: []string{"turn:relay.example"}, ExpiresAt: iceExpiresAt}}}
	resolver := &resolverHarness{endpoint: source.Endpoint{
		URL:                      &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=edge-new"},
		Authorization:            "",
		DestinationAuthorization: "Bearer destination-new",
		ExpiresAt:                now.Add(2 * time.Minute),
	}}
	sourceAuthorization := &authorizationUpdaterHarness{}
	destinationAuthorization := &authorizationUpdaterHarness{}
	refreshed, err := refreshSessionCredentials(
		context.Background(),
		resolver,
		"devices/device-1",
		current,
		sourceAuthorization,
		destinationAuthorization,
		now,
	)
	if err != nil {
		t.Fatalf("refresh authorizations: %v", err)
	}
	if resolver.calls != 1 || resolver.path != "devices/device-1" || resolver.purpose != source.ResolutionPurposeSignaling {
		t.Fatalf("resolver calls = %d path %q purpose %q", resolver.calls, resolver.path, resolver.purpose)
	}
	if sourceAuthorization.authorization != "" || sourceAuthorization.endpoint.Query().Get("rstream.token") != "edge-new" || destinationAuthorization.authorization != "Bearer destination-new" {
		t.Fatalf("refreshed authorizations = source %q destination %q", sourceAuthorization.authorization, destinationAuthorization.authorization)
	}
	if !refreshed.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("refreshed expiration = %s", refreshed.ExpiresAt)
	}
	if !refreshed.ICEExpiresAt.Equal(iceExpiresAt) || len(refreshed.ICEServers) != 1 {
		t.Fatalf("signaling refresh discarded ICE credentials: %+v", refreshed)
	}
}

func TestMaintainSessionsRestartsExpiringICEAndStopsWithItsOwner(t *testing.T) {
	now := time.Now()
	current := source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=edge-old"}, ExpiresAt: now.Add(2 * time.Minute), ICEExpiresAt: now.Add(time.Millisecond)}
	refreshedICE := now.Add(2 * time.Minute)
	resolver := &resolverHarness{endpoint: source.Endpoint{
		URL:                      &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=edge-new"},
		Authorization:            "Bearer source-new",
		DestinationAuthorization: "Bearer destination-new",
		ExpiresAt:                now.Add(2 * time.Minute),
		ICEExpiresAt:             refreshedICE,
		ICEServers:               []source.ICEServer{{URLs: []string{"turn:relay.example"}, Username: "viewer", Credential: "secret", ExpiresAt: refreshedICE}},
	}}
	sourceSession := &sourceSessionHarness{restarts: make(chan struct{}, 1)}
	destinationSession := &authorizationUpdaterHarness{}
	writer := &rtcpWriterHarness{}
	ctx, cancel := context.WithCancel(context.Background())
	var restarts atomic.Uint64
	var refreshFailures atomic.Uint64
	result := make(chan error, 1)
	go func() {
		result <- maintainSessions(ctx, resolver, "devices/device-1", current, sourceSession, destinationSession, writer, 9001, &restarts, &refreshFailures)
	}()
	select {
	case <-sourceSession.restarts:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("ICE maintenance did not restart the expiring generation")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("maintenance shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ICE maintenance did not stop with its owner")
	}
	if resolver.calls != 1 || resolver.purpose != source.ResolutionPurposeSession {
		t.Fatalf("resolver calls = %d purpose %q", resolver.calls, resolver.purpose)
	}
	if sourceSession.target.Query().Get("rstream.token") != "edge-new" || sourceSession.authorization != "Bearer source-new" || len(sourceSession.servers) != 1 {
		t.Fatalf("source restart credentials = target %v authorization %q servers %+v", sourceSession.target, sourceSession.authorization, sourceSession.servers)
	}
	if destinationSession.authorization != "Bearer destination-new" || restarts.Load() != 1 || len(writer.packets) != 1 {
		t.Fatalf("maintenance result = destination %q restarts %d RTCP %d", destinationSession.authorization, restarts.Load(), len(writer.packets))
	}
}

func TestNextMaintenanceCombinesEqualCredentialDeadlines(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	action, got, ok := nextMaintenance(source.Endpoint{ExpiresAt: deadline, ICEExpiresAt: deadline})
	if !ok || action != maintenanceRestartICE || !got.Equal(deadline.Add(-credentialRefreshLead)) {
		t.Fatalf("combined maintenance = action %d deadline %s ok %t", action, got, ok)
	}
	action, got, ok = nextMaintenance(source.Endpoint{ExpiresAt: deadline})
	if !ok || action != maintenanceRefreshSignaling || !got.Equal(deadline.Add(-credentialRefreshLead)) {
		t.Fatalf("signaling maintenance = action %d deadline %s ok %t", action, got, ok)
	}
	if _, _, ok := nextMaintenance(source.Endpoint{}); ok {
		t.Fatal("static credentials scheduled idle maintenance")
	}
}

func TestMaintainSessionsHasZeroIdleCallsAndOneCallPerFailure(t *testing.T) {
	endpoint := source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep"}, ExpiresAt: time.Now().Add(time.Hour), ICEExpiresAt: time.Now().Add(time.Hour)}
	idleResolver := &resolverHarness{}
	idleCtx, cancelIdle := context.WithCancel(context.Background())
	cancelIdle()
	var restarts atomic.Uint64
	var refreshFailures atomic.Uint64
	err := maintainSessions(idleCtx, idleResolver, "devices/device-1", endpoint, &sourceSessionHarness{}, &authorizationUpdaterHarness{}, &rtcpWriterHarness{}, 9001, &restarts, &refreshFailures)
	if !errors.Is(err, context.Canceled) || idleResolver.calls != 0 {
		t.Fatalf("idle maintenance = error %v resolver calls %d", err, idleResolver.calls)
	}
	failure := errors.New("resolver unavailable")
	failingResolver := &resolverHarness{err: failure}
	endpoint.ICEExpiresAt = time.Now()
	err = maintainSessions(context.Background(), failingResolver, "devices/device-1", endpoint, &sourceSessionHarness{}, &authorizationUpdaterHarness{}, &rtcpWriterHarness{}, 9001, &restarts, &refreshFailures)
	if !errors.Is(err, failure) || failingResolver.calls != 1 || restarts.Load() != 0 || refreshFailures.Load() != 1 {
		t.Fatalf("failed maintenance = error %v resolver calls %d restarts %d refresh failures %d", err, failingResolver.calls, restarts.Load(), refreshFailures.Load())
	}
}

func TestMaintainSessionsRetriesTransientResolutionWithoutTearingDownTheSession(t *testing.T) {
	now := time.Now()
	current := source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=old"}, ExpiresAt: now.Add(time.Second)}
	resolverFailure := errors.New("resolver temporarily unavailable")
	resolver := &resolverHarness{
		endpoint: source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep", RawQuery: "rstream.token=new"}, ExpiresAt: now.Add(time.Hour)},
		errs:     []error{resolverFailure},
	}
	sourceSession := &sourceSessionHarness{authorizationUpdaterHarness: authorizationUpdaterHarness{updates: make(chan struct{}, 1)}}
	destinationSession := &authorizationUpdaterHarness{}
	ctx, cancel := context.WithCancel(context.Background())
	var restarts atomic.Uint64
	var refreshFailures atomic.Uint64
	done := make(chan error, 1)
	go func() {
		done <- maintainSessions(ctx, resolver, "devices/device-1", current, sourceSession, destinationSession, &rtcpWriterHarness{}, 9001, &restarts, &refreshFailures)
	}()
	select {
	case <-sourceSession.updates:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("transient resolver failure did not recover before credential expiration")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("maintenance result = %v, want owner cancellation", err)
	}
	if resolver.calls != 2 || refreshFailures.Load() != 1 || sourceSession.endpoint.Query().Get("rstream.token") != "new" || restarts.Load() != 0 {
		t.Fatalf("recovered maintenance = calls %d failures %d endpoint %v restarts %d", resolver.calls, refreshFailures.Load(), sourceSession.endpoint, restarts.Load())
	}
}

func TestMaintainSessionsDoesNotRetryPermanentResolutionFailure(t *testing.T) {
	failure := source.Permanent(errors.New("resolver contract rejected"))
	resolver := &resolverHarness{err: failure}
	current := source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep"}, ExpiresAt: time.Now()}
	var restarts atomic.Uint64
	var refreshFailures atomic.Uint64
	err := maintainSessions(context.Background(), resolver, "devices/device-1", current, &sourceSessionHarness{}, &authorizationUpdaterHarness{}, &rtcpWriterHarness{}, 9001, &restarts, &refreshFailures)
	if !errors.Is(err, failure) || resolver.calls != 1 || refreshFailures.Load() != 1 {
		t.Fatalf("permanent maintenance failure = error %v calls %d failures %d", err, resolver.calls, refreshFailures.Load())
	}
}

func TestMaintainSessionsCancelsDuringTransientResolutionBackoff(t *testing.T) {
	called := make(chan struct{}, 1)
	resolver := &resolverHarness{err: errors.New("resolver temporarily unavailable"), called: called}
	current := source.Endpoint{URL: &url.URL{Scheme: "https", Host: "device.example", Path: "/whep"}, ExpiresAt: time.Now().Add(time.Second)}
	ctx, cancel := context.WithCancel(context.Background())
	var restarts atomic.Uint64
	var refreshFailures atomic.Uint64
	done := make(chan error, 1)
	go func() {
		done <- maintainSessions(ctx, resolver, "devices/device-1", current, &sourceSessionHarness{}, &authorizationUpdaterHarness{}, &rtcpWriterHarness{}, 9001, &restarts, &refreshFailures)
	}()
	select {
	case <-called:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("resolver was not called")
	}
	deadline := time.Now().Add(time.Second)
	for refreshFailures.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled maintenance result = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("credential retry backoff ignored cancellation")
	}
	if resolver.calls != 1 || refreshFailures.Load() != 1 {
		t.Fatalf("canceled maintenance = calls %d failures %d", resolver.calls, refreshFailures.Load())
	}
}

func TestRefreshSessionCredentialsRejectsAnotherSourceEndpoint(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	current := source.Endpoint{
		URL:       &url.URL{Scheme: "https", Host: "device-a.example", Path: "/whep"},
		ExpiresAt: now,
	}
	resolver := &resolverHarness{endpoint: source.Endpoint{
		URL:                      &url.URL{Scheme: "https", Host: "device-b.example", Path: "/whep"},
		Authorization:            "Bearer wrong-source",
		DestinationAuthorization: "Bearer destination-new",
		ExpiresAt:                now.Add(2 * time.Minute),
	}}
	sourceAuthorization := &authorizationUpdaterHarness{}
	destinationAuthorization := &authorizationUpdaterHarness{}
	_, err := refreshSessionCredentials(
		context.Background(),
		resolver,
		"devices/device-1",
		current,
		sourceAuthorization,
		destinationAuthorization,
		now,
	)
	if err == nil || !strings.Contains(err.Error(), "changed the active endpoint") {
		t.Fatalf("refresh error = %v", err)
	}
	if sourceAuthorization.authorization != "" {
		t.Fatalf("source authorization changed to %q", sourceAuthorization.authorization)
	}
	if destinationAuthorization.authorization != "" {
		t.Fatalf("destination authorization changed to %q", destinationAuthorization.authorization)
	}
}

func TestRefreshSessionCredentialsKeepsUsableCredentials(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	current := source.Endpoint{
		URL:       &url.URL{Scheme: "https", Host: "device.example", Path: "/whep"},
		ExpiresAt: now.Add(credentialRefreshLead + time.Nanosecond),
	}
	resolver := &resolverHarness{}
	refreshed, err := refreshSessionCredentials(
		context.Background(),
		resolver,
		"devices/device-1",
		current,
		&authorizationUpdaterHarness{},
		&authorizationUpdaterHarness{},
		now,
	)
	if err != nil {
		t.Fatalf("keep authorizations: %v", err)
	}
	if resolver.calls != 0 || !refreshed.ExpiresAt.Equal(current.ExpiresAt) {
		t.Fatalf("usable credentials were refreshed: calls %d expiration %s", resolver.calls, refreshed.ExpiresAt)
	}
}

func TestRepairObserverPublishesBoundedLiveSnapshots(t *testing.T) {
	var sourceFEC atomic.Uint64
	var invalidFEC atomic.Uint64
	var damagedSourceFramesDropped atomic.Uint64
	var damagedSourcePacketsDropped atomic.Uint64
	var sourceICERestarts atomic.Uint64
	var sourceCredentialRefreshFailures atomic.Uint64
	sourceFEC.Store(7)
	invalidFEC.Store(2)
	damagedSourceFramesDropped.Store(5)
	damagedSourcePacketsDropped.Store(9)
	sourceICERestarts.Store(1)
	sourceCredentialRefreshFailures.Store(4)
	observed := make(chan Result, 1)
	options := repairObserver(func(result Result) { observed <- result }, &sourceFEC, &invalidFEC, &damagedSourceFramesDropped, &damagedSourcePacketsDropped, &sourceICERestarts, &sourceCredentialRefreshFailures)
	if options.Interval != metricsObservationInterval || options.Observe == nil {
		t.Fatalf("observer options = %+v", options)
	}
	options.Observe(repair.Stats{Received: 11, RepairedRTX: 3})
	result := <-observed
	if result.Repair.Received != 11 || result.Repair.RepairedRTX != 3 || result.SourceFECPackets != 7 || result.InvalidFEC != 2 || result.DamagedSourceFramesDropped != 5 || result.DamagedSourcePacketsDropped != 9 || result.SourceICERestarts != 1 || result.SourceCredentialRefreshFailures != 4 {
		t.Fatalf("observed result = %+v", result)
	}
	if disabled := repairObserver(nil, &sourceFEC, &invalidFEC, &damagedSourceFramesDropped, &damagedSourcePacketsDropped, &sourceICERestarts, &sourceCredentialRefreshFailures); disabled.Observe != nil || disabled.Interval != 0 {
		t.Fatalf("disabled observer = %+v", disabled)
	}
}

func (p *peerStateHarness) ConnectionState() webrtc.PeerConnectionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (r *resolverHarness) Resolve(ctx context.Context, path string, purpose source.ResolutionPurpose) (source.Endpoint, error) {
	r.calls++
	r.path = path
	r.purpose = purpose
	if r.called != nil {
		select {
		case r.called <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		return source.Endpoint{}, ctx.Err()
	default:
		if r.calls <= len(r.errs) && r.errs[r.calls-1] != nil {
			return source.Endpoint{}, r.errs[r.calls-1]
		}
		if r.err != nil {
			return source.Endpoint{}, r.err
		}
		return r.endpoint, nil
	}
}

func (w *rtcpWriterHarness) WriteRTCP(packets []rtcp.Packet) error {
	w.packets = append(w.packets, packets...)
	return w.err
}

func (u *authorizationUpdaterHarness) SetAuthorization(authorization string) error {
	if u.err != nil {
		return u.err
	}
	u.authorization = authorization
	return nil
}

func (u *authorizationUpdaterHarness) SetCredentials(endpoint *url.URL, authorization string) error {
	if u.err != nil {
		return u.err
	}
	u.endpoint = new(url.URL)
	*u.endpoint = *endpoint
	u.authorization = authorization
	if u.updates != nil {
		select {
		case u.updates <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *sourceSessionHarness) Restart(_ context.Context, endpoint *url.URL, authorization string, servers []webrtc.ICEServer) error {
	s.target = endpoint
	s.authorization = authorization
	s.servers = servers
	if s.restarts != nil {
		s.restarts <- struct{}{}
	}
	return s.err
}

func (p *peerStateHarness) OnConnectionStateChange(callback func(webrtc.PeerConnectionState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callback = callback
}

func (p *peerStateHarness) setState(state webrtc.PeerConnectionState) {
	p.mu.Lock()
	p.state = state
	callback := p.callback
	p.mu.Unlock()
	if callback != nil {
		callback(state)
	}
}
