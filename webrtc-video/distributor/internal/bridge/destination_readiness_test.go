package bridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/config"
)

func TestOpenDestinationDoesNotPublishBeforeTransportConnects(t *testing.T) {
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remote.Close() }()
	var deletes atomic.Uint32
	patched := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			offer, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if readErr != nil {
				t.Error(readErr)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if setErr := remote.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); setErr != nil {
				t.Error(setErr)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			answer, answerErr := remote.CreateAnswer(nil)
			if answerErr != nil {
				t.Error(answerErr)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if setErr := remote.SetLocalDescription(answer); setErr != nil {
				t.Error(setErr)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// Signal normally, but never exchange candidates: SDP completion must
			// not allow the adapter to consume and silently lose initial SPS/PPS.
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("Location", "/session")
			w.Header().Set("ETag", `"session"`)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, answer.SDP)
		case http.MethodPatch:
			w.WriteHeader(http.StatusNoContent)
			select {
			case patched <- struct{}{}:
			default:
			}
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		peer, _, _, session, openErr := openDestination(ctx, config.Config{Path: "camera", MediaMTXURL: endpoint}, "", server.Client())
		if session != nil {
			_ = closeSession(session)
		} else if peer != nil {
			_ = peer.Close()
		}
		finished <- openErr
	}()
	select {
	case <-patched:
	case openErr := <-finished:
		t.Fatalf("destination returned before transport connection: %v", openErr)
	case <-time.After(5 * time.Second):
		cancel()
		<-finished
		t.Fatal("destination did not finish signaling")
	}
	select {
	case openErr := <-finished:
		t.Fatalf("destination returned before transport connection: %v", openErr)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case openErr := <-finished:
		if !errors.Is(openErr, context.Canceled) {
			t.Fatalf("destination cancellation = %v", openErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("destination did not stop after cancellation")
	}
	if got := deletes.Load(); got != 1 {
		t.Fatalf("session cleanup requests = %d, want exactly one", got)
	}
}

func TestWaitForPeerConnectionHandlesStateChangesAndCancellation(t *testing.T) {
	for _, state := range []webrtc.PeerConnectionState{
		webrtc.PeerConnectionStateConnected,
		webrtc.PeerConnectionStateFailed,
		webrtc.PeerConnectionStateClosed,
	} {
		t.Run(state.String(), func(t *testing.T) {
			peer := &peerStateHarness{state: webrtc.PeerConnectionStateConnecting}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- waitForPeerConnection(ctx, peer) }()
			// Transition before or during registration: both paths must observe it.
			peer.setState(state)
			err := <-finished
			if state == webrtc.PeerConnectionStateConnected {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("terminal state %s returned %v", state, err)
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if peer.callback != nil {
				t.Fatal("startup callback retained after wait")
			}
		})
	}
	for _, alreadyConnected := range []bool{false, true} {
		peer := &peerStateHarness{state: webrtc.PeerConnectionStateConnecting}
		if alreadyConnected {
			peer.state = webrtc.PeerConnectionStateConnected
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := waitForPeerConnection(ctx, peer); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled wait (connected=%t) = %v", alreadyConnected, err)
		}
	}
	peer := &peerStateHarness{state: webrtc.PeerConnectionStateConnecting}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := waitForPeerConnection(ctx, peer); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unconnected wait = %v, want deadline", err)
	}
}
