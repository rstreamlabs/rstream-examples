package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
)

const (
	maxWHEPBodyBytes        = 128 * 1024
	maxWHEPPatchesPerSecond = 32
	whepPostBurst           = 8
	whepPostsPerSecond      = 4
	whepSessionSetupTimeout = 12 * time.Second
	whepDiagnosticsHeader   = "X-Rstream-Diagnostics-Token"
	whepRetryAfterSeconds   = 1
)

type whepResource struct {
	session          Session
	patchMu          sync.Mutex
	etag             string
	diagnosticsToken string
	patchWindow      time.Time
	patches          int
	restartPending   bool
	deleting         bool
}

type whepServer struct {
	logger           *logs.Logger
	openSession      func(context.Context) (Session, error)
	checkOrigin      func(*http.Request) bool
	operationTimeout time.Duration
	mu               sync.Mutex
	now              func() time.Time
	postLimiter      *whepPostLimiter
	initialRequests  [whepInitialOutcomeCount]atomic.Uint64
	sessions         map[string]*whepResource
}

type whepInitialOutcome uint8

const (
	whepInitialCreated whepInitialOutcome = iota
	whepInitialInvalid
	whepInitialRateLimited
	whepInitialCapacityLimited
	whepInitialNegotiationFailed
	whepInitialInternalFailed
	whepInitialResponseFailed
	whepInitialOutcomeCount
)

type whepPostLimiter struct {
	mu       sync.Mutex
	last     time.Time
	tokens   float64
	rate     float64
	capacity float64
}

func newWHEPServer(
	logger *logs.Logger,
	openSession func(context.Context) (Session, error),
	checkOrigin func(*http.Request) bool,
) *whepServer {
	return &whepServer{
		logger:           logger,
		openSession:      openSession,
		checkOrigin:      checkOrigin,
		operationTimeout: whepSessionSetupTimeout,
		now:              time.Now,
		postLimiter:      newWHEPPostLimiter(whepPostsPerSecond, whepPostBurst),
		sessions:         make(map[string]*whepResource),
	}
}

func (s *whepServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.allowOrigin(w, r) {
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodHead:
		s.handleHead(w)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodOptions:
		s.handleOptions(w)
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodPatch:
		s.handlePatch(w, r)
	case http.MethodDelete:
		s.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "HEAD, GET, OPTIONS, POST, PATCH, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *whepServer) allowOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.checkOrigin == nil || !s.checkOrigin(r) {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Expose-Headers", "Accept-Post, ETag, Location, Retry-After, "+whepDiagnosticsHeader)
	w.Header().Add("Vary", "Origin")
	return true
}

func (s *whepServer) handleHead(w http.ResponseWriter) {
	w.Header().Set("Accept-Post", "application/sdp")
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusOK)
}

func (s *whepServer) handleOptions(w http.ResponseWriter) {
	w.Header().Set("Accept-Post", "application/sdp")
	w.Header().Set("Access-Control-Allow-Methods", "HEAD, GET, OPTIONS, POST, PATCH, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match")
	w.WriteHeader(http.StatusOK)
}

func (s *whepServer) handlePost(w http.ResponseWriter, r *http.Request) {
	outcome := whepInitialInternalFailed
	defer func() { s.initialRequests[outcome].Add(1) }()
	if !hasMediaType(r, "application/sdp") {
		outcome = whepInitialInvalid
		http.Error(w, "Content-Type must be application/sdp", http.StatusUnsupportedMediaType)
		return
	}
	offer, err := readWHEPBody(w, r)
	if err != nil {
		outcome = whepInitialInvalid
		s.writeReadError(w, err, "offer SDP")
		return
	}
	if strings.TrimSpace(offer) == "" {
		outcome = whepInitialInvalid
		http.Error(w, "offer SDP is required", http.StatusBadRequest)
		return
	}
	if allowed, retryAfter := s.postLimiter.allow(s.now()); !allowed {
		outcome = whepInitialRateLimited
		w.Header().Set("Retry-After", fmt.Sprint(max(1, int(math.Ceil(retryAfter.Seconds())))))
		http.Error(w, "WHEP session setup rate is exhausted", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.operationTimeout)
	defer cancel()
	session, err := s.openSession(ctx)
	if err != nil {
		if errors.Is(err, rtc.ErrSessionCapacity) {
			outcome = whepInitialCapacityLimited
			s.logger.Warn("WHEP session capacity exhausted: %v", err)
			w.Header().Set("Retry-After", fmt.Sprint(whepRetryAfterSeconds))
			http.Error(w, "WHEP session capacity is exhausted", http.StatusServiceUnavailable)
			return
		}
		s.logger.Error("WHEP session creation failed: %v", err)
		http.Error(w, "failed to create the WHEP session", http.StatusInternalServerError)
		return
	}
	if session == nil {
		s.logger.Error("WHEP session creation returned no session")
		http.Error(w, "failed to create the WHEP session", http.StatusInternalServerError)
		return
	}
	answer, err := session.HandleWHEPOffer(ctx, offer)
	if err != nil {
		outcome = whepInitialNegotiationFailed
		s.logger.Warn("WHEP session %s offer failed: %v", session.ID(), err)
		session.Close("WHEP offer failed")
		http.Error(w, "failed to negotiate the WHEP session", http.StatusBadRequest)
		return
	}
	resource, err := s.add(session)
	if err != nil {
		s.logger.Error("WHEP session %s registration failed: %v", session.ID(), err)
		session.Close("WHEP registration failed")
		http.Error(w, "failed to register the WHEP session", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Accept-Post", "application/sdp")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("ETag", resource.etag)
	w.Header().Set("Location", "/whep/"+session.ID())
	w.Header().Set(whepDiagnosticsHeader, resource.diagnosticsToken)
	w.WriteHeader(http.StatusCreated)
	if _, err := io.WriteString(w, answer); err != nil {
		outcome = whepInitialResponseFailed
		s.remove(session.ID(), resource)
		session.Close("WHEP answer write failed")
		return
	}
	outcome = whepInitialCreated
}

func newWHEPPostLimiter(rate int, capacity int) *whepPostLimiter {
	if rate <= 0 || capacity <= 0 {
		panic("WHEP POST rate and capacity must be positive")
	}
	return &whepPostLimiter{rate: float64(rate), capacity: float64(capacity), tokens: float64(capacity)}
}

func (l *whepPostLimiter) allow(now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() {
		l.last = now
	} else if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens = min(l.capacity, l.tokens+elapsed.Seconds()*l.rate)
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - l.tokens) / l.rate * float64(time.Second)))
	return false, max(time.Nanosecond, wait)
}

func (s *whepServer) handlePatch(w http.ResponseWriter, r *http.Request) {
	if hasMediaType(r, "application/sdp") {
		http.Error(w, "the WHEP offer/answer exchange is already complete", http.StatusUnprocessableEntity)
		return
	}
	if !hasMediaType(r, "application/trickle-ice-sdpfrag") {
		http.Error(w, "Content-Type must be application/trickle-ice-sdpfrag", http.StatusUnsupportedMediaType)
		return
	}
	id := strings.TrimSpace(r.PathValue("session"))
	resource := s.get(id)
	if resource == nil {
		http.Error(w, "WHEP session not found", http.StatusNotFound)
		return
	}
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" {
		http.Error(w, "If-Match is required", http.StatusPreconditionRequired)
		return
	}
	fragment, err := readWHEPBody(w, r)
	if err != nil {
		s.writeReadError(w, err, "ICE fragment")
		return
	}
	if strings.TrimSpace(fragment) == "" {
		http.Error(w, "ICE fragment is required", http.StatusBadRequest)
		return
	}
	resource.patchMu.Lock()
	if resource.deleting || !s.isCurrent(id, resource) {
		resource.patchMu.Unlock()
		http.Error(w, "WHEP session not found", http.StatusNotFound)
		return
	}
	if !resource.allowPatch(time.Now()) {
		resource.patchMu.Unlock()
		w.Header().Set("Retry-After", "1")
		http.Error(w, "WHEP PATCH rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	restart := ifMatch == "*"
	if !restart && !strongETagMatches(ifMatch, resource.etag) {
		resource.patchMu.Unlock()
		http.Error(w, "ICE session does not match", http.StatusPreconditionFailed)
		return
	}
	if resource.restartPending {
		resource.patchMu.Unlock()
		w.Header().Set("Retry-After", "1")
		http.Error(w, "an ICE restart is already in progress", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.operationTimeout)
	defer cancel()
	if !restart {
		_, err := resource.session.HandleWHEPICE(ctx, fragment, false)
		resource.patchMu.Unlock()
		if err != nil {
			s.logger.Warn("WHEP session %s ICE update failed: %v", id, err)
			http.Error(w, "failed to apply the ICE update", http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	nextETag, err := newWHEPETag()
	if err != nil {
		resource.patchMu.Unlock()
		s.logger.Error("WHEP session %s ETag generation failed: %v", id, err)
		http.Error(w, "failed to prepare the ICE restart", http.StatusInternalServerError)
		return
	}
	resource.restartPending = true
	resource.patchMu.Unlock()
	if err := resource.session.RefreshWHEPICE(ctx); err != nil {
		resource.finishRestart()
		s.logger.Warn("WHEP session %s ICE credential refresh failed: %v", id, err)
		http.Error(w, "failed to refresh ICE credentials", http.StatusServiceUnavailable)
		return
	}
	resource.patchMu.Lock()
	defer resource.patchMu.Unlock()
	defer func() { resource.restartPending = false }()
	if !s.isCurrent(id, resource) {
		http.Error(w, "WHEP session not found", http.StatusNotFound)
		return
	}
	answer, err := resource.session.HandleWHEPICE(ctx, fragment, true)
	if err != nil {
		s.logger.Warn("WHEP session %s ICE update failed: %v", id, err)
		http.Error(w, "failed to apply the ICE update", http.StatusUnprocessableEntity)
		return
	}
	resource.etag = nextETag
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/trickle-ice-sdpfrag")
	w.Header().Set("ETag", nextETag)
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, answer); err != nil {
		s.logger.Warn("WHEP session %s ICE restart answer write failed: %v", id, err)
	}
}

func (r *whepResource) finishRestart() {
	r.patchMu.Lock()
	r.restartPending = false
	r.patchMu.Unlock()
}

func (s *whepServer) writeReadError(w http.ResponseWriter, err error, subject string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, subject+" exceeds 128 KiB", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "failed to read "+subject, http.StatusBadRequest)
}

func readWHEPBody(w http.ResponseWriter, r *http.Request) (string, error) {
	reader := http.MaxBytesReader(w, r.Body, maxWHEPBodyBytes)
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func hasMediaType(r *http.Request, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, expected)
}

func strongETagMatches(raw string, current string) bool {
	for value := range strings.SplitSeq(raw, ",") {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "W/") && value == current {
			return true
		}
	}
	return false
}

func newWHEPETag() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return `"` + hex.EncodeToString(value[:]) + `"`, nil
}

func newWHEPDiagnosticsToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (r *whepResource) allowPatch(now time.Time) bool {
	if r.patchWindow.IsZero() || now.Sub(r.patchWindow) >= time.Second {
		r.patchWindow = now
		r.patches = 1
		return true
	}
	if r.patches >= maxWHEPPatchesPerSecond {
		return false
	}
	r.patches++
	return true
}

func (s *whepServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("session"))
	if id == "" {
		http.Error(w, "WHEP session identifier is required", http.StatusBadRequest)
		return
	}
	resource := s.take(id)
	if resource == nil {
		http.Error(w, "WHEP session not found", http.StatusNotFound)
		return
	}
	resource.patchMu.Lock()
	resource.deleting = true
	resource.patchMu.Unlock()
	resource.session.Close("WHEP session deleted")
	w.WriteHeader(http.StatusOK)
}

func (s *whepServer) add(session Session) (*whepResource, error) {
	id := strings.TrimSpace(session.ID())
	if id == "" {
		return nil, errors.New("the WHEP session identifier is empty")
	}
	etag, err := newWHEPETag()
	if err != nil {
		return nil, fmt.Errorf("generate WHEP ETag: %w", err)
	}
	diagnosticsToken, err := newWHEPDiagnosticsToken()
	if err != nil {
		return nil, fmt.Errorf("generate WHEP diagnostics token: %w", err)
	}
	resource := &whepResource{session: session, etag: etag, diagnosticsToken: diagnosticsToken}
	s.mu.Lock()
	if _, exists := s.sessions[id]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("the WHEP session identifier %q already exists", id)
	}
	s.sessions[id] = resource
	s.mu.Unlock()
	go func() {
		<-session.Done()
		s.remove(id, resource)
	}()
	return resource, nil
}

func (s *whepServer) diagnostics(id string, token string) (rtc.SessionStats, int) {
	resource := s.get(id)
	if resource == nil {
		return rtc.SessionStats{}, http.StatusNotFound
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(resource.diagnosticsToken)) != 1 {
		return rtc.SessionStats{}, http.StatusUnauthorized
	}
	select {
	case <-resource.session.Done():
		return rtc.SessionStats{}, http.StatusNotFound
	default:
		return resource.session.StatsSnapshot(), http.StatusOK
	}
}

func (s *whepServer) get(id string) *whepResource {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *whepServer) isCurrent(id string, expected *whepResource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id] == expected
}

func (s *whepServer) take(id string) *whepResource {
	s.mu.Lock()
	defer s.mu.Unlock()
	resource := s.sessions[id]
	delete(s.sessions, id)
	return resource
}

func (s *whepServer) remove(id string, expected *whepResource) {
	s.mu.Lock()
	if s.sessions[id] == expected {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
}

func (s *whepServer) initialRequestSnapshot() map[string]uint64 {
	return map[string]uint64{
		"capacity_limited":   s.initialRequests[whepInitialCapacityLimited].Load(),
		"created":            s.initialRequests[whepInitialCreated].Load(),
		"internal_failed":    s.initialRequests[whepInitialInternalFailed].Load(),
		"invalid":            s.initialRequests[whepInitialInvalid].Load(),
		"negotiation_failed": s.initialRequests[whepInitialNegotiationFailed].Load(),
		"rate_limited":       s.initialRequests[whepInitialRateLimited].Load(),
		"response_failed":    s.initialRequests[whepInitialResponseFailed].Load(),
	}
}
