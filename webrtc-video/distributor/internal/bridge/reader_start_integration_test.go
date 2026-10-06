//go:build integration

package bridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaMTXReaderHookRequestsASharedSourceKeyFrame(t *testing.T) {
	executable := distributorExecutable(t)
	source := newSourceHarness(t)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTXWithRunCommand(t, mediaMTXExecutable(t), executable, server.URL+"/whep", false, executable+" reader-started")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	first := newViewer(t)
	defer first.close()
	waitSignal(t, source.connected, "source connection")
	deadline := time.Now().Add(750 * time.Millisecond)
	for source.plis.Load() < 2 { // publisher readiness, then the connected reader
		if time.Now().After(deadline) {
			t.Fatal("first reader did not trigger a source key-frame request")
		}
		time.Sleep(time.Millisecond)
	}
	// Leave the short coalescing window before joining the established source.
	// The new request must precede MediaMTX's ordinary two-second PLI period.
	time.Sleep(300 * time.Millisecond)
	before := source.plis.Load()
	started := time.Now()
	second := newViewer(t)
	defer second.close()
	for source.plis.Load() <= before {
		if time.Since(started) > 750*time.Millisecond {
			t.Fatal("connected reader did not trigger a prompt source key-frame request")
		}
		time.Sleep(time.Millisecond)
	}
	if source.posts.Load() != 1 {
		t.Fatalf("reader hook opened %d source sessions, want one", source.posts.Load())
	}
	first.close()
	second.close()
	waitSignal(t, source.deleted, "source cleanup after the final reader")
}
