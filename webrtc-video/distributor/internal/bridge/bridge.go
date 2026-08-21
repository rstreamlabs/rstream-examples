package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor/pkg/flexfec"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/repair"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/source"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/whipwhep"
)

const (
	httpTimeout                       = 15 * time.Second
	sourceTrackTimeout                = 10 * time.Second
	sessionCleanupTimeout             = 3 * time.Second
	workerShutdownTimeout             = 3 * time.Second
	minimumResolvedCredentialLifetime = 60 * time.Second
	credentialRefreshLead             = 30 * time.Second
	credentialRefreshTimeout          = 3 * time.Second
	credentialRetryInitialDelay       = 250 * time.Millisecond
	credentialRetryMaximumDelay       = 3 * time.Second
	iceRestartOperationTimeout        = 15 * time.Second
	metricsObservationInterval        = time.Second
	packetQueueCapacity               = 256
	maxRTPPacketBytes                 = 65535
	baseWorkerCount                   = 6
)

var ErrWorkerShutdownTimeout = errors.New("worker shutdown timed out")

type Result struct {
	Repair                          repair.Stats
	SourceFECPackets                uint64
	InvalidFEC                      uint64
	DamagedSourceFramesDropped      uint64
	DamagedSourcePacketsDropped     uint64
	SourceICERestarts               uint64
	SourceCredentialRefreshFailures uint64
}

type sourceTrack struct {
	track    *webrtc.TrackRemote
	receiver *webrtc.RTPReceiver
}

type decoderEvent struct {
	packet     *rtp.Packet
	receivedAt time.Time
	media      bool
	rtx        bool
}

type workerResult struct {
	name     string
	stats    repair.Stats
	hasStats bool
	err      error
}

type runOptions struct {
	dropMediaSequence *uint16
	dropFirstFEC      bool
	observe           func(Result)
}

type peerConnectionStateSource interface {
	ConnectionState() webrtc.PeerConnectionState
	OnConnectionStateChange(func(webrtc.PeerConnectionState))
}

type rtcpWriter interface {
	WriteRTCP([]rtcp.Packet) error
}

type fecDecoder interface {
	Decode(rtp.Packet) ([]rtp.Packet, error)
}

func Run(ctx context.Context, configuration config.Config) (Result, error) {
	return run(ctx, configuration, runOptions{})
}

func RunObserved(ctx context.Context, configuration config.Config, observe func(Result)) (Result, error) {
	return run(ctx, configuration, runOptions{observe: observe})
}

func run(ctx context.Context, configuration config.Config, options runOptions) (result Result, err error) {
	client := &http.Client{Timeout: httpTimeout}
	resolver, err := configuredSourceResolver(configuration, client)
	if err != nil {
		return Result{}, source.Permanent(err)
	}
	endpoint, err := resolver.Resolve(ctx, configuration.Path, source.ResolutionPurposeSession)
	if err != nil {
		return Result{}, fmt.Errorf("resolve source for path %q: %w", configuration.Path, err)
	}
	destination, sender, output, destinationSession, err := openDestination(ctx, configuration, endpoint.DestinationAuthorization, client)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if destinationSession != nil {
			err = errors.Join(err, closeSession(destinationSession))
		}
	}()
	sourcePeer, tracks, sourceSession, err := openSource(ctx, endpoint, client)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if sourceSession != nil {
			err = errors.Join(err, closeSession(sourceSession))
		}
	}()
	incoming, err := waitForSourceTrack(ctx, tracks)
	if err != nil {
		return Result{}, err
	}
	if err := requestSourceKeyFrame(sourcePeer, uint32(incoming.track.SSRC())); err != nil {
		return Result{}, fmt.Errorf("request initial source key frame: %w", err)
	}
	var sourceICERestarts atomic.Uint64
	var sourceCredentialRefreshFailures atomic.Uint64
	activeSourceSession := sourceSession
	activeDestinationSession := destinationSession
	maintain := func(workerCtx context.Context) error {
		return maintainSessions(workerCtx, resolver, configuration.Path, endpoint, activeSourceSession, activeDestinationSession, sourcePeer, uint32(incoming.track.SSRC()), &sourceICERestarts, &sourceCredentialRefreshFailures)
	}
	if endpoint.ExpiresAt.IsZero() && endpoint.ICEExpiresAt.IsZero() {
		maintain = nil
	}
	shutdown := func() error {
		refreshCtx, cancelRefresh := context.WithTimeout(context.Background(), credentialRefreshTimeout)
		_, refreshErr := refreshSessionCredentials(
			refreshCtx,
			resolver,
			configuration.Path,
			endpoint,
			sourceSession,
			destinationSession,
			time.Now(),
		)
		cancelRefresh()
		shutdownErr := errors.Join(refreshErr, closeSession(destinationSession), closeSession(sourceSession))
		destinationSession = nil
		sourceSession = nil
		return shutdownErr
	}
	return forward(ctx, sourcePeer, incoming, destination, sender, output, maintain, &sourceICERestarts, &sourceCredentialRefreshFailures, shutdown, options)
}

func configuredSourceResolver(configuration config.Config, client *http.Client) (source.Resolver, error) {
	if configuration.SourceURL != nil {
		return source.StaticResolver{Endpoint: source.Endpoint{
			URL:                      configuration.SourceURL,
			Authorization:            configuration.SourceAuthorization,
			DestinationAuthorization: configuration.MediaMTXAuthorization,
		}}, nil
	}
	authorizer, err := source.NewRequestSigner(
		configuration.ResolverPrivateKey,
		configuration.ResolverInstance,
		configuration.ResolverIssuer,
		configuration.ResolverAudience,
	)
	if err != nil {
		return nil, fmt.Errorf("configure source resolver identity: %w", err)
	}
	resolver, err := source.NewHTTPResolver(
		configuration.ResolverURL,
		authorizer,
		client,
		source.ResolverOptions{MinimumLifetime: minimumResolvedCredentialLifetime},
	)
	if err != nil {
		return nil, fmt.Errorf("configure source resolver: %w", err)
	}
	return resolver, nil
}

type authorizationUpdater interface {
	SetAuthorization(string) error
}

type sourceCredentialsUpdater interface {
	SetCredentials(*url.URL, string) error
}

type sourceSessionController interface {
	sourceCredentialsUpdater
	Restart(context.Context, *url.URL, string, []webrtc.ICEServer) error
}

func refreshSessionCredentials(
	ctx context.Context,
	resolver source.Resolver,
	path string,
	current source.Endpoint,
	sourceSession sourceCredentialsUpdater,
	destinationSession authorizationUpdater,
	now time.Time,
) (source.Endpoint, error) {
	if current.ExpiresAt.IsZero() || current.ExpiresAt.After(now.Add(credentialRefreshLead)) {
		return current, nil
	}
	refreshed, err := resolver.Resolve(ctx, path, source.ResolutionPurposeSignaling)
	if err != nil {
		return current, fmt.Errorf("refresh media session authorization: %w", err)
	}
	return applyRefreshedSignaling(current, refreshed, sourceSession, destinationSession)
}

func applyRefreshedSignaling(
	current source.Endpoint,
	refreshed source.Endpoint,
	sourceSession sourceCredentialsUpdater,
	destinationSession authorizationUpdater,
) (source.Endpoint, error) {
	if !sameEndpoint(current.URL, refreshed.URL) {
		return refreshed, errors.New("refreshed source authorization changed the active endpoint")
	}
	refreshed.ICEServers = current.ICEServers
	refreshed.ICEExpiresAt = current.ICEExpiresAt
	var refreshErr error
	if sourceSession != nil {
		if err := sourceSession.SetCredentials(refreshed.URL, refreshed.Authorization); err != nil {
			refreshErr = errors.Join(refreshErr, fmt.Errorf("refresh source credentials: %w", err))
		}
	}
	if refreshErr == nil && destinationSession != nil {
		if err := destinationSession.SetAuthorization(refreshed.DestinationAuthorization); err != nil {
			refreshErr = errors.Join(refreshErr, fmt.Errorf("refresh destination authorization: %w", err))
		}
	}
	return refreshed, refreshErr
}

func maintainSessions(
	ctx context.Context,
	resolver source.Resolver,
	path string,
	current source.Endpoint,
	sourceSession sourceSessionController,
	destinationSession authorizationUpdater,
	sourcePeer rtcpWriter,
	sourceSSRC uint32,
	restarts *atomic.Uint64,
	refreshFailures *atomic.Uint64,
) error {
	for {
		action, deadline, ok := nextMaintenance(current)
		if !ok {
			<-ctx.Done()
			return ctx.Err()
		}
		timer := time.NewTimer(max(time.Duration(0), time.Until(deadline)))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		if action == maintenanceRefreshSignaling {
			refreshed, err := resolveSourceWithRetry(ctx, resolver, path, source.ResolutionPurposeSignaling, current.ExpiresAt, 0, refreshFailures)
			if err != nil {
				return fmt.Errorf("refresh media session authorization: %w", err)
			}
			refreshed, err = applyRefreshedSignaling(current, refreshed, sourceSession, destinationSession)
			if err != nil {
				return err
			}
			current = refreshed
			continue
		}
		refreshed, err := resolveSourceWithRetry(ctx, resolver, path, source.ResolutionPurposeSession, current.ICEExpiresAt, iceRestartOperationTimeout, refreshFailures)
		if err == nil && !sameEndpoint(current.URL, refreshed.URL) {
			err = errors.New("refreshed source ICE credentials changed the active endpoint")
		}
		operationCtx, cancel := context.WithTimeout(ctx, iceRestartOperationTimeout)
		if err == nil {
			err = sourceSession.Restart(operationCtx, refreshed.URL, refreshed.Authorization, sourceConfiguration(refreshed.ICEServers).ICEServers)
		}
		if err == nil {
			err = destinationSession.SetAuthorization(refreshed.DestinationAuthorization)
		}
		if err == nil {
			err = requestSourceKeyFrame(sourcePeer, sourceSSRC)
		}
		cancel()
		if err != nil {
			return fmt.Errorf("renew active source ICE generation: %w", err)
		}
		current = refreshed
		restarts.Add(1)
	}
}

func resolveSourceWithRetry(
	ctx context.Context,
	resolver source.Resolver,
	path string,
	purpose source.ResolutionPurpose,
	expiresAt time.Time,
	postResolutionBudget time.Duration,
	failures *atomic.Uint64,
) (source.Endpoint, error) {
	retryDeadline := expiresAt.Add(-postResolutionBudget)
	delay := credentialRetryInitialDelay
	for attempt := 0; ; attempt++ {
		operationDeadline := time.Now().Add(credentialRefreshTimeout)
		if attempt > 0 && time.Now().Before(retryDeadline) {
			operationDeadline = earlierTime(operationDeadline, retryDeadline)
		}
		operationCtx, cancel := context.WithDeadline(ctx, operationDeadline)
		endpoint, err := resolver.Resolve(operationCtx, path, purpose)
		cancel()
		if err == nil {
			return endpoint, nil
		}
		if ctx.Err() != nil {
			return source.Endpoint{}, ctx.Err()
		}
		if failures != nil {
			failures.Add(1)
		}
		if source.IsPermanent(err) || !time.Now().Add(delay).Before(retryDeadline) {
			return source.Endpoint{}, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return source.Endpoint{}, ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, credentialRetryMaximumDelay)
	}
}

func earlierTime(left time.Time, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

type maintenanceAction uint8

const (
	maintenanceRefreshSignaling maintenanceAction = iota
	maintenanceRestartICE
)

func nextMaintenance(endpoint source.Endpoint) (maintenanceAction, time.Time, bool) {
	authorization := endpoint.ExpiresAt
	ice := endpoint.ICEExpiresAt
	if authorization.IsZero() && ice.IsZero() {
		return 0, time.Time{}, false
	}
	if ice.IsZero() || (!authorization.IsZero() && authorization.Before(ice)) {
		return maintenanceRefreshSignaling, authorization.Add(-credentialRefreshLead), true
	}
	return maintenanceRestartICE, ice.Add(-credentialRefreshLead), true
}

func sameEndpoint(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	leftTarget := *left
	leftQuery := leftTarget.Query()
	leftQuery.Del("rstream.token")
	leftTarget.RawQuery = leftQuery.Encode()
	rightTarget := *right
	rightQuery := rightTarget.Query()
	rightQuery.Del("rstream.token")
	rightTarget.RawQuery = rightQuery.Encode()
	return leftTarget.String() == rightTarget.String()
}

func openDestination(
	ctx context.Context,
	configuration config.Config,
	authorization string,
	client *http.Client,
) (*webrtc.PeerConnection, *webrtc.RTPSender, *webrtc.TrackLocalStaticRTP, *whipwhep.Session, error) {
	peer, err := media.NewDestinationPeer(webrtc.Configuration{BundlePolicy: webrtc.BundlePolicyMaxBundle})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	output, err := webrtc.NewTrackLocalStaticRTP(media.H264Capability(), "video", "rstream-distributor")
	if err != nil {
		_ = peer.Close()
		return nil, nil, nil, nil, fmt.Errorf("create destination track: %w", err)
	}
	sender, err := peer.AddTrack(output)
	if err != nil {
		_ = peer.Close()
		return nil, nil, nil, nil, fmt.Errorf("add destination track: %w", err)
	}
	session, err := whipwhep.Exchange(ctx, peer, configuration.DestinationURL(), authorization, client, whipwhep.Options{AllowLegacyWildcardETag: true})
	if err != nil {
		_ = peer.Close()
		return nil, nil, nil, nil, fmt.Errorf("open MediaMTX WHIP session: %w", err)
	}
	return peer, sender, output, session, nil
}

func openSource(
	ctx context.Context,
	endpoint source.Endpoint,
	client *http.Client,
) (*webrtc.PeerConnection, <-chan sourceTrack, *whipwhep.Session, error) {
	peer, err := media.NewSourcePeer(sourceConfiguration(endpoint.ICEServers))
	if err != nil {
		return nil, nil, nil, err
	}
	tracks := make(chan sourceTrack, 1)
	peer.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		select {
		case tracks <- sourceTrack{track: track, receiver: receiver}:
		default:
		}
	})
	_, err = peer.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	)
	if err != nil {
		_ = peer.Close()
		return nil, nil, nil, fmt.Errorf("add source transceiver: %w", err)
	}
	session, err := whipwhep.Exchange(ctx, peer, endpoint.URL, endpoint.Authorization, client)
	if err != nil {
		_ = peer.Close()
		return nil, nil, nil, fmt.Errorf("open source WHEP session: %w", err)
	}
	return peer, tracks, session, nil
}

func sourceConfiguration(servers []source.ICEServer) webrtc.Configuration {
	iceServers := make([]webrtc.ICEServer, len(servers))
	for index, server := range servers {
		iceServers[index] = webrtc.ICEServer{
			URLs:       append([]string(nil), server.URLs...),
			Username:   server.Username,
			Credential: server.Credential,
		}
	}
	return webrtc.Configuration{BundlePolicy: webrtc.BundlePolicyMaxBundle, ICEServers: iceServers}
}

func waitForSourceTrack(ctx context.Context, tracks <-chan sourceTrack) (sourceTrack, error) {
	timer := time.NewTimer(sourceTrackTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return sourceTrack{}, ctx.Err()
	case track := <-tracks:
		return track, nil
	case <-timer.C:
		return sourceTrack{}, errors.New("source did not publish an H264 track")
	}
}

func requestSourceKeyFrame(writer rtcpWriter, sourceSSRC uint32) error {
	return writer.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: sourceSSRC}})
}

func forward(
	ctx context.Context,
	sourcePeer *webrtc.PeerConnection,
	incoming sourceTrack,
	destination *webrtc.PeerConnection,
	sender *webrtc.RTPSender,
	output *webrtc.TrackLocalStaticRTP,
	maintain func(context.Context) error,
	sourceICERestarts *atomic.Uint64,
	sourceCredentialRefreshFailures *atomic.Uint64,
	shutdown func() error,
	options runOptions,
) (Result, error) {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan decoderEvent, packetQueueCapacity)
	packets := make(chan repair.Packet, packetQueueCapacity)
	workerCount := baseWorkerCount
	if maintain != nil {
		workerCount++
	}
	var decoder fecDecoder
	continuity := newH264ContinuityFilter(incoming.track.Codec().MimeType == webrtc.MimeTypeH264)
	sequenceRewriter := rtpSequenceRewriter{}
	var damagedSourceFramesDropped atomic.Uint64
	var damagedSourcePacketsDropped atomic.Uint64
	if incoming.track.FecSSRC() != 0 {
		workerCount++
		decoder = flexfec.NewDecoder03(
			uint32(incoming.track.FecSSRC()),
			uint32(incoming.track.SSRC()),
			logging.NewDefaultLoggerFactory(),
		)
	}
	results := make(chan workerResult, workerCount)
	var invalidFEC atomic.Uint64
	var sourceFEC atomic.Uint64
	startWorker(results, "source media reader", func() error {
		return readSourceMedia(workerCtx, incoming.track, events)
	})
	if decoder != nil {
		startWorker(results, "source FlexFEC reader", func() error {
			return readSourceFEC(workerCtx, incoming.receiver, events, options.dropFirstFEC)
		})
	}
	startWorker(results, "source repair decoder", func() error {
		return decodeSource(workerCtx, decoder, events, packets, &sourceFEC, &invalidFEC, options.dropMediaSequence)
	})
	startWorker(results, "destination RTCP reader", func() error {
		return forwardDestinationRTCP(workerCtx, sender, sourcePeer, uint32(incoming.track.SSRC()))
	})
	startWorker(results, "source peer monitor", func() error {
		return watchPeerConnection(workerCtx, sourcePeer)
	})
	startWorker(results, "destination peer monitor", func() error {
		return watchPeerConnection(workerCtx, destination)
	})
	if maintain != nil {
		startWorker(results, "session credential maintainer", func() error { return maintain(workerCtx) })
	}
	go func() {
		stats, processErr := repair.ProcessObserved(
			workerCtx,
			repair.DefaultConfig(),
			packets,
			func(packet *rtp.Packet) error {
				forward, damaged := continuity.accept(packet)
				if damaged {
					damagedSourceFramesDropped.Add(1)
				}
				if !forward {
					damagedSourcePacketsDropped.Add(1)
					return nil
				}
				sequenceRewriter.rewrite(packet)
				stripSourceExtensions(packet)
				return output.WriteRTP(packet)
			},
			func(event repair.FeedbackEvent) error {
				if event.RequestKeyFrame {
					return requestSourceKeyFrame(sourcePeer, uint32(incoming.track.SSRC()))
				}
				return sourcePeer.WriteRTCP([]rtcp.Packet{&rtcp.TransportLayerNack{
					MediaSSRC: uint32(incoming.track.SSRC()),
					Nacks:     rtcp.NackPairsFromSequenceNumbers(event.MissingSequences),
				}})
			},
			repairObserver(options.observe, &sourceFEC, &invalidFEC, &damagedSourceFramesDropped, &damagedSourcePacketsDropped, sourceICERestarts, sourceCredentialRefreshFailures),
		)
		results <- workerResult{name: "repair processor", stats: stats, hasStats: true, err: processErr}
	}()
	return superviseWorkers(ctx, cancel, results, workerCount, &sourceFEC, &invalidFEC, &damagedSourceFramesDropped, &damagedSourcePacketsDropped, sourceICERestarts, sourceCredentialRefreshFailures, shutdown, workerShutdownTimeout)
}

func repairObserver(
	observe func(Result),
	sourceFEC *atomic.Uint64,
	invalidFEC *atomic.Uint64,
	damagedSourceFramesDropped *atomic.Uint64,
	damagedSourcePacketsDropped *atomic.Uint64,
	sourceICERestarts *atomic.Uint64,
	sourceCredentialRefreshFailures *atomic.Uint64,
) repair.ObserverOptions {
	if observe == nil {
		return repair.ObserverOptions{}
	}
	return repair.ObserverOptions{
		Interval: metricsObservationInterval,
		Observe: func(stats repair.Stats) {
			observe(Result{
				Repair:                          stats,
				SourceFECPackets:                sourceFEC.Load(),
				InvalidFEC:                      invalidFEC.Load(),
				DamagedSourceFramesDropped:      damagedSourceFramesDropped.Load(),
				DamagedSourcePacketsDropped:     damagedSourcePacketsDropped.Load(),
				SourceICERestarts:               sourceICERestarts.Load(),
				SourceCredentialRefreshFailures: sourceCredentialRefreshFailures.Load(),
			})
		},
	}
}

func watchPeerConnection(ctx context.Context, peer peerConnectionStateSource) error {
	terminal := make(chan webrtc.PeerConnectionState, 1)
	report := func(state webrtc.PeerConnectionState) {
		if state != webrtc.PeerConnectionStateFailed && state != webrtc.PeerConnectionStateClosed {
			return
		}
		select {
		case terminal <- state:
		default:
		}
	}
	peer.OnConnectionStateChange(report)
	report(peer.ConnectionState())
	select {
	case <-ctx.Done():
		return ctx.Err()
	case state := <-terminal:
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("peer connection entered %s state", state.String())
	}
}

func startWorker(results chan<- workerResult, name string, run func() error) {
	go func() { results <- workerResult{name: name, err: run()} }()
}

func superviseWorkers(
	ctx context.Context,
	cancel context.CancelFunc,
	results <-chan workerResult,
	workerCount int,
	sourceFEC *atomic.Uint64,
	invalidFEC *atomic.Uint64,
	damagedSourceFramesDropped *atomic.Uint64,
	damagedSourcePacketsDropped *atomic.Uint64,
	sourceICERestarts *atomic.Uint64,
	sourceCredentialRefreshFailures *atomic.Uint64,
	shutdown func() error,
	shutdownTimeout time.Duration,
) (Result, error) {
	completed := 0
	result := Result{}
	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case worker := <-results:
		completed++
		collectWorkerResult(worker, &result)
		if ctx.Err() != nil {
			runErr = ctx.Err()
		} else {
			runErr = unexpectedWorkerExit(worker)
		}
	}
	cancel()
	runErr = errors.Join(runErr, shutdown())
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	for completed < workerCount {
		select {
		case worker := <-results:
			completed++
			collectWorkerResult(worker, &result)
			if !isShutdownError(worker.err) {
				runErr = errors.Join(runErr, fmt.Errorf("%s: %w", worker.name, worker.err))
			}
		case <-timer.C:
			runErr = errors.Join(runErr, fmt.Errorf("%w after %s with %d workers still running", ErrWorkerShutdownTimeout, shutdownTimeout, workerCount-completed))
			completed = workerCount
		}
	}
	result.InvalidFEC = invalidFEC.Load()
	result.SourceFECPackets = sourceFEC.Load()
	result.DamagedSourceFramesDropped = damagedSourceFramesDropped.Load()
	result.DamagedSourcePacketsDropped = damagedSourcePacketsDropped.Load()
	result.SourceICERestarts = sourceICERestarts.Load()
	result.SourceCredentialRefreshFailures = sourceCredentialRefreshFailures.Load()
	return result, runErr
}

func collectWorkerResult(worker workerResult, result *Result) {
	if worker.hasStats {
		result.Repair = worker.stats
	}
}

func unexpectedWorkerExit(worker workerResult) error {
	if worker.err == nil {
		return fmt.Errorf("%s stopped unexpectedly", worker.name)
	}
	return fmt.Errorf("%s: %w", worker.name, worker.err)
}

func readSourceMedia(ctx context.Context, track *webrtc.TrackRemote, events chan<- decoderEvent) error {
	for {
		packet, attributes, err := track.ReadRTP()
		if err != nil {
			return fmt.Errorf("read source media: %w", err)
		}
		event := decoderEvent{
			packet:     packet,
			receivedAt: time.Now(),
			media:      true,
			rtx:        attributes.Get(webrtc.AttributeRtxSsrc) != nil,
		}
		if err := sendEvent(ctx, events, event); err != nil {
			return err
		}
	}
}

func readSourceFEC(ctx context.Context, receiver *webrtc.RTPReceiver, events chan<- decoderEvent, dropFirst bool) error {
	buffer := make([]byte, maxRTPPacketBytes)
	for {
		size, _, err := receiver.ReadFEC(buffer)
		if err != nil {
			return fmt.Errorf("read source FlexFEC: %w", err)
		}
		if dropFirst {
			dropFirst = false
			continue
		}
		var packet rtp.Packet
		if err := packet.Unmarshal(buffer[:size]); err != nil {
			return fmt.Errorf("decode source FlexFEC: %w", err)
		}
		event := decoderEvent{packet: packet.Clone(), receivedAt: time.Now()}
		if err := sendEvent(ctx, events, event); err != nil {
			return err
		}
	}
}

func sendEvent(ctx context.Context, events chan<- decoderEvent, event decoderEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case events <- event:
		return nil
	}
}

func decodeSource(
	ctx context.Context,
	decoder fecDecoder,
	events <-chan decoderEvent,
	packets chan<- repair.Packet,
	sourceFEC *atomic.Uint64,
	invalidFEC *atomic.Uint64,
	dropMediaSequence *uint16,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return io.EOF
			}
			if !event.media {
				sourceFEC.Add(1)
			}
			if event.media && !event.rtx && dropMediaSequence != nil && event.packet.SequenceNumber == *dropMediaSequence {
				dropMediaSequence = nil
				continue
			}
			var recovered []rtp.Packet
			if decoder != nil {
				decoded, err := decoder.Decode(*event.packet)
				if err != nil {
					invalidFEC.Add(1)
				}
				recovered = decoded
			} else if !event.media {
				return errors.New("received FlexFEC packet without a negotiated decoder")
			}
			if event.media {
				packet := repair.Packet{
					RTP:          event.packet,
					ReceivedAt:   event.receivedAt,
					RecoveredRTX: event.rtx,
				}
				if err := sendPacket(ctx, packets, packet); err != nil {
					return err
				}
			}
			for index := range recovered {
				packet := recovered[index]
				if err := sendPacket(ctx, packets, repair.Packet{
					RTP:          &packet,
					ReceivedAt:   time.Now(),
					RecoveredFEC: true,
				}); err != nil {
					return err
				}
			}
		}
	}
}

func sendPacket(ctx context.Context, packets chan<- repair.Packet, packet repair.Packet) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case packets <- packet:
		return nil
	}
}

func forwardDestinationRTCP(
	ctx context.Context,
	sender *webrtc.RTPSender,
	sourcePeer *webrtc.PeerConnection,
	mediaSSRC uint32,
) error {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return fmt.Errorf("read destination RTCP: %w", err)
		}
		forward := keyframeRequests(packets, mediaSSRC)
		if len(forward) == 0 {
			continue
		}
		if err := sourcePeer.WriteRTCP(forward); err != nil {
			return fmt.Errorf("forward keyframe request: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func keyframeRequests(packets []rtcp.Packet, mediaSSRC uint32) []rtcp.Packet {
	forward := make([]rtcp.Packet, 0, len(packets))
	for _, packet := range packets {
		switch value := packet.(type) {
		case *rtcp.PictureLossIndication:
			forward = append(forward, &rtcp.PictureLossIndication{
				SenderSSRC: value.SenderSSRC,
				MediaSSRC:  mediaSSRC,
			})
		case *rtcp.FullIntraRequest:
			entries := append([]rtcp.FIREntry(nil), value.FIR...)
			for index := range entries {
				entries[index].SSRC = mediaSSRC
			}
			forward = append(forward, &rtcp.FullIntraRequest{
				SenderSSRC: value.SenderSSRC,
				MediaSSRC:  mediaSSRC,
				FIR:        entries,
			})
		}
	}
	return forward
}

func stripSourceExtensions(packet *rtp.Packet) {
	packet.Extension = false
	packet.ExtensionProfile = 0
	packet.Extensions = nil
}

func closeSession(session *whipwhep.Session) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionCleanupTimeout)
	defer cancel()
	return session.Close(ctx)
}

func isShutdownError(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}
