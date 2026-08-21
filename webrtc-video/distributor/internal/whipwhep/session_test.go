package whipwhep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestExchangeCreatesAndDeletesSession(t *testing.T) {
	const authorization = "Bearer source-secret"
	serverPeer := newSendingPeer(t)
	var posts atomic.Uint32
	var patches atomic.Uint32
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != authorization {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.Method {
		case http.MethodPost:
			posts.Add(1)
			offer, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read offer: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := serverPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
				t.Errorf("set remote offer: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if !bytes.Contains(offer, []byte("a=rtcp-mux-only")) {
				t.Error("offer does not require RTP/RTCP multiplexing")
			}
			answer, err := serverPeer.CreateAnswer(nil)
			if err != nil {
				t.Errorf("create answer: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			gathered := webrtc.GatheringCompletePromise(serverPeer)
			if err := serverPeer.SetLocalDescription(answer); err != nil {
				t.Errorf("set local answer: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			select {
			case <-gathered:
			case <-time.After(5 * time.Second):
				t.Error("server ICE gathering timed out")
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("ETag", `"generation-1"`)
			w.Header().Set("Location", "/sessions/opaque")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(serverPeer.LocalDescription().SDP))
		case http.MethodPatch:
			if request.Header.Get("If-Match") != `"generation-1"` {
				t.Errorf("If-Match = %q", request.Header.Get("If-Match"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read ICE fragment: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if !bytes.Contains(body, []byte("a=candidate:")) {
				t.Errorf("ICE fragment has no candidate: %q", body)
			}
			patches.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	clientPeer := newReceivingPeer(t)
	session, err := Exchange(context.Background(), clientPeer, mustURL(t, server.URL+"/whep"), authorization, server.Client())
	if err != nil {
		t.Fatalf("exchange SDP: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for patches.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if patches.Load() != 1 {
		t.Fatalf("candidate PATCH requests = %d, want 1", patches.Load())
	}
	var workers sync.WaitGroup
	workers.Add(8)
	for index := 0; index < 8; index++ {
		go func() {
			defer workers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := session.Close(ctx); err != nil {
				t.Errorf("close session: %v", err)
			}
		}()
	}
	workers.Wait()
	if posts.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("requests = POST %d DELETE %d, want 1 each", posts.Load(), deletes.Load())
	}
}

func TestCandidateWorkerSendsASeparateEndOfCandidatesFragment(t *testing.T) {
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read candidate completion: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	peer := newReceivingPeer(t)
	defer func() { _ = peer.Close() }()
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create local offer: %v", err)
	}
	if err := peer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	close(ready)
	session := &Session{
		peer:       peer,
		endpoint:   mustURL(t, server.URL+"/session"),
		client:     server.Client(),
		etag:       `"generation-1"`,
		ctx:        ctx,
		cancel:     cancel,
		ready:      ready,
		candidates: make(chan candidateEvent, 1),
		restarts:   make(chan restartRequest, 1),
		workerDone: make(chan struct{}),
	}
	go session.runCandidateWorker()
	session.candidates <- candidateEvent{complete: true}
	select {
	case fragment := <-requests:
		if !strings.Contains(fragment, "a=end-of-candidates\r\n") || strings.Contains(fragment, "a=candidate:") {
			t.Fatalf("candidate completion fragment = %q", fragment)
		}
	case <-time.After(time.Second):
		t.Fatal("candidate worker did not signal the end of candidate gathering")
	}
	cancel()
	select {
	case <-session.workerDone:
	case <-time.After(time.Second):
		t.Fatal("candidate worker did not stop")
	}
}

func TestRestartRenewsCredentialsAndKeepsOneConnectedPeer(t *testing.T) {
	const initialAuthorization = "Bearer source-initial"
	const refreshedAuthorization = "Bearer source-refreshed"
	serverPeer := newSendingPeer(t)
	serverConnected := make(chan struct{})
	var serverConnectedOnce sync.Once
	serverPeer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			serverConnectedOnce.Do(func() { close(serverConnected) })
		}
	})
	var candidatePatches atomic.Uint32
	var restartAttempts atomic.Uint32
	var restarts atomic.Uint32
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			if request.Header.Get("Authorization") != initialAuthorization || request.URL.Query().Get("rstream.token") != "edge-initial" {
				t.Errorf("initial credentials = authorization %q edge %q", request.Header.Get("Authorization"), request.URL.Query().Get("rstream.token"))
			}
			offer, err := io.ReadAll(request.Body)
			if err != nil || serverPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}) != nil {
				http.Error(w, "invalid offer", http.StatusBadRequest)
				return
			}
			answer, err := serverPeer.CreateAnswer(nil)
			if err != nil {
				http.Error(w, "answer failed", http.StatusInternalServerError)
				return
			}
			gathered := webrtc.GatheringCompletePromise(serverPeer)
			if err := serverPeer.SetLocalDescription(answer); err != nil {
				http.Error(w, "answer failed", http.StatusInternalServerError)
				return
			}
			select {
			case <-gathered:
			case <-time.After(5 * time.Second):
				http.Error(w, "gathering timed out", http.StatusGatewayTimeout)
				return
			}
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("ETag", `"generation-1"`)
			w.Header().Set("Location", "/sessions/restartable")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, serverPeer.LocalDescription().SDP)
		case http.MethodPatch:
			fragment, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(w, "fragment failed", http.StatusBadRequest)
				return
			}
			remote, err := parseICEFragment(string(fragment))
			if err != nil {
				http.Error(w, "fragment failed", http.StatusBadRequest)
				return
			}
			if request.Header.Get("If-Match") != "*" {
				for _, candidate := range remote.candidates {
					if err := serverPeer.AddICECandidate(candidate); err != nil {
						t.Errorf("apply initial candidate: %v", err)
					}
				}
				candidatePatches.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if request.Header.Get("Authorization") != refreshedAuthorization || request.URL.Query().Get("rstream.token") != "edge-refreshed" {
				t.Errorf("restart credentials = authorization %q edge %q", request.Header.Get("Authorization"), request.URL.Query().Get("rstream.token"))
			}
			if restartAttempts.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "temporary restart failure", http.StatusServiceUnavailable)
				return
			}
			if restartAttempts.Load() == 2 {
				<-request.Context().Done()
				return
			}
			current := serverPeer.RemoteDescription()
			if current == nil {
				http.Error(w, "remote SDP unavailable", http.StatusConflict)
				return
			}
			restarted, err := replaceICECredentials(current.SDP, remote.ufrag, remote.pwd)
			if err != nil || serverPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: restarted}) != nil {
				http.Error(w, "restart offer failed", http.StatusUnprocessableEntity)
				return
			}
			for _, candidate := range remote.candidates {
				if err := serverPeer.AddICECandidate(candidate); err != nil {
					http.Error(w, "restart candidate failed", http.StatusUnprocessableEntity)
					return
				}
			}
			answer, err := serverPeer.CreateAnswer(nil)
			if err != nil {
				http.Error(w, "restart answer failed", http.StatusInternalServerError)
				return
			}
			gathered := webrtc.GatheringCompletePromise(serverPeer)
			if err := serverPeer.SetLocalDescription(answer); err != nil {
				http.Error(w, "restart answer failed", http.StatusInternalServerError)
				return
			}
			select {
			case <-gathered:
			case <-time.After(5 * time.Second):
				http.Error(w, "restart gathering timed out", http.StatusGatewayTimeout)
				return
			}
			response, err := completeICEFragment(serverPeer.LocalDescription().SDP)
			if err != nil {
				http.Error(w, "restart response failed", http.StatusInternalServerError)
				return
			}
			restarts.Add(1)
			w.Header().Set("Content-Type", "application/trickle-ice-sdpfrag")
			w.Header().Set("ETag", `"generation-2"`)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, response)
		case http.MethodDelete:
			if request.Header.Get("Authorization") != refreshedAuthorization || request.URL.Query().Get("rstream.token") != "edge-refreshed" {
				t.Errorf("delete credentials = authorization %q edge %q", request.Header.Get("Authorization"), request.URL.Query().Get("rstream.token"))
			}
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	clientPeer := newReceivingPeer(t)
	initial := mustURL(t, server.URL+"/whep?rstream.token=edge-initial")
	session, err := Exchange(context.Background(), clientPeer, initial, initialAuthorization, server.Client())
	if err != nil {
		t.Fatalf("exchange SDP: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for (clientPeer.ConnectionState() != webrtc.PeerConnectionStateConnected || candidatePatches.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if clientPeer.ConnectionState() != webrtc.PeerConnectionStateConnected || candidatePatches.Load() == 0 {
		t.Fatalf("initial connection = state %s candidate PATCHes %d", clientPeer.ConnectionState(), candidatePatches.Load())
	}
	select {
	case <-serverConnected:
	case <-time.After(time.Second):
		t.Fatal("server peer did not connect")
	}
	refreshed := mustURL(t, server.URL+"/whep?rstream.token=edge-refreshed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := session.Restart(ctx, refreshed, refreshedAuthorization, nil); err == nil || IsPermanent(err) {
		cancel()
		t.Fatalf("temporary restart error = %v, want retryable failure", err)
	}
	if clientPeer.SignalingState() != webrtc.SignalingStateStable || session.workerError() != nil {
		cancel()
		t.Fatalf("failed restart state = signaling %s worker error %v", clientPeer.SignalingState(), session.workerError())
	}
	cancelledCtx, cancelRestart := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := session.Restart(cancelledCtx, refreshed, refreshedAuthorization, nil); !errors.Is(err, context.DeadlineExceeded) {
		cancelRestart()
		cancel()
		t.Fatalf("cancelled restart error = %v, want deadline", err)
	}
	cancelRestart()
	if clientPeer.SignalingState() != webrtc.SignalingStateStable || session.workerError() != nil {
		cancel()
		t.Fatalf("cancelled restart state = signaling %s worker error %v", clientPeer.SignalingState(), session.workerError())
	}
	if err := session.Restart(ctx, refreshed, refreshedAuthorization, nil); err != nil {
		cancel()
		t.Fatalf("restart WHEP ICE: %v", err)
	}
	cancel()
	if restartAttempts.Load() != 3 || restarts.Load() != 1 || session.etag != `"generation-2"` {
		t.Fatalf("restart state = attempts %d successes %d ETag %q", restartAttempts.Load(), restarts.Load(), session.etag)
	}
	deadline = time.Now().Add(5 * time.Second)
	for clientPeer.ConnectionState() != webrtc.PeerConnectionStateConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if clientPeer.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Fatalf("connection state after restart = %s", clientPeer.ConnectionState())
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(closeCtx); err != nil {
		t.Fatalf("close restarted session: %v", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE requests = %d, want 1", deletes.Load())
	}
}

func TestExchangeIgnoresBodyCloseFailureAfterCompleteCounterOffer(t *testing.T) {
	serverPeer := newSendingPeer(t)
	offer, err := serverPeer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create counter-offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(serverPeer)
	if err := serverPeer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set counter-offer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("counter-offer ICE gathering timed out")
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodPost:
			return &http.Response{
				Body: closeErrorReadCloser{Reader: strings.NewReader(serverPeer.LocalDescription().SDP)},
				Header: http.Header{
					"Content-Type": {`application/sdp; valid-until="` + time.Now().Add(time.Minute).UTC().Format(http.TimeFormat) + `"`},
					"Location":     {"/sessions/close-error"},
				},
				StatusCode: http.StatusNotAcceptable,
			}, nil
		case http.MethodPatch, http.MethodDelete:
			return &http.Response{Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), StatusCode: http.StatusNoContent}, nil
		default:
			return nil, fmt.Errorf("unexpected method %s", request.Method)
		}
	})}
	peer := newReceivingPeer(t)
	session, err := Exchange(context.Background(), peer, mustURL(t, "https://source.example/whep"), "", client)
	if err != nil {
		t.Fatalf("exchange counter-offer with close failure: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestPostOfferClassifiesPermanentAndRetryableFailures(t *testing.T) {
	tests := []struct {
		status    int
		permanent bool
	}{
		{status: http.StatusBadRequest, permanent: true},
		{status: http.StatusUnauthorized, permanent: true},
		{status: http.StatusForbidden, permanent: true},
		{status: http.StatusNotFound, permanent: true},
		{status: http.StatusUnsupportedMediaType, permanent: true},
		{status: http.StatusUnprocessableEntity, permanent: true},
		{status: http.StatusRequestTimeout},
		{status: http.StatusConflict},
		{status: http.StatusTooEarly},
		{status: http.StatusTooManyRequests},
		{status: http.StatusInternalServerError},
		{status: http.StatusServiceUnavailable},
	}
	const edgeCredential = "edge-credential-that-must-not-leak"
	endpoint := mustURL(t, "https://source.example/whep?rstream.token="+edgeCredential)
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{Body: io.NopCloser(strings.NewReader("private response")), Header: http.Header{"Retry-After": {"3"}}, StatusCode: test.status}, nil
			})}
			_, err := postOffer(context.Background(), endpoint, "offer", "", client, map[string]struct{}{urlOrigin(endpoint): {}})
			if err == nil {
				t.Fatal("post offer accepted an error status")
			}
			if IsPermanent(err) != test.permanent {
				t.Fatalf("permanent = %t, want %t for status %d", IsPermanent(err), test.permanent, test.status)
			}
			if strings.Contains(err.Error(), "private response") {
				t.Fatalf("signaling error exposed response body: %v", err)
			}
			var statusErr *HTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != test.status {
				t.Fatalf("HTTP status error = %#v, want status %d", statusErr, test.status)
			}
			if statusErr.RetryAfter() != 3*time.Second {
				t.Fatalf("retry after = %s, want 3s", statusErr.RetryAfter())
			}
		})
	}
	transportFailure := errors.New("network unavailable")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportFailure
	})}
	_, err := postOffer(context.Background(), endpoint, "offer", "", client, map[string]struct{}{urlOrigin(endpoint): {}})
	if !errors.Is(err, transportFailure) || IsPermanent(err) {
		t.Fatalf("transport error = %v, want retryable cause", err)
	}
	if strings.Contains(err.Error(), edgeCredential) {
		t.Fatalf("transport error exposed the edge credential: %v", err)
	}
}

func TestParseRetryAfterAcceptsDeltaAndDateAndBoundsOverflow(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "empty", raw: "", want: 0},
		{name: "delta", raw: "7", want: 7 * time.Second},
		{name: "future date", raw: now.Add(11 * time.Second).Format(http.TimeFormat), want: 11 * time.Second},
		{name: "past date", raw: now.Add(-time.Second).Format(http.TimeFormat), want: 0},
		{name: "malformed", raw: "soon", want: 0},
		{name: "overflow", raw: "18446744073709551615", want: time.Duration(1<<63 - 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseRetryAfter(test.raw, now); got != test.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", test.raw, got, test.want)
			}
		})
	}
}

func TestExchangeCompletesServerCounterOffer(t *testing.T) {
	serverPeer := newSendingPeer(t)
	offer, err := serverPeer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create counter-offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(serverPeer)
	if err := serverPeer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set counter-offer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("counter-offer ICE gathering timed out")
	}
	var answers atomic.Uint32
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", `application/sdp; valid-until="`+time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)+`"`)
			w.Header().Set("Location", "/sessions/counter-offer")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = io.WriteString(w, serverPeer.LocalDescription().SDP)
		case http.MethodPatch:
			if request.Header.Get("Content-Type") != "application/sdp" {
				t.Errorf("counter-offer PATCH Content-Type = %q", request.Header.Get("Content-Type"))
			}
			answer, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Errorf("read counter-offer answer: %v", readErr)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if !bytes.Contains(answer, []byte("a=rtcp-mux-only")) {
				t.Error("counter-offer answer does not require RTP/RTCP multiplexing")
			}
			if setErr := serverPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(answer)}); setErr != nil {
				t.Errorf("set counter-offer answer: %v", setErr)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			answers.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	clientPeer := newReceivingPeer(t)
	session, err := Exchange(context.Background(), clientPeer, mustURL(t, server.URL+"/whep"), "Bearer source-secret", server.Client())
	if err != nil {
		t.Fatalf("exchange counter-offer: %v", err)
	}
	if answers.Load() != 1 {
		t.Fatalf("counter-offer answers = %d, want 1", answers.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close counter-offer session: %v", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("counter-offer DELETE requests = %d, want 1", deletes.Load())
	}
}

func TestExchangeDeletesCreatedSessionWhenAnswerIsInvalid(t *testing.T) {
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("ETag", `"generation-1"`)
		w.Header().Set("Location", "/sessions/failed")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-an-sdp-answer"))
	}))
	defer server.Close()
	peer := newReceivingPeer(t)
	defer func() { _ = peer.Close() }()
	if _, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "", server.Client()); err == nil {
		t.Fatal("exchange accepted an invalid SDP answer")
	}
	if deletes.Load() != 1 {
		t.Fatalf("cleanup DELETE requests = %d, want 1", deletes.Load())
	}
}

func TestExchangeRejectsCrossOriginRedirectWithoutForwardingAuthorization(t *testing.T) {
	var redirected atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirected.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, request, "https://other.example/redirected", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	peer := newReceivingPeer(t)
	defer func() { _ = peer.Close() }()
	if _, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "Bearer secret", server.Client()); err == nil {
		t.Fatal("exchange accepted a redirect")
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target requests = %d, want 0", redirected.Load())
	}
}

func TestTrustedRedirectUsesDestinationEdgeCredential(t *testing.T) {
	current := mustURL(t, "https://edge.example/whep?rstream.token=edge-source")
	trusted, err := redirectOriginSet(current, []string{"https://region.example"})
	if err != nil {
		t.Fatalf("trusted redirect origins: %v", err)
	}
	sameOrigin, err := safeRedirect(current, "/balanced/whep", trusted)
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if token := sameOrigin.Query().Get("rstream.token"); token != "edge-source" {
		t.Fatalf("same-origin edge credential = %q, want edge-source", token)
	}
	crossOrigin, err := safeRedirect(current, "https://region.example/whep?rstream.token=edge-destination", trusted)
	if err != nil {
		t.Fatalf("trusted cross-origin redirect: %v", err)
	}
	if token := crossOrigin.Query().Get("rstream.token"); token != "edge-destination" {
		t.Fatalf("cross-origin edge credential = %q, want edge-destination", token)
	}
	if _, err := safeRedirect(current, "https://untrusted.example/whep?rstream.token=leaked", trusted); err == nil {
		t.Fatal("untrusted redirect was accepted")
	}
	if _, err := redirectOriginSet(current, []string{"https://region.example/whep"}); err == nil {
		t.Fatal("trusted redirect origin with a path was accepted")
	}
}

func TestExchangeRejectsOversizedAnswerAndDeletesSession(t *testing.T) {
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("ETag", `"generation-1"`)
		w.Header().Set("Location", "/sessions/oversized")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxSDPBytes+1))
	}))
	defer server.Close()
	peer := newReceivingPeer(t)
	defer func() { _ = peer.Close() }()
	if _, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "", server.Client()); err == nil {
		t.Fatal("exchange accepted an oversized SDP answer")
	}
	if deletes.Load() != 1 {
		t.Fatalf("cleanup DELETE requests = %d, want 1", deletes.Load())
	}
}

func TestExchangeRejectsInvalidETagAndOmitsEmptyAuthorization(t *testing.T) {
	for _, etag := range []string{"", `W/"generation-1"`, "*"} {
		t.Run(etag, func(t *testing.T) {
			var requests atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if _, ok := request.Header["Authorization"]; ok {
					t.Error("empty authorization was sent")
				}
				if request.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/sdp")
				w.Header().Set("ETag", etag)
				w.Header().Set("Location", "/sessions/invalid-etag")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, "invalid")
			}))
			defer server.Close()
			peer := newReceivingPeer(t)
			defer func() { _ = peer.Close() }()
			_, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "  ", server.Client())
			if err == nil || !strings.Contains(err.Error(), "strong ETag") {
				t.Fatalf("exchange error = %v, want strong ETag rejection", err)
			}
			if requests.Load() != 2 {
				t.Fatalf("requests = %d, want POST and cleanup DELETE", requests.Load())
			}
		})
	}
}

func TestRequireETagRejectsInvalidOpaqueTags(t *testing.T) {
	for _, value := range []string{`"generation"extra"`, "\"generation\x7f\"", "\"generation\t\""} {
		if _, err := requireETag(value, false); err == nil {
			t.Fatalf("invalid ETag %q was accepted", value)
		}
	}
	if value, err := requireETag(`"generation-1"`, false); err != nil || value != `"generation-1"` {
		t.Fatalf("valid ETag result = %q, %v", value, err)
	}
}

func TestExchangeRejectsExpiredCounterOfferAndDeletesSession(t *testing.T) {
	var patches atomic.Uint32
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", `application/sdp; valid-until="`+time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)+`"`)
			w.Header().Set("Location", "/sessions/expired-counter-offer")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = io.WriteString(w, "unused")
		case http.MethodPatch:
			patches.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	peer := newReceivingPeer(t)
	defer func() { _ = peer.Close() }()
	_, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "", server.Client())
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("exchange error = %v, want expired counter-offer rejection", err)
	}
	if patches.Load() != 0 || deletes.Load() != 1 {
		t.Fatalf("requests = PATCH %d DELETE %d, want 0 and 1", patches.Load(), deletes.Load())
	}
}

func TestCounterOfferDeadlineIsRecheckedAfterProcessing(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	deadline, err := counterOfferDeadline("application/sdp", now)
	if err != nil {
		t.Fatalf("default counter-offer deadline: %v", err)
	}
	if want := now.Add(30 * time.Second); !deadline.Equal(want) {
		t.Fatalf("default counter-offer deadline = %s, want %s", deadline, want)
	}
	if err := requireFreshCounterOffer(deadline, deadline); err == nil {
		t.Fatal("counter-offer remained valid at its deadline")
	}
	validUntil := now.Add(time.Minute)
	contentType := `application/sdp; valid-until="` + validUntil.Format(http.TimeFormat) + `"`
	deadline, err = counterOfferDeadline(contentType, now)
	if err != nil {
		t.Fatalf("explicit counter-offer deadline: %v", err)
	}
	if !deadline.Equal(validUntil) {
		t.Fatalf("explicit counter-offer deadline = %s, want %s", deadline, validUntil)
	}
}

func TestCloseCancelsStalledCandidatePatch(t *testing.T) {
	serverPeer := newSendingPeer(t)
	patchStarted := make(chan struct{})
	var deletes atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			offer, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read offer: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := serverPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
				t.Errorf("set remote offer: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			answer, err := serverPeer.CreateAnswer(nil)
			if err != nil {
				t.Errorf("create answer: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := serverPeer.SetLocalDescription(answer); err != nil {
				t.Errorf("set local answer: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("ETag", `"generation-1"`)
			w.Header().Set("Location", "/sessions/stalled-patch")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, serverPeer.LocalDescription().SDP)
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	baseTransport := server.Client().Transport
	var patchOnce sync.Once
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch {
			return baseTransport.RoundTrip(request)
		}
		patchOnce.Do(func() { close(patchStarted) })
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	peer := newReceivingPeer(t)
	session, err := Exchange(context.Background(), peer, mustURL(t, server.URL+"/whep"), "", client)
	if err != nil {
		t.Fatalf("exchange SDP: %v", err)
	}
	select {
	case <-patchStarted:
	case <-time.After(time.Second):
		t.Fatal("candidate PATCH did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close stalled session: %v", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE requests = %d, want 1", deletes.Load())
	}
}

func TestCloseDeletesRemoteResourceBeforeClosingPeer(t *testing.T) {
	peer := newReceivingPeer(t)
	workerDone := make(chan struct{})
	close(workerDone)
	sessionCtx, cancel := context.WithCancel(context.Background())
	var peerWasClosed atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodDelete {
			t.Fatalf("request method = %s, want DELETE", request.Method)
		}
		peerWasClosed.Store(peer.ConnectionState() == webrtc.PeerConnectionStateClosed)
		return &http.Response{
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			StatusCode: http.StatusNoContent,
		}, nil
	})}
	session := &Session{
		peer:       peer,
		endpoint:   mustURL(t, "https://source.example/sessions/opaque"),
		client:     client,
		ctx:        sessionCtx,
		cancel:     cancel,
		workerDone: workerDone,
	}
	ctx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if peerWasClosed.Load() {
		t.Fatal("peer was closed before the remote resource DELETE")
	}
	if state := peer.ConnectionState(); state != webrtc.PeerConnectionStateClosed {
		t.Fatalf("peer state = %s, want closed", state)
	}
}

func TestSetAuthorizationUpdatesRemoteDeletionAndRejectsUnsafeValues(t *testing.T) {
	peer := newReceivingPeer(t)
	workerDone := make(chan struct{})
	close(workerDone)
	sessionCtx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer refreshed" {
			t.Fatalf("DELETE authorization = %q", authorization)
		}
		return &http.Response{
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			StatusCode: http.StatusNoContent,
		}, nil
	})}
	session := &Session{
		peer:          peer,
		endpoint:      mustURL(t, "https://source.example/sessions/opaque"),
		authorization: "Bearer initial",
		client:        client,
		ctx:           sessionCtx,
		cancel:        cancel,
		workerDone:    workerDone,
	}
	for _, authorization := range []string{
		"Bearer invalid\r\nX-Injected: true",
		strings.Repeat("a", maxAuthorizationBytes+1),
	} {
		if err := session.SetAuthorization(authorization); err == nil {
			t.Fatalf("accepted unsafe authorization of length %d", len(authorization))
		}
	}
	if err := session.SetAuthorization("  Bearer refreshed  "); err != nil {
		t.Fatalf("refresh authorization: %v", err)
	}
	ctx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestSetCredentialsRotatesOnlyTheEdgeAndApplicationCredentials(t *testing.T) {
	peer := newReceivingPeer(t)
	workerDone := make(chan struct{})
	close(workerDone)
	sessionCtx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://source.example/sessions/opaque?resource=one&rstream.token=edge-new" {
			t.Fatalf("DELETE URL = %q", request.URL)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer application-new" {
			t.Fatalf("DELETE authorization = %q", authorization)
		}
		return &http.Response{Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), StatusCode: http.StatusNoContent}, nil
	})}
	session := &Session{
		peer:          peer,
		target:        mustURL(t, "https://source.example/whep?profile=main&rstream.token=edge-old"),
		endpoint:      mustURL(t, "https://source.example/sessions/opaque?resource=one&rstream.token=edge-old"),
		authorization: "Bearer application-old",
		client:        client,
		ctx:           sessionCtx,
		cancel:        cancel,
		workerDone:    workerDone,
	}
	for _, endpoint := range []string{
		"https://other.example/whep?profile=main&rstream.token=edge-new",
		"https://source.example/other?profile=main&rstream.token=edge-new",
		"https://source.example/whep?profile=other&rstream.token=edge-new",
		"https://source.example/whep?profile=main&rstream.token=one&rstream.token=two",
		"https://user:secret@source.example/whep?profile=main&rstream.token=edge-new",
	} {
		if err := session.SetCredentials(mustURL(t, endpoint), "Bearer application-new"); err == nil {
			t.Fatalf("accepted credentials for changed endpoint %q", endpoint)
		}
	}
	if err := session.SetCredentials(mustURL(t, "https://source.example/whep?profile=main&rstream.token=edge-new"), "Bearer application-new"); err != nil {
		t.Fatalf("rotate session credentials: %v", err)
	}
	ctx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestSetCredentialsIsRaceSafeWithRequestSnapshots(t *testing.T) {
	session := &Session{
		target:        mustURL(t, "https://source.example/whep?rstream.token=edge-0"),
		endpoint:      mustURL(t, "https://source.example/sessions/opaque?rstream.token=edge-0"),
		authorization: "Bearer application-0",
	}
	var workers sync.WaitGroup
	workers.Add(8)
	for index := 0; index < 4; index++ {
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 1000; iteration++ {
				endpoint, authorization := session.requestCredentials()
				if endpoint == nil || authorization == "" {
					t.Error("credential snapshot is incomplete")
					return
				}
				edgeVersion := strings.TrimPrefix(endpoint.Query().Get("rstream.token"), "edge-")
				applicationVersion := strings.TrimPrefix(authorization, "Bearer application-")
				if edgeVersion != applicationVersion {
					t.Errorf("credential snapshot mixed edge %q with application %q", edgeVersion, applicationVersion)
					return
				}
			}
		}()
		go func(worker int) {
			defer workers.Done()
			for iteration := 0; iteration < 1000; iteration++ {
				token := fmt.Sprintf("edge-%d-%d", worker, iteration)
				authorization := fmt.Sprintf("Bearer application-%d-%d", worker, iteration)
				endpoint := &url.URL{Scheme: "https", Host: "source.example", Path: "/whep", RawQuery: "rstream.token=" + token}
				if err := session.SetCredentials(endpoint, authorization); err != nil {
					t.Errorf("rotate credentials: %v", err)
					return
				}
			}
		}(index)
	}
	workers.Wait()
}

func TestCloseClosesPeerWhenRemoteDeletionTimesOut(t *testing.T) {
	peer := newReceivingPeer(t)
	workerDone := make(chan struct{})
	close(workerDone)
	sessionCtx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	session := &Session{
		peer:       peer,
		endpoint:   mustURL(t, "https://source.example/sessions/stalled"),
		client:     client,
		ctx:        sessionCtx,
		cancel:     cancel,
		workerDone: workerDone,
	}
	ctx, closeCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer closeCancel()
	err := session.Close(ctx)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("close error = %v, want timeout", err)
	}
	if state := peer.ConnectionState(); state != webrtc.PeerConnectionStateClosed {
		t.Fatalf("peer state = %s, want closed", state)
	}
}

func TestConcurrentCloseWaitHonorsEachCallerContext(t *testing.T) {
	peer := newReceivingPeer(t)
	workerDone := make(chan struct{})
	close(workerDone)
	sessionCtx, cancel := context.WithCancel(context.Background())
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var deletes atomic.Uint32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deletes.Add(1)
		close(requestStarted)
		select {
		case <-releaseRequest:
			return &http.Response{Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), StatusCode: http.StatusNoContent}, nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})}
	session := &Session{
		peer:       peer,
		endpoint:   mustURL(t, "https://source.example/sessions/concurrent-close"),
		client:     client,
		ctx:        sessionCtx,
		cancel:     cancel,
		workerDone: workerDone,
	}
	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Second)
	defer firstCancel()
	first := make(chan error, 1)
	go func() { first <- session.Close(firstCtx) }()
	<-requestStarted
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer secondCancel()
	err := session.Close(secondCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Close() error = %v, want deadline exceeded", err)
	}
	if state := peer.ConnectionState(); state == webrtc.PeerConnectionStateClosed {
		t.Fatal("concurrent close caller interrupted the active cleanup")
	}
	close(releaseRequest)
	if err := <-first; err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("completed Close() error = %v", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE requests = %d, want 1", deletes.Load())
	}
	if state := peer.ConnectionState(); state != webrtc.PeerConnectionStateClosed {
		t.Fatalf("peer state = %s, want closed", state)
	}
}

func TestCloseRejectsMissingContext(t *testing.T) {
	session := &Session{}
	var ctx context.Context
	if err := session.Close(ctx); err == nil {
		t.Fatal("Close() accepted a nil context")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

type closeErrorReadCloser struct {
	io.Reader
}

func (closeErrorReadCloser) Close() error {
	return errors.New("synthetic close failure")
}

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestResolveSessionURLRejectsChangedOriginAndCredentials(t *testing.T) {
	endpoint := mustURL(t, "https://source.example/whep")
	for _, location := range []string{"https://other.example/session", "https://user:secret@source.example/session", "//other.example/session"} {
		if _, err := resolveSessionURL(endpoint, location); err == nil {
			t.Fatalf("accepted unsafe Location %q", location)
		}
	}
}

func TestResolveSessionURLCarriesTheEdgeCredentialOntoTheOpaqueResource(t *testing.T) {
	endpoint := mustURL(t, "https://source.example/whep?rstream.token=edge-current")
	location, err := resolveSessionURL(endpoint, "/sessions/opaque?resource=one&rstream.token=stale")
	if err != nil {
		t.Fatalf("resolve session URL: %v", err)
	}
	if location.String() != "https://source.example/sessions/opaque?resource=one&rstream.token=edge-current" {
		t.Fatalf("session URL = %q", location)
	}
}

func newReceivingPeer(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create receiving peer: %v", err)
	}
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = peer.Close()
		t.Fatalf("add receiving transceiver: %v", err)
	}
	return peer
}

func newSendingPeer(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create sending peer: %v", err)
	}
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "source")
	if err != nil {
		_ = peer.Close()
		t.Fatalf("create source track: %v", err)
	}
	if _, err := peer.AddTrack(track); err != nil {
		_ = peer.Close()
		t.Fatalf("add source track: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	return peer
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
