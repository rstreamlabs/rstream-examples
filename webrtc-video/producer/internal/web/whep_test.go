package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
)

type fakeWHEPSession struct {
	id           string
	answer       string
	answerErr    error
	handleOffer  func(context.Context, string) (string, error)
	refreshICE   func(context.Context) error
	handleICE    func(context.Context, string, bool) (string, error)
	iceAnswer    string
	iceErr       error
	iceCalls     chan fakeWHEPICECall
	done         chan struct{}
	closeOnce    sync.Once
	closeCount   atomic.Uint32
	closeReasons chan string
	onClose      func()
	stats        rtc.SessionStats
}

type fakeWHEPICECall struct {
	fragment string
	restart  bool
}

func newFakeWHEPSession(id string) *fakeWHEPSession {
	return &fakeWHEPSession{
		id:           id,
		answer:       "v=0\r\n",
		iceAnswer:    "a=ice-ufrag:new\r\n",
		iceCalls:     make(chan fakeWHEPICECall, maxWHEPPatchesPerSecond+8),
		done:         make(chan struct{}),
		closeReasons: make(chan string, 1),
	}
}

func (s *fakeWHEPSession) ID() string {
	return s.id
}

func (s *fakeWHEPSession) Done() <-chan struct{} {
	return s.done
}

func (s *fakeWHEPSession) HandleWHEPOffer(ctx context.Context, offer string) (string, error) {
	if s.handleOffer != nil {
		return s.handleOffer(ctx, offer)
	}
	return s.answer, s.answerErr
}

func (s *fakeWHEPSession) HandleWHEPICE(ctx context.Context, fragment string, restart bool) (string, error) {
	if s.handleICE != nil {
		return s.handleICE(ctx, fragment, restart)
	}
	s.iceCalls <- fakeWHEPICECall{fragment: fragment, restart: restart}
	return s.iceAnswer, s.iceErr
}

func (s *fakeWHEPSession) RefreshWHEPICE(ctx context.Context) error {
	if s.refreshICE != nil {
		return s.refreshICE(ctx)
	}
	return nil
}

func (s *fakeWHEPSession) StatsSnapshot() rtc.SessionStats {
	return s.stats
}

func (s *fakeWHEPSession) Close(reason string) {
	s.closeCount.Add(1)
	s.closeOnce.Do(func() {
		if s.onClose != nil {
			s.onClose()
		}
		s.closeReasons <- reason
		close(s.done)
	})
}

func TestWHEPOptionsAdvertisesTrickleICE(t *testing.T) {
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return nil, errors.New("not implemented")
	})
	req := httptest.NewRequest(http.MethodOptions, "/whep", nil)
	req.Header.Set("Origin", "https://platform.example")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("OPTIONS status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Accept-Post"); got != "application/sdp" {
		t.Fatalf("Accept-Post = %q, want application/sdp", got)
	}
	if methods := res.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, http.MethodPatch) {
		t.Fatalf("OPTIONS does not advertise Trickle ICE: %q", methods)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "https://platform.example" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want platform origin", got)
	}
	if got := res.Header().Get("Access-Control-Expose-Headers"); got != "Accept-Post, ETag, Location, Retry-After, X-Rstream-Diagnostics-Token" {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
}

func TestWHEPDiscoveryAndResourcesHaveNoRepresentation(t *testing.T) {
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return newFakeWHEPSession("session-1"), nil
	})
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req := httptest.NewRequest(method, "/whep", nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		wantStatus := http.StatusNoContent
		if method == http.MethodHead {
			wantStatus = http.StatusOK
		}
		if res.Code != wantStatus {
			t.Fatalf("%s status = %d, want %d", method, res.Code, wantStatus)
		}
		if got := res.Header().Get("Content-Type"); got != "application/sdp" {
			t.Fatalf("%s Content-Type = %q, want application/sdp", method, got)
		}
		if res.Body.Len() != 0 {
			t.Fatalf("%s body length = %d, want 0", method, res.Body.Len())
		}
	}
}

func TestWHEPRejectsInvalidBrowserOrigin(t *testing.T) {
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return newFakeWHEPSession("unexpected"), nil
	})
	req := newWHEPPostRequest("v=0\r\n")
	req.Header.Set("Origin", "://invalid")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("invalid-origin POST status = %d, want %d", res.Code, http.StatusForbidden)
	}
}

func TestWHEPPostCreatesAndDeletesSession(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	req := newWHEPPostRequest("v=0\r\n")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d: %s", res.Code, http.StatusCreated, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != "/whep/session-1" {
		t.Fatalf("Location = %q, want /whep/session-1", got)
	}
	if got := res.Header().Get("ETag"); len(got) != 34 || !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("ETag = %q, want a quoted 128-bit tag", got)
	}
	if got := res.Header().Get("Content-Type"); got != "application/sdp" {
		t.Fatalf("Content-Type = %q, want application/sdp", got)
	}
	if got := res.Body.String(); got != session.answer {
		t.Fatalf("answer = %q, want %q", got, session.answer)
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil)
	deleteRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d", deleteRes.Code, http.StatusOK)
	}
	select {
	case reason := <-session.closeReasons:
		if reason != "WHEP session deleted" {
			t.Fatalf("close reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("DELETE did not close the WHEP session")
	}
}

func TestWHEPDeleteClosesOutsideTheSignalingLock(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	resource := server.whep.get(session.id)
	if resource == nil {
		t.Fatal("WHEP resource was not registered")
	}
	closeObservedDeleting := make(chan bool, 1)
	session.onClose = func() {
		resource.patchMu.Lock()
		deleting := resource.deleting
		resource.patchMu.Unlock()
		closeObservedDeleting <- deleting
	}
	done := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil))
		done <- response.Code
	}()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("DELETE status = %d, want %d", status, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("WHEP DELETE held its signaling lock while closing the session")
	}
	if deleting := <-closeObservedDeleting; !deleting {
		t.Fatal("WHEP DELETE closed the session before marking its lifecycle state")
	}
}

func TestWHEPPostReportsSaturationAndRecoversAfterDelete(t *testing.T) {
	first := newFakeWHEPSession("session-1")
	replacement := newFakeWHEPSession("session-2")
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		switch opens.Add(1) {
		case 1:
			return first, nil
		case 2:
			select {
			case <-first.Done():
				t.Fatal("first session closed before the saturation attempt")
			default:
			}
			return nil, rtc.ErrSessionCapacity
		case 3:
			select {
			case <-first.Done():
				return replacement, nil
			default:
				t.Fatal("replacement opened before the first session closed")
				return nil, nil
			}
		default:
			t.Fatalf("unexpected session open attempt %d", opens.Load())
			return nil, nil
		}
	})
	created := httptest.NewRecorder()
	server.Handler().ServeHTTP(created, newWHEPPostRequest("v=0\r\n"))
	if created.Code != http.StatusCreated {
		t.Fatalf("initial POST status = %d, want %d", created.Code, http.StatusCreated)
	}
	saturated := httptest.NewRecorder()
	server.Handler().ServeHTTP(saturated, newWHEPPostRequest("v=0\r\n"))
	if saturated.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated POST status = %d, want %d", saturated.Code, http.StatusServiceUnavailable)
	}
	if got := saturated.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("saturated Retry-After = %q, want 1", got)
	}
	deleted := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d", deleted.Code, http.StatusOK)
	}
	retried := httptest.NewRecorder()
	server.Handler().ServeHTTP(retried, newWHEPPostRequest("v=0\r\n"))
	if retried.Code != http.StatusCreated {
		t.Fatalf("retry POST status = %d, want %d: %s", retried.Code, http.StatusCreated, retried.Body.String())
	}
	if got := retried.Header().Get("Location"); got != "/whep/session-2" {
		t.Fatalf("retry Location = %q, want /whep/session-2", got)
	}
}

func TestWHEPPostDoesNotMisclassifyInternalFailureAsSaturation(t *testing.T) {
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return nil, errors.New("peer factory failed")
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newWHEPPostRequest("v=0\r\n"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("internal failure status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get("Retry-After"); got != "" {
		t.Fatalf("internal failure Retry-After = %q, want empty", got)
	}
	if strings.Contains(response.Body.String(), "peer factory") {
		t.Fatalf("internal failure leaked its cause: %q", response.Body.String())
	}
}

func TestWHEPPostAdmissionBoundsConcurrentSetupAvalanches(t *testing.T) {
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		id := opens.Add(1)
		return newFakeWHEPSession(fmt.Sprintf("session-%d", id)), nil
	})
	now := time.Unix(100, 0)
	server.whep.now = func() time.Time { return now }
	const requests = 64
	statuses := make(chan int, requests)
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(requests)
	for index := 0; index < requests; index++ {
		go func() {
			defer workers.Done()
			<-start
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, newWHEPPostRequest("v=0\r\n"))
			statuses <- response.Code
		}()
	}
	close(start)
	workers.Wait()
	close(statuses)
	created := 0
	limited := 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected POST status %d", status)
		}
	}
	if created != whepPostBurst || limited != requests-whepPostBurst {
		t.Fatalf("created = %d limited = %d, want %d and %d", created, limited, whepPostBurst, requests-whepPostBurst)
	}
	if got := int(opens.Load()); got != whepPostBurst {
		t.Fatalf("session opens = %d, want %d", got, whepPostBurst)
	}
	snapshot := server.WHEPInitialRequests()
	if snapshot["created"] != whepPostBurst || snapshot["rate_limited"] != requests-whepPostBurst {
		t.Fatalf("initial-request metrics = %v", snapshot)
	}
}

func TestWHEPPostAdmissionRecoversAtTheConfiguredRate(t *testing.T) {
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		id := opens.Add(1)
		return newFakeWHEPSession(fmt.Sprintf("session-%d", id)), nil
	})
	now := time.Unix(100, 0)
	server.whep.now = func() time.Time { return now }
	for index := 0; index < whepPostBurst; index++ {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newWHEPPostRequest("v=0\r\n"))
		if response.Code != http.StatusCreated {
			t.Fatalf("burst POST %d status = %d, want %d", index, response.Code, http.StatusCreated)
		}
	}
	limited := httptest.NewRecorder()
	server.Handler().ServeHTTP(limited, newWHEPPostRequest("v=0\r\n"))
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "1" {
		t.Fatalf("limited response = status %d Retry-After %q", limited.Code, limited.Header().Get("Retry-After"))
	}
	now = now.Add(time.Second / whepPostsPerSecond)
	recovered := httptest.NewRecorder()
	server.Handler().ServeHTTP(recovered, newWHEPPostRequest("v=0\r\n"))
	if recovered.Code != http.StatusCreated {
		t.Fatalf("recovered POST status = %d, want %d", recovered.Code, http.StatusCreated)
	}
	snapshot := server.WHEPInitialRequests()
	if snapshot["created"] != whepPostBurst+1 || snapshot["rate_limited"] != 1 {
		t.Fatalf("initial-request metrics = %v", snapshot)
	}
}

func TestInvalidWHEPPostsDoNotConsumeSessionAdmission(t *testing.T) {
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		id := opens.Add(1)
		return newFakeWHEPSession(fmt.Sprintf("session-%d", id)), nil
	})
	now := time.Unix(100, 0)
	server.whep.now = func() time.Time { return now }
	for index := 0; index < whepPostBurst*2; index++ {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newWHEPPostRequest(""))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid POST %d status = %d, want %d", index, response.Code, http.StatusBadRequest)
		}
	}
	for index := 0; index < whepPostBurst; index++ {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newWHEPPostRequest("v=0\r\n"))
		if response.Code != http.StatusCreated {
			t.Fatalf("valid POST %d status = %d, want %d", index, response.Code, http.StatusCreated)
		}
	}
	snapshot := server.WHEPInitialRequests()
	if snapshot["invalid"] != whepPostBurst*2 || snapshot["created"] != whepPostBurst {
		t.Fatalf("initial-request metrics = %v", snapshot)
	}
}

func TestWHEPPatchAppliesTrickleAndRestartWithStrongETags(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	var refreshes atomic.Uint32
	session.refreshICE = func(context.Context) error {
		refreshes.Add(1)
		return nil
	}
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	if post.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d", post.Code, http.StatusCreated)
	}
	initialETag := post.Header().Get("ETag")
	trickle := newWHEPPatchRequest("a=ice-ufrag:old\r\na=ice-pwd:password\r\n", initialETag)
	trickleRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(trickleRes, trickle)
	if trickleRes.Code != http.StatusNoContent {
		t.Fatalf("trickle PATCH status = %d, want %d: %s", trickleRes.Code, http.StatusNoContent, trickleRes.Body.String())
	}
	if call := <-session.iceCalls; call.restart || call.fragment != "a=ice-ufrag:old\r\na=ice-pwd:password\r\n" {
		t.Fatalf("trickle call = %+v", call)
	}
	restart := newWHEPPatchRequest("a=ice-ufrag:new\r\na=ice-pwd:password2\r\n", "*")
	restartRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(restartRes, restart)
	if restartRes.Code != http.StatusOK {
		t.Fatalf("restart PATCH status = %d, want %d: %s", restartRes.Code, http.StatusOK, restartRes.Body.String())
	}
	if got := restartRes.Header().Get("Content-Type"); got != "application/trickle-ice-sdpfrag" {
		t.Fatalf("restart Content-Type = %q", got)
	}
	if got := restartRes.Header().Get("ETag"); got == "" || got == initialETag {
		t.Fatalf("restart ETag = %q, initial = %q", got, initialETag)
	}
	if got := restartRes.Body.String(); got != session.iceAnswer {
		t.Fatalf("restart answer = %q, want %q", got, session.iceAnswer)
	}
	if call := <-session.iceCalls; !call.restart {
		t.Fatalf("restart call = %+v", call)
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("ICE credential refreshes = %d, want 1", got)
	}
	stale := newWHEPPatchRequest("a=ice-ufrag:new\r\na=ice-pwd:password2\r\n", initialETag)
	staleRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(staleRes, stale)
	if staleRes.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale PATCH status = %d, want %d", staleRes.Code, http.StatusPreconditionFailed)
	}
}

func TestWHEPStalledCredentialRefreshDoesNotBlockDeleteOrDuplicateWork(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var refreshOnce sync.Once
	var refreshes atomic.Uint32
	session.refreshICE = func(context.Context) error {
		refreshes.Add(1)
		refreshOnce.Do(func() { close(refreshStarted) })
		<-releaseRefresh
		return nil
	}
	server := newTestWHEPServer(func(context.Context) (Session, error) { return session, nil })
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	if post.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d", post.Code, http.StatusCreated)
	}
	firstDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newWHEPPatchRequest("a=ice-ufrag:new\r\na=ice-pwd:password2\r\n", "*"))
		firstDone <- response.Code
	}()
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("ICE credential refresh did not start")
	}
	duplicate := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicate, newWHEPPatchRequest("a=ice-ufrag:duplicate\r\na=ice-pwd:password3\r\n", "*"))
	if duplicate.Code != http.StatusConflict || duplicate.Header().Get("Retry-After") != "1" {
		t.Fatalf("duplicate restart = status %d Retry-After %q", duplicate.Code, duplicate.Header().Get("Retry-After"))
	}
	deleteDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil))
		deleteDone <- response.Code
	}()
	select {
	case status := <-deleteDone:
		if status != http.StatusOK {
			t.Fatalf("DELETE status = %d, want %d", status, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("DELETE blocked behind external ICE credential I/O")
	}
	close(releaseRefresh)
	select {
	case status := <-firstDone:
		if status != http.StatusNotFound {
			t.Fatalf("orphaned restart status = %d, want %d", status, http.StatusNotFound)
		}
	case <-time.After(time.Second):
		t.Fatal("orphaned restart did not stop")
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("ICE credential refreshes = %d, want 1", got)
	}
	if got := session.closeCount.Load(); got != 1 {
		t.Fatalf("session closes = %d, want 1", got)
	}
}

func TestWHEPPatchRejectsInvalidPreconditionsAndBodies(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	etag := post.Header().Get("ETag")
	tests := []struct {
		name        string
		contentType string
		ifMatch     string
		body        []byte
		wantStatus  int
	}{
		{name: "completed offer answer", contentType: "application/sdp", ifMatch: etag, body: []byte("a=x\r\n"), wantStatus: http.StatusUnprocessableEntity},
		{name: "unsupported content type", contentType: "application/json", ifMatch: etag, body: []byte("{}"), wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing precondition", contentType: "application/trickle-ice-sdpfrag", body: []byte("a=x\r\n"), wantStatus: http.StatusPreconditionRequired},
		{name: "weak precondition", contentType: "application/trickle-ice-sdpfrag", ifMatch: "W/" + etag, body: []byte("a=x\r\n"), wantStatus: http.StatusPreconditionFailed},
		{name: "empty fragment", contentType: "application/trickle-ice-sdpfrag", ifMatch: etag, wantStatus: http.StatusBadRequest},
		{name: "oversized fragment", contentType: "application/trickle-ice-sdpfrag", ifMatch: etag, body: bytes.Repeat([]byte("a"), maxWHEPBodyBytes+1), wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/whep/session-1", bytes.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			if test.ifMatch != "" {
				req.Header.Set("If-Match", test.ifMatch)
			}
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, test.wantStatus)
			}
		})
	}
	select {
	case call := <-session.iceCalls:
		t.Fatalf("invalid PATCH reached session: %+v", call)
	default:
	}
}

func TestWHEPPostRejectsInvalidRequestsBeforeOpeningSession(t *testing.T) {
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		opens.Add(1)
		return newFakeWHEPSession("unexpected"), nil
	})
	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  int
	}{
		{name: "content type", contentType: "application/json", body: []byte("{}"), wantStatus: http.StatusUnsupportedMediaType},
		{name: "empty offer", contentType: "application/sdp", wantStatus: http.StatusBadRequest},
		{name: "oversized offer", contentType: "application/sdp", body: bytes.Repeat([]byte("a"), maxWHEPBodyBytes+1), wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/whep", bytes.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, test.wantStatus)
			}
		})
	}
	if got := opens.Load(); got != 0 {
		t.Fatalf("sessions opened for invalid requests = %d, want 0", got)
	}
}

func TestWHEPPostClosesFailedNegotiation(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	session.answerErr = errors.New("invalid SDP")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, newWHEPPostRequest("v=0\r\n"))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	select {
	case reason := <-session.closeReasons:
		if reason != "WHEP offer failed" {
			t.Fatalf("close reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("failed negotiation did not close the WHEP session")
	}
}

func TestWHEPPostPropagatesCancellationAndClosesSession(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	started := make(chan struct{})
	session.handleOffer = func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := newWHEPPostRequest("v=0\r\n").WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("WHEP negotiation did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled WHEP negotiation did not stop")
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("cancelled POST status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	select {
	case reason := <-session.closeReasons:
		if reason != "WHEP offer failed" {
			t.Fatalf("close reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled negotiation did not close the WHEP session")
	}
}

func TestWHEPSessionIsRemovedWhenPeerCloses(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, newWHEPPostRequest("v=0\r\n"))
	if res.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d", res.Code, http.StatusCreated)
	}
	session.Close("peer closed")
	deadline := time.Now().Add(time.Second)
	for {
		deleteReq := httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil)
		deleteRes := httptest.NewRecorder()
		server.Handler().ServeHTTP(deleteRes, deleteReq)
		if deleteRes.Code == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed session remained registered, DELETE status = %d", deleteRes.Code)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWHEPDiagnosticsRequireTheSessionToken(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	session.stats = rtc.SessionStats{Codec: "video/H264", TWCCEnabled: true, TWCCNegotiated: true}
	other := newFakeWHEPSession("session-2")
	var opens atomic.Uint32
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		if opens.Add(1) == 1 {
			return session, nil
		}
		return other, nil
	})
	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, newWHEPPostRequest("v=0\r\n"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d, want %d", first.Code, http.StatusCreated)
	}
	firstToken := first.Header().Get(whepDiagnosticsHeader)
	if len(firstToken) != 64 {
		t.Fatalf("diagnostics token length = %d, want 64", len(firstToken))
	}
	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, newWHEPPostRequest("v=0\r\n"))
	secondToken := second.Header().Get(whepDiagnosticsHeader)
	for name, token := range map[string]string{
		"missing":        "",
		"malformed auth": "Basic " + firstToken,
		"wrong session":  secondToken,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/diagnostics/sessions/session-1", nil)
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/api/diagnostics/sessions/session-1", nil)
	request.Header.Set("Authorization", "Bearer "+firstToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var stats rtc.SessionStats
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if stats.Codec != session.stats.Codec || !stats.TWCCNegotiated {
		t.Fatalf("diagnostics = %+v, want %+v", stats, session.stats)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/whep/session-1", nil)
	server.Handler().ServeHTTP(httptest.NewRecorder(), deleteRequest)
	closed := httptest.NewRecorder()
	server.Handler().ServeHTTP(closed, request)
	if closed.Code != http.StatusNotFound {
		t.Fatalf("closed-session status = %d, want %d", closed.Code, http.StatusNotFound)
	}
}

func TestWHEPPatchRateLimitIsBoundedPerSession(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	etag := post.Header().Get("ETag")
	for index := 0; index < maxWHEPPatchesPerSecond; index++ {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newWHEPPatchRequest("a=end-of-candidates\r\n", etag))
		if response.Code != http.StatusNoContent {
			t.Fatalf("PATCH %d status = %d, want %d", index+1, response.Code, http.StatusNoContent)
		}
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newWHEPPatchRequest("a=end-of-candidates\r\n", etag))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited PATCH status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestWHEPFailedRestartKeepsTheSessionAvailable(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	session.iceErr = errors.New("temporary restart failure")
	failed := httptest.NewRecorder()
	server.Handler().ServeHTTP(failed, newWHEPPatchRequest("a=ice-ufrag:new\r\na=ice-pwd:new-password\r\n", "*"))
	if failed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failed restart status = %d, want %d", failed.Code, http.StatusUnprocessableEntity)
	}
	if got := session.closeCount.Load(); got != 0 {
		t.Fatalf("failed restart closed the session %d times", got)
	}
	session.iceErr = nil
	retry := httptest.NewRecorder()
	server.Handler().ServeHTTP(retry, newWHEPPatchRequest("a=ice-ufrag:newer\r\na=ice-pwd:newer-password\r\n", "*"))
	if retry.Code != http.StatusOK {
		t.Fatalf("restart retry status = %d, want %d", retry.Code, http.StatusOK)
	}
}

func TestWHEPPatchHasABoundedNegotiationDeadline(t *testing.T) {
	session := newFakeWHEPSession("session-1")
	server := newTestWHEPServer(func(context.Context) (Session, error) {
		return session, nil
	})
	server.whep.operationTimeout = 20 * time.Millisecond
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, newWHEPPostRequest("v=0\r\n"))
	session.handleICE = func(ctx context.Context, _ string, _ bool) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newWHEPPatchRequest("a=end-of-candidates\r\n", post.Header().Get("ETag")))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("timed-out PATCH status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
	session.handleICE = nil
	retry := httptest.NewRecorder()
	server.Handler().ServeHTTP(retry, newWHEPPatchRequest("a=end-of-candidates\r\n", post.Header().Get("ETag")))
	if retry.Code != http.StatusNoContent {
		t.Fatalf("PATCH retry status = %d, want %d", retry.Code, http.StatusNoContent)
	}
}

func newTestWHEPServer(open func(context.Context) (Session, error)) *Server {
	hub := logs.NewHub(16)
	return NewServer(logs.NewLogger(hub, false), nil, open, ServerOptions{Viewer: false})
}

func newWHEPPostRequest(offer string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/whep", strings.NewReader(offer))
	req.Header.Set("Content-Type", "application/sdp")
	return req
}

func newWHEPPatchRequest(fragment string, ifMatch string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/whep/session-1", strings.NewReader(fragment))
	req.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
	req.Header.Set("If-Match", ifMatch)
	return req
}
