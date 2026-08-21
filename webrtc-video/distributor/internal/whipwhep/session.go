package whipwhep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	maxSDPBytes                    = 256 * 1024
	maxCandidatesPerGeneration     = 64
	maxCandidateBytesPerGeneration = 64 * 1024
	failedExchangeCleanupTimeout   = 2 * time.Second
	candidateBatchDelay            = 20 * time.Millisecond
	maxRedirects                   = 3
	maxAuthorizationBytes          = 8 * 1024
)

type Options struct {
	AllowLegacyWildcardETag bool
	TrustedRedirectOrigins  []string
}

type HTTPStatusError struct {
	Operation  string
	StatusCode int
	retryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s: unexpected HTTP status %d", e.Operation, e.StatusCode)
}

func (e *HTTPStatusError) RetryAfter() time.Duration {
	return e.retryAfter
}

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func Permanent(err error) error {
	if err == nil || IsPermanent(err) {
		return err
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

type candidateEvent struct {
	candidate webrtc.ICECandidateInit
	complete  bool
}

type iceFragment struct {
	ufrag      string
	pwd        string
	candidates []webrtc.ICECandidateInit
}

type restartRequest struct {
	ctx           context.Context
	target        *url.URL
	endpoint      *url.URL
	authorization string
	iceServers    []webrtc.ICEServer
	result        chan error
}

type Session struct {
	peer             *webrtc.PeerConnection
	target           *url.URL
	endpoint         *url.URL
	authorization    string
	credentialsMu    sync.RWMutex
	client           *http.Client
	etag             string
	ctx              context.Context
	cancel           context.CancelFunc
	ready            chan struct{}
	candidates       chan candidateEvent
	restarts         chan restartRequest
	workerDone       chan struct{}
	failOnce         sync.Once
	errMu            sync.Mutex
	workerErr        error
	candidateCount   atomic.Uint32
	candidateBytes   atomic.Uint64
	candidateMu      sync.Mutex
	ignoreCandidates atomic.Bool
	closeMu          sync.Mutex
	closing          bool
	closeDone        chan struct{}
	closeErr         error
}

func Exchange(ctx context.Context, peer *webrtc.PeerConnection, endpoint *url.URL, authorization string, client *http.Client, options ...Options) (*Session, error) {
	if peer == nil || endpoint == nil || client == nil {
		return nil, Permanent(errors.New("peer, endpoint, and HTTP client are required"))
	}
	if err := validateEndpoint(endpoint); err != nil {
		return nil, Permanent(err)
	}
	if len(options) > 1 {
		return nil, Permanent(errors.New("at most one WHEP option set is allowed"))
	}
	configuration := Options{}
	if len(options) == 1 {
		configuration = options[0]
	}
	trustedRedirectOrigins, err := redirectOriginSet(endpoint, configuration.TrustedRedirectOrigins)
	if err != nil {
		return nil, Permanent(err)
	}
	authorization, err = normalizeAuthorization(authorization)
	if err != nil {
		return nil, Permanent(err)
	}
	client = withoutRedirects(client)
	sessionCtx, cancel := context.WithCancel(ctx)
	session := &Session{peer: peer, target: cloneURL(endpoint), authorization: strings.TrimSpace(authorization), client: client, ctx: sessionCtx, cancel: cancel, ready: make(chan struct{}), candidates: make(chan candidateEvent, maxCandidatesPerGeneration+1), restarts: make(chan restartRequest, 1), workerDone: make(chan struct{})}
	go session.runCandidateWorker()
	peer.OnICECandidate(session.handleLocalCandidate)
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		session.abort()
		return nil, Permanent(fmt.Errorf("create SDP offer: %w", err))
	}
	wireOffer, err := addRTCPMuxOnly(offer.SDP)
	if err != nil {
		session.abort()
		return nil, Permanent(fmt.Errorf("prepare standards-compliant SDP offer: %w", err))
	}
	result, err := postOffer(sessionCtx, endpoint, wireOffer, session.authorizationHeader(), client, trustedRedirectOrigins)
	if err != nil {
		session.abort()
		return nil, err
	}
	location, err := resolveSessionURL(result.endpoint, result.response.Header.Get("Location"))
	if err != nil {
		drain(result.response.Body)
		_ = result.response.Body.Close()
		session.abort()
		return nil, Permanent(err)
	}
	sessionEstablished := false
	defer func() {
		if !sessionEstablished {
			cleanupFailedExchange(location, session.authorizationHeader(), client)
		}
	}()
	if result.response.StatusCode == http.StatusNotAcceptable {
		if err := session.completeCounterOffer(sessionCtx, result.response, location); err != nil {
			session.abort()
			return nil, err
		}
		sessionEstablished = true
		return session, nil
	}
	if err := requireSDPContentType(result.response.Header.Get("Content-Type")); err != nil {
		drain(result.response.Body)
		_ = result.response.Body.Close()
		session.abort()
		return nil, Permanent(err)
	}
	allowWildcard := configuration.AllowLegacyWildcardETag
	etag, err := requireETag(result.response.Header.Get("ETag"), allowWildcard)
	if err != nil {
		drain(result.response.Body)
		_ = result.response.Body.Close()
		session.abort()
		return nil, Permanent(err)
	}
	answer, err := readBounded(result.response.Body)
	if err != nil {
		_ = result.response.Body.Close()
		session.abort()
		return nil, fmt.Errorf("read SDP answer: %w", err)
	}
	_ = result.response.Body.Close()
	if err := peer.SetLocalDescription(offer); err != nil {
		session.abort()
		return nil, Permanent(fmt.Errorf("set local SDP offer: %w", err))
	}
	if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(answer)}); err != nil {
		session.abort()
		return nil, Permanent(fmt.Errorf("set remote SDP answer: %w", err))
	}
	session.setResourceEndpoint(location)
	session.etag = etag
	close(session.ready)
	if err := session.workerError(); err != nil {
		session.abort()
		return nil, err
	}
	sessionEstablished = true
	return session, nil
}

type postResult struct {
	endpoint *url.URL
	response *http.Response
}

func postOffer(ctx context.Context, endpoint *url.URL, offer string, authorization string, client *http.Client, trustedRedirectOrigins map[string]struct{}) (postResult, error) {
	current := cloneURL(endpoint)
	for redirects := 0; ; redirects++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, current.String(), strings.NewReader(offer))
		if err != nil {
			return postResult{}, Permanent(fmt.Errorf("create SDP request: %w", err))
		}
		request.Header.Set("Accept", "application/sdp")
		request.Header.Set("Content-Type", "application/sdp")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := client.Do(request)
		if err != nil {
			return postResult{}, safeRequestError("exchange SDP", err)
		}
		if response.StatusCode != http.StatusTemporaryRedirect && response.StatusCode != http.StatusPermanentRedirect {
			if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusNotAcceptable {
				drain(response.Body)
				_ = response.Body.Close()
				err := newHTTPStatusError("exchange SDP", response, time.Now())
				if retryableSignalingStatus(response.StatusCode) {
					return postResult{}, err
				}
				return postResult{}, Permanent(err)
			}
			return postResult{endpoint: current, response: response}, nil
		}
		if redirects >= maxRedirects {
			drain(response.Body)
			_ = response.Body.Close()
			return postResult{}, Permanent(errors.New("exchange SDP: redirect limit exceeded"))
		}
		next, err := safeRedirect(current, response.Header.Get("Location"), trustedRedirectOrigins)
		drain(response.Body)
		_ = response.Body.Close()
		if err != nil {
			return postResult{}, Permanent(err)
		}
		current = next
	}
}

func retryableSignalingStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func (s *Session) completeCounterOffer(ctx context.Context, response *http.Response, location *url.URL) error {
	if err := requireSDPContentType(response.Header.Get("Content-Type")); err != nil {
		drain(response.Body)
		_ = response.Body.Close()
		return Permanent(fmt.Errorf("WHEP counter-offer: %w", err))
	}
	deadline, err := counterOfferDeadline(response.Header.Get("Content-Type"), time.Now())
	if err != nil {
		drain(response.Body)
		_ = response.Body.Close()
		return Permanent(err)
	}
	offer, err := readBounded(response.Body)
	if err != nil {
		_ = response.Body.Close()
		return fmt.Errorf("read WHEP counter-offer: %w", err)
	}
	_ = response.Body.Close()
	s.ignoreCandidates.Store(true)
	if err := s.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		return Permanent(fmt.Errorf("set WHEP counter-offer: %w", err))
	}
	answer, err := s.peer.CreateAnswer(nil)
	if err != nil {
		return Permanent(fmt.Errorf("create WHEP counter-offer answer: %w", err))
	}
	gathered := webrtc.GatheringCompletePromise(s.peer)
	if err := s.peer.SetLocalDescription(answer); err != nil {
		return Permanent(fmt.Errorf("set WHEP counter-offer answer: %w", err))
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("gather WHEP counter-offer candidates: %w", ctx.Err())
	case <-gathered:
	}
	local := s.peer.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return Permanent(errors.New("WHEP counter-offer answer is unavailable"))
	}
	wireAnswer, err := addRTCPMuxOnly(local.SDP)
	if err != nil {
		return Permanent(fmt.Errorf("prepare WHEP counter-offer answer: %w", err))
	}
	if err := requireFreshCounterOffer(deadline, time.Now()); err != nil {
		return Permanent(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, location.String(), strings.NewReader(wireAnswer))
	if err != nil {
		return Permanent(fmt.Errorf("create WHEP counter-offer answer request: %w", err))
	}
	request.Header.Set("Content-Type", "application/sdp")
	if authorization := s.authorizationHeader(); authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	patchResponse, err := s.client.Do(request)
	if err != nil {
		return safeRequestError("send WHEP counter-offer answer", err)
	}
	drain(patchResponse.Body)
	_ = patchResponse.Body.Close()
	if patchResponse.StatusCode != http.StatusNoContent {
		err := newHTTPStatusError("send WHEP counter-offer answer", patchResponse, time.Now())
		if retryableSignalingStatus(patchResponse.StatusCode) {
			return err
		}
		return Permanent(err)
	}
	s.setResourceEndpoint(location)
	close(s.ready)
	if err := s.workerError(); err != nil {
		return err
	}
	return nil
}

func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("the media session close context is required")
	}
	s.closeMu.Lock()
	if s.closing {
		done := s.closeDone
		s.closeMu.Unlock()
		select {
		case <-done:
			s.closeMu.Lock()
			err := s.closeErr
			s.closeMu.Unlock()
			return err
		case <-ctx.Done():
			return fmt.Errorf("wait for media session close: %w", ctx.Err())
		}
	}
	s.closing = true
	s.closeDone = make(chan struct{})
	s.closeMu.Unlock()
	err := s.closeSession(ctx)
	s.closeMu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.closeMu.Unlock()
	return err
}

func (s *Session) closeSession(ctx context.Context) error {
	s.cancel()
	var closeErr error
	select {
	case <-s.workerDone:
	case <-ctx.Done():
		closeErr = fmt.Errorf("stop candidate worker: %w", ctx.Err())
	}
	endpoint, authorization := s.requestCredentials()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err == nil {
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, requestErr := s.client.Do(request)
		if requestErr != nil {
			err = safeRequestError("delete media session", requestErr)
		} else {
			drain(response.Body)
			_ = response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = newHTTPStatusError("delete media session", response, time.Now())
				if !retryableSignalingStatus(response.StatusCode) {
					err = Permanent(err)
				}
			}
		}
	}
	peerErr := s.peer.Close()
	closeErr = errors.Join(closeErr, s.workerError(), err)
	if peerErr != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("close peer connection: %w", peerErr))
	}
	return closeErr
}

func (s *Session) SetAuthorization(authorization string) error {
	value, err := normalizeAuthorization(authorization)
	if err != nil {
		return Permanent(err)
	}
	s.credentialsMu.Lock()
	s.authorization = value
	s.credentialsMu.Unlock()
	return nil
}

func (s *Session) SetCredentials(target *url.URL, authorization string) error {
	if err := validateEndpoint(target); err != nil {
		return Permanent(err)
	}
	value, err := normalizeAuthorization(authorization)
	if err != nil {
		return Permanent(err)
	}
	s.credentialsMu.Lock()
	defer s.credentialsMu.Unlock()
	if s.target == nil || credentialTarget(s.target) != credentialTarget(target) {
		return Permanent(errors.New("refreshed credentials changed the active endpoint"))
	}
	if s.endpoint == nil {
		return Permanent(errors.New("media session endpoint is unavailable"))
	}
	refreshed := cloneURL(s.endpoint)
	copyEdgeCredential(target, refreshed)
	s.target = cloneURL(target)
	s.endpoint = refreshed
	s.authorization = value
	return nil
}

func (s *Session) Restart(ctx context.Context, target *url.URL, authorization string, iceServers []webrtc.ICEServer) error {
	endpoint, value, err := s.refreshedRequestCredentials(target, authorization)
	if err != nil {
		return err
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() {
		stop()
		cancel()
	}()
	request := restartRequest{ctx: operationCtx, target: cloneURL(target), endpoint: endpoint, authorization: value, iceServers: copyICEServers(iceServers), result: make(chan error, 1)}
	select {
	case <-operationCtx.Done():
		return fmt.Errorf("schedule WHEP ICE restart: %w", operationCtx.Err())
	case s.restarts <- request:
	}
	select {
	case <-operationCtx.Done():
		return fmt.Errorf("complete WHEP ICE restart: %w", operationCtx.Err())
	case err := <-request.result:
		return err
	}
}

func (s *Session) refreshedRequestCredentials(target *url.URL, authorization string) (*url.URL, string, error) {
	if err := validateEndpoint(target); err != nil {
		return nil, "", Permanent(err)
	}
	value, err := normalizeAuthorization(authorization)
	if err != nil {
		return nil, "", Permanent(err)
	}
	s.credentialsMu.RLock()
	defer s.credentialsMu.RUnlock()
	if s.target == nil || credentialTarget(s.target) != credentialTarget(target) {
		return nil, "", Permanent(errors.New("refreshed credentials changed the active endpoint"))
	}
	if s.endpoint == nil {
		return nil, "", Permanent(errors.New("media session endpoint is unavailable"))
	}
	endpoint := cloneURL(s.endpoint)
	copyEdgeCredential(target, endpoint)
	return endpoint, value, nil
}

func (s *Session) handleLocalCandidate(candidate *webrtc.ICECandidate) {
	s.candidateMu.Lock()
	if s.ignoreCandidates.Load() {
		s.candidateMu.Unlock()
		return
	}
	var failure error
	if candidate == nil {
		select {
		case s.candidates <- candidateEvent{complete: true}:
		case <-s.ctx.Done():
		default:
			failure = Permanent(errors.New("local ICE candidate queue is full"))
		}
		s.candidateMu.Unlock()
		if failure != nil {
			s.fail(failure)
		}
		return
	}
	init := candidate.ToJSON()
	count := s.candidateCount.Add(1)
	bytes := s.candidateBytes.Add(uint64(len(init.Candidate)))
	if count > maxCandidatesPerGeneration || bytes > maxCandidateBytesPerGeneration {
		failure = Permanent(errors.New("local ICE candidate generation exceeds its resource limit"))
		s.candidateMu.Unlock()
		s.fail(failure)
		return
	}
	select {
	case s.candidates <- candidateEvent{candidate: init}:
	case <-s.ctx.Done():
	default:
		failure = Permanent(errors.New("local ICE candidate queue is full"))
	}
	s.candidateMu.Unlock()
	if failure != nil {
		s.fail(failure)
	}
}

func (s *Session) runCandidateWorker() {
	defer close(s.workerDone)
	select {
	case <-s.ctx.Done():
		return
	case <-s.ready:
	}
	if s.etag == "" {
		<-s.ctx.Done()
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case request := <-s.restarts:
			request.result <- s.performRestart(request)
		case event := <-s.candidates:
			batch, complete := s.collectCandidateBatch(event)
			if len(batch) == 0 && !complete {
				continue
			}
			if err := s.patchCandidates(batch, complete); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

func (s *Session) collectCandidateBatch(first candidateEvent) ([]webrtc.ICECandidateInit, bool) {
	batch := make([]webrtc.ICECandidateInit, 0, 8)
	complete := first.complete
	if first.candidate.Candidate != "" {
		batch = append(batch, first.candidate)
	}
	if complete {
		return batch, true
	}
	timer := time.NewTimer(candidateBatchDelay)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return batch, complete
		case event := <-s.candidates:
			complete = complete || event.complete
			if event.candidate.Candidate != "" {
				batch = append(batch, event.candidate)
			}
			if complete {
				return batch, true
			}
		case <-timer.C:
			return batch, complete
		}
	}
}

func (s *Session) patchCandidates(candidates []webrtc.ICECandidateInit, complete bool) error {
	local := s.peer.LocalDescription()
	if local == nil {
		return Permanent(errors.New("patch ICE candidates: local SDP is unavailable"))
	}
	fragment, err := candidateFragment(local.SDP, candidates, complete)
	if err != nil {
		return Permanent(fmt.Errorf("patch ICE candidates: %w", err))
	}
	endpoint, authorization := s.requestCredentials()
	request, err := http.NewRequestWithContext(s.ctx, http.MethodPatch, endpoint.String(), strings.NewReader(fragment))
	if err != nil {
		return Permanent(fmt.Errorf("create ICE candidate request: %w", err))
	}
	request.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
	request.Header.Set("If-Match", s.etag)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := s.client.Do(request)
	if err != nil {
		if s.ctx.Err() != nil {
			return nil
		}
		return safeRequestError("patch ICE candidates", err)
	}
	defer func() { _ = response.Body.Close() }()
	drain(response.Body)
	if response.StatusCode != http.StatusNoContent {
		err := newHTTPStatusError("patch ICE candidates", response, time.Now())
		if retryableSignalingStatus(response.StatusCode) {
			return err
		}
		return Permanent(err)
	}
	return nil
}

func (s *Session) performRestart(restart restartRequest) (err error) {
	if err := restart.ctx.Err(); err != nil {
		return fmt.Errorf("start WHEP ICE restart: %w", err)
	}
	if s.etag == "" {
		return Permanent(errors.New("WHEP endpoint did not enable ICE restart"))
	}
	s.beginRestartGeneration()
	generationActive := false
	defer func() {
		if !generationActive {
			s.endRestartGeneration()
		}
	}()
	configuration := s.peer.GetConfiguration()
	configuration.ICEServers = copyICEServers(restart.iceServers)
	if err := s.peer.SetConfiguration(configuration); err != nil {
		return Permanent(fmt.Errorf("apply refreshed ICE servers: %w", err))
	}
	offer, err := s.peer.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return Permanent(fmt.Errorf("create WHEP ICE restart offer: %w", err))
	}
	fragment, err := restartICEFragment(offer.SDP)
	if err != nil {
		return Permanent(fmt.Errorf("prepare WHEP ICE restart fragment: %w", err))
	}
	request, err := http.NewRequestWithContext(restart.ctx, http.MethodPatch, restart.endpoint.String(), strings.NewReader(fragment))
	if err != nil {
		return Permanent(fmt.Errorf("create WHEP ICE restart request: %w", err))
	}
	request.Header.Set("Accept", "application/trickle-ice-sdpfrag")
	request.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
	request.Header.Set("If-Match", "*")
	if restart.authorization != "" {
		request.Header.Set("Authorization", restart.authorization)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return safeRequestError("restart WHEP ICE", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		drain(response.Body)
		err := newHTTPStatusError("restart WHEP ICE", response, time.Now())
		if retryableSignalingStatus(response.StatusCode) {
			return err
		}
		return Permanent(err)
	}
	if err := requireMediaType(response.Header.Get("Content-Type"), "application/trickle-ice-sdpfrag"); err != nil {
		drain(response.Body)
		return Permanent(err)
	}
	etag, err := requireETag(response.Header.Get("ETag"), false)
	if err != nil {
		drain(response.Body)
		return Permanent(err)
	}
	body, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("read WHEP ICE restart answer: %w", err)
	}
	answer, err := parseICEFragment(string(body))
	if err != nil {
		return Permanent(fmt.Errorf("parse WHEP ICE restart answer: %w", err))
	}
	if len(answer.candidates) == 0 {
		return Permanent(errors.New("WHEP ICE restart answer has no candidates"))
	}
	remote := s.peer.RemoteDescription()
	if remote == nil || strings.TrimSpace(remote.SDP) == "" {
		return Permanent(errors.New("WHEP remote description is unavailable"))
	}
	remoteSDP, err := replaceICECredentials(remote.SDP, answer.ufrag, answer.pwd)
	if err != nil {
		return Permanent(fmt.Errorf("apply WHEP ICE restart credentials: %w", err))
	}
	s.endRestartGeneration()
	generationActive = true
	if err := s.peer.SetLocalDescription(offer); err != nil {
		restartErr := Permanent(fmt.Errorf("set WHEP ICE restart offer: %w", err))
		s.fail(restartErr)
		return restartErr
	}
	if err := s.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: remoteSDP}); err != nil {
		restartErr := Permanent(fmt.Errorf("set WHEP ICE restart answer: %w", err))
		s.fail(restartErr)
		return restartErr
	}
	for _, candidate := range answer.candidates {
		if err := s.peer.AddICECandidate(candidate); err != nil {
			restartErr := Permanent(fmt.Errorf("apply WHEP ICE restart candidate: %w", err))
			s.fail(restartErr)
			return restartErr
		}
	}
	if err := s.SetCredentials(restart.target, restart.authorization); err != nil {
		s.fail(err)
		return err
	}
	s.etag = etag
	return nil
}

func (s *Session) beginRestartGeneration() {
	s.candidateMu.Lock()
	s.ignoreCandidates.Store(true)
	for {
		select {
		case <-s.candidates:
		default:
			s.candidateCount.Store(0)
			s.candidateBytes.Store(0)
			s.candidateMu.Unlock()
			return
		}
	}
}

func (s *Session) endRestartGeneration() {
	s.candidateMu.Lock()
	s.ignoreCandidates.Store(false)
	s.candidateMu.Unlock()
}

func copyICEServers(servers []webrtc.ICEServer) []webrtc.ICEServer {
	copy := make([]webrtc.ICEServer, len(servers))
	for index, server := range servers {
		copy[index] = server
		copy[index].URLs = append([]string(nil), server.URLs...)
	}
	return copy
}

func newHTTPStatusError(operation string, response *http.Response, now time.Time) *HTTPStatusError {
	return &HTTPStatusError{
		Operation:  operation,
		StatusCode: response.StatusCode,
		retryAfter: parseRetryAfter(response.Header.Get("Retry-After"), now),
	}
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		const maximumSeconds = uint64((1<<63 - 1) / int64(time.Second))
		if seconds > maximumSeconds {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil || !retryAt.After(now) {
		return 0
	}
	return retryAt.Sub(now)
}

func (s *Session) fail(err error) {
	s.failOnce.Do(func() {
		s.errMu.Lock()
		s.workerErr = err
		s.errMu.Unlock()
		s.cancel()
		_ = s.peer.Close()
	})
}

func (s *Session) workerError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.workerErr
}

func (s *Session) authorizationHeader() string {
	s.credentialsMu.RLock()
	defer s.credentialsMu.RUnlock()
	return s.authorization
}

func (s *Session) requestCredentials() (*url.URL, string) {
	s.credentialsMu.RLock()
	defer s.credentialsMu.RUnlock()
	return cloneURL(s.endpoint), s.authorization
}

func (s *Session) setResourceEndpoint(endpoint *url.URL) {
	s.credentialsMu.Lock()
	s.endpoint = cloneURL(endpoint)
	s.credentialsMu.Unlock()
}

func (s *Session) abort() {
	s.cancel()
	<-s.workerDone
}

func addRTCPMuxOnly(raw string) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	for _, media := range description.MediaDescriptions {
		if media == nil || media.MediaName.Port.Value == 0 || !hasAttribute(media.Attributes, "rtcp-mux") {
			continue
		}
		if !hasAttribute(media.Attributes, "rtcp-mux-only") {
			media.Attributes = append(media.Attributes, sdp.Attribute{Key: "rtcp-mux-only"})
		}
		if !hasAttribute(media.Attributes, "msid") {
			media.Attributes = append(media.Attributes, sdp.Attribute{Key: "msid", Value: "rstream-whep rstream-video"})
		}
	}
	encoded, err := description.Marshal()
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func candidateFragment(raw string, candidates []webrtc.ICECandidateInit, complete bool) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	if len(candidates) == 0 && !complete {
		return "", errors.New("candidate batch is empty")
	}
	var media *sdp.MediaDescription
	if len(candidates) > 0 {
		line := candidates[0].SDPMLineIndex
		if line == nil || int(*line) >= len(description.MediaDescriptions) {
			return "", errors.New("candidate media index is invalid")
		}
		for _, candidate := range candidates {
			if candidate.SDPMLineIndex == nil || *candidate.SDPMLineIndex != *line {
				return "", errors.New("candidate batch spans multiple BUNDLE media sections")
			}
		}
		media = description.MediaDescriptions[*line]
	} else {
		bundle := strings.Fields(groupBundle(description.Attributes))
		if len(bundle) == 0 {
			return "", errors.New("SDP has no BUNDLE-tagged media section")
		}
		for _, candidate := range description.MediaDescriptions {
			if candidate == nil {
				continue
			}
			mid, _ := candidate.Attribute("mid")
			if strings.TrimSpace(mid) == bundle[0] {
				media = candidate
				break
			}
		}
	}
	if media == nil {
		return "", errors.New("candidate media section is unavailable")
	}
	mid, ok := media.Attribute("mid")
	if !ok || strings.TrimSpace(mid) == "" {
		return "", errors.New("candidate media section has no mid")
	}
	ufrag := inheritedAttribute(description.Attributes, media.Attributes, "ice-ufrag")
	pwd := inheritedAttribute(description.Attributes, media.Attributes, "ice-pwd")
	if ufrag == "" || pwd == "" {
		return "", errors.New("candidate media section has no ICE credentials")
	}
	var fragment strings.Builder
	writeSelectedAttributes(&fragment, description.Attributes, "ice-lite", "ice-options", "ice-pacing")
	if bundle := groupBundle(description.Attributes); bundle != "" {
		fragment.WriteString("a=group:BUNDLE " + bundle + "\r\n")
	}
	fragment.WriteString("m=" + media.MediaName.String() + "\r\n")
	fragment.WriteString("a=mid:" + strings.TrimSpace(mid) + "\r\n")
	fragment.WriteString("a=ice-ufrag:" + ufrag + "\r\n")
	fragment.WriteString("a=ice-pwd:" + pwd + "\r\n")
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.Candidate)
		if !strings.HasPrefix(value, "candidate:") {
			return "", errors.New("candidate syntax is invalid")
		}
		fragment.WriteString("a=" + value + "\r\n")
	}
	if complete {
		fragment.WriteString("a=end-of-candidates\r\n")
	}
	return fragment.String(), nil
}

func completeICEFragment(raw string) (string, error) {
	return iceFragmentFromDescription(raw, true, true)
}

func restartICEFragment(raw string) (string, error) {
	return iceFragmentFromDescription(raw, false, false)
}

func iceFragmentFromDescription(raw string, complete bool, requireCandidates bool) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	bundle := groupBundle(description.Attributes)
	fields := strings.Fields(bundle)
	if len(fields) == 0 {
		return "", errors.New("SDP has no BUNDLE-tagged media section")
	}
	var media *sdp.MediaDescription
	for _, candidate := range description.MediaDescriptions {
		if candidate == nil {
			continue
		}
		mid, _ := candidate.Attribute("mid")
		if strings.TrimSpace(mid) == fields[0] {
			media = candidate
			break
		}
	}
	if media == nil {
		return "", errors.New("SDP BUNDLE-tagged media section is unavailable")
	}
	ufrag := inheritedAttribute(description.Attributes, media.Attributes, "ice-ufrag")
	pwd := inheritedAttribute(description.Attributes, media.Attributes, "ice-pwd")
	if ufrag == "" || pwd == "" {
		return "", errors.New("SDP has no ICE credentials")
	}
	var fragment strings.Builder
	writeSelectedAttributes(&fragment, description.Attributes, "ice-lite", "ice-options", "ice-pacing")
	fragment.WriteString("a=group:BUNDLE " + bundle + "\r\n")
	fragment.WriteString("m=" + media.MediaName.String() + "\r\n")
	fragment.WriteString("a=mid:" + fields[0] + "\r\n")
	fragment.WriteString("a=ice-ufrag:" + ufrag + "\r\n")
	fragment.WriteString("a=ice-pwd:" + pwd + "\r\n")
	candidates := 0
	candidateBytes := 0
	for _, attribute := range media.Attributes {
		if attribute.Key != "candidate" {
			continue
		}
		value := strings.TrimSpace(attribute.Value)
		if value == "" {
			return "", errors.New("SDP contains an empty ICE candidate")
		}
		candidates++
		candidateBytes += len(value)
		if candidates > maxCandidatesPerGeneration || candidateBytes > maxCandidateBytesPerGeneration {
			return "", errors.New("ICE candidate generation exceeds its resource limit")
		}
		fragment.WriteString("a=candidate:" + value + "\r\n")
	}
	if requireCandidates && candidates == 0 {
		return "", errors.New("SDP contains no ICE candidates")
	}
	if complete {
		fragment.WriteString("a=end-of-candidates\r\n")
	}
	return fragment.String(), nil
}

func parseICEFragment(raw string) (iceFragment, error) {
	if strings.TrimSpace(raw) == "" {
		return iceFragment{}, errors.New("ICE fragment is empty")
	}
	const prefix = "v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n"
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(prefix + normalizeSDPLines(raw))); err != nil {
		return iceFragment{}, err
	}
	fragment := iceFragment{}
	if err := mergeICECredentials(&fragment, description.Attributes); err != nil {
		return iceFragment{}, err
	}
	candidateBytes := 0
	for _, media := range description.MediaDescriptions {
		if media == nil {
			continue
		}
		if err := mergeICECredentials(&fragment, media.Attributes); err != nil {
			return iceFragment{}, err
		}
		mid, ok := media.Attribute("mid")
		for _, attribute := range media.Attributes {
			if attribute.Key != "candidate" {
				continue
			}
			value := strings.TrimSpace(attribute.Value)
			if !ok || strings.TrimSpace(mid) == "" || value == "" {
				return iceFragment{}, errors.New("ICE candidate media section is invalid")
			}
			candidateBytes += len(value)
			if len(fragment.candidates) >= maxCandidatesPerGeneration || candidateBytes > maxCandidateBytesPerGeneration {
				return iceFragment{}, errors.New("ICE candidate generation exceeds its resource limit")
			}
			midCopy := strings.TrimSpace(mid)
			fragment.candidates = append(fragment.candidates, webrtc.ICECandidateInit{Candidate: "candidate:" + value, SDPMid: &midCopy, UsernameFragment: optionalString(fragment.ufrag)})
		}
	}
	if fragment.ufrag == "" || fragment.pwd == "" {
		return iceFragment{}, errors.New("ICE fragment has incomplete credentials")
	}
	return fragment, nil
}

func mergeICECredentials(fragment *iceFragment, values []sdp.Attribute) error {
	ufrag, hasUfrag := attribute(values, "ice-ufrag")
	pwd, hasPwd := attribute(values, "ice-pwd")
	if hasUfrag != hasPwd {
		return errors.New("ICE fragment credentials are incomplete")
	}
	if !hasUfrag {
		return nil
	}
	ufrag = strings.TrimSpace(ufrag)
	pwd = strings.TrimSpace(pwd)
	if ufrag == "" || pwd == "" {
		return errors.New("ICE fragment credentials are empty")
	}
	if fragment.ufrag != "" && (fragment.ufrag != ufrag || fragment.pwd != pwd) {
		return errors.New("ICE fragment contains conflicting credentials")
	}
	fragment.ufrag = ufrag
	fragment.pwd = pwd
	return nil
}

func replaceICECredentials(raw string, ufrag string, pwd string) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	replaced := replaceICEAttributes(&description.Attributes, ufrag, pwd)
	for _, media := range description.MediaDescriptions {
		if media != nil {
			replaced = replaceICEAttributes(&media.Attributes, ufrag, pwd) || replaced
		}
	}
	if !replaced {
		return "", errors.New("SDP has no ICE credentials")
	}
	encoded, err := description.Marshal()
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func replaceICEAttributes(values *[]sdp.Attribute, ufrag string, pwd string) bool {
	replaced := false
	filtered := (*values)[:0]
	for _, value := range *values {
		switch value.Key {
		case "ice-ufrag":
			value.Value = ufrag
			replaced = true
		case "ice-pwd":
			value.Value = pwd
			replaced = true
		case "candidate", "end-of-candidates", "remote-candidates":
			continue
		}
		filtered = append(filtered, value)
	}
	*values = filtered
	return replaced
}

func normalizeSDPLines(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return strings.ReplaceAll(strings.TrimSpace(raw), "\n", "\r\n") + "\r\n"
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func inheritedAttribute(session []sdp.Attribute, media []sdp.Attribute, key string) string {
	if value, ok := attribute(media, key); ok {
		return strings.TrimSpace(value)
	}
	value, _ := attribute(session, key)
	return strings.TrimSpace(value)
}

func attribute(attributes []sdp.Attribute, key string) (string, bool) {
	for _, candidate := range attributes {
		if candidate.Key == key {
			return candidate.Value, true
		}
	}
	return "", false
}

func hasAttribute(attributes []sdp.Attribute, key string) bool {
	_, ok := attribute(attributes, key)
	return ok
}

func groupBundle(attributes []sdp.Attribute) string {
	for _, candidate := range attributes {
		fields := strings.Fields(candidate.Value)
		if candidate.Key == "group" && len(fields) > 1 && strings.EqualFold(fields[0], "BUNDLE") {
			return strings.Join(fields[1:], " ")
		}
	}
	return ""
}

func writeSelectedAttributes(out *strings.Builder, attributes []sdp.Attribute, keys ...string) {
	for _, candidate := range attributes {
		for _, key := range keys {
			if candidate.Key != key {
				continue
			}
			out.WriteString("a=" + candidate.Key)
			if candidate.Value != "" {
				out.WriteString(":" + candidate.Value)
			}
			out.WriteString("\r\n")
		}
	}
}

func requireETag(raw string, allowWildcard bool) (string, error) {
	value := strings.TrimSpace(raw)
	if allowWildcard && value == "*" {
		return value, nil
	}
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' || strings.HasPrefix(value, "W/") {
		return "", errors.New("SDP response did not provide a strong ETag")
	}
	for index := 1; index < len(value)-1; index++ {
		character := value[index]
		if character < 0x21 || character == 0x22 || character == 0x7f {
			return "", errors.New("SDP response did not provide a strong ETag")
		}
	}
	return value, nil
}

func counterOfferDeadline(raw string, now time.Time) (time.Time, error) {
	_, parameters, err := mime.ParseMediaType(raw)
	if err != nil {
		return time.Time{}, errors.New("WHEP counter-offer Content-Type is invalid")
	}
	value, ok := parameters["valid-until"]
	if !ok {
		return now.Add(30 * time.Second), nil
	}
	deadline, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}, errors.New("WHEP counter-offer valid-until is invalid")
	}
	if err := requireFreshCounterOffer(deadline, now); err != nil {
		return time.Time{}, err
	}
	return deadline, nil
}

func requireFreshCounterOffer(deadline time.Time, now time.Time) error {
	if !deadline.After(now) {
		return errors.New("WHEP counter-offer expired")
	}
	return nil
}

func requireSDPContentType(raw string) error {
	if err := requireMediaType(raw, "application/sdp"); err != nil {
		return errors.New("SDP response Content-Type is not application/sdp")
	}
	return nil
}

func requireMediaType(raw string, expected string) error {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || !strings.EqualFold(mediaType, expected) {
		return fmt.Errorf("response Content-Type is not %s", expected)
	}
	return nil
}

func resolveSessionURL(endpoint *url.URL, rawLocation string) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(rawLocation))
	if err != nil || rawLocation == "" {
		return nil, errors.New("SDP response Location is invalid")
	}
	location := endpoint.ResolveReference(reference)
	if location.Scheme != endpoint.Scheme || !strings.EqualFold(location.Host, endpoint.Host) || location.User != nil || location.Fragment != "" {
		return nil, errors.New("SDP response Location changed origin")
	}
	if err := validateEndpoint(location); err != nil {
		return nil, fmt.Errorf("SDP response Location is invalid: %w", err)
	}
	copyEdgeCredential(endpoint, location)
	return location, nil
}

func safeRedirect(current *url.URL, rawLocation string, trustedOrigins map[string]struct{}) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(rawLocation))
	if err != nil || rawLocation == "" {
		return nil, errors.New("exchange SDP: redirect Location is invalid")
	}
	next := current.ResolveReference(reference)
	if next.Scheme != current.Scheme || next.User != nil || next.Fragment != "" {
		return nil, errors.New("exchange SDP: redirect changed scheme or contained credentials")
	}
	if err := validateEndpoint(next); err != nil {
		return nil, fmt.Errorf("exchange SDP: redirect Location is invalid: %w", err)
	}
	if _, trusted := trustedOrigins[urlOrigin(next)]; !trusted {
		return nil, errors.New("exchange SDP: redirect changed to an untrusted origin")
	}
	if urlOrigin(next) == urlOrigin(current) {
		copyEdgeCredential(current, next)
	}
	return next, nil
}

func redirectOriginSet(endpoint *url.URL, configured []string) (map[string]struct{}, error) {
	origins := map[string]struct{}{urlOrigin(endpoint): {}}
	for _, raw := range configured {
		candidate, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || candidate.Host == "" || candidate.User != nil || candidate.Fragment != "" || candidate.RawQuery != "" || (candidate.Path != "" && candidate.Path != "/") {
			return nil, errors.New("trusted WHEP redirect origin is invalid")
		}
		if candidate.Scheme != endpoint.Scheme {
			return nil, errors.New("trusted WHEP redirect origin changed scheme")
		}
		origins[urlOrigin(candidate)] = struct{}{}
	}
	return origins, nil
}

func urlOrigin(value *url.URL) string {
	return strings.ToLower(value.Scheme + "://" + value.Host)
}

func readBounded(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxSDPBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSDPBytes {
		return nil, Permanent(fmt.Errorf("SDP exceeds %d bytes", maxSDPBytes))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, Permanent(errors.New("SDP is empty"))
	}
	return body, nil
}

func drain(reader io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, maxSDPBytes))
}

func withoutRedirects(client *http.Client) *http.Client {
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func cloneURL(value *url.URL) *url.URL {
	clone := *value
	return &clone
}

func validateEndpoint(endpoint *url.URL) error {
	if endpoint == nil || endpoint.Host == "" {
		return errors.New("endpoint is required")
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return errors.New("endpoint scheme must be HTTP or HTTPS")
	}
	if endpoint.User != nil || endpoint.Fragment != "" {
		return errors.New("endpoint contains credentials or a fragment")
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil {
		return errors.New("endpoint query is invalid")
	}
	tokens, present := query["rstream.token"]
	if present && (len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "") {
		return errors.New("endpoint edge credential is invalid")
	}
	return nil
}

func credentialTarget(endpoint *url.URL) string {
	target := cloneURL(endpoint)
	query := target.Query()
	query.Del("rstream.token")
	target.RawQuery = query.Encode()
	return target.String()
}

func copyEdgeCredential(source *url.URL, destination *url.URL) {
	query := destination.Query()
	query.Del("rstream.token")
	if token := source.Query().Get("rstream.token"); token != "" {
		query.Set("rstream.token", token)
	}
	destination.RawQuery = query.Encode()
}

func normalizeAuthorization(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) > maxAuthorizationBytes || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("authorization is invalid")
	}
	return value, nil
}

func safeRequestError(operation string, err error) error {
	var requestError *url.Error
	if errors.As(err, &requestError) {
		return fmt.Errorf("%s: %w", operation, requestError.Err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func cleanupFailedExchange(endpoint *url.URL, authorization string, client *http.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), failedExchangeCleanupTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		return
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	drain(response.Body)
	_ = response.Body.Close()
}
