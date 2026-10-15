package telemetry

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestBindingsFollowAttemptLifetimeAndCannotChangeIdentity(t *testing.T) {
	a := newAggregator()
	now := time.Now()
	b := Binding{Path: "robots/one/front", SourceSessionID: "source-a", DestinationSessionID: "publish-a"}
	m := message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive, Binding: &b}
	if err := a.apply(m, now); err != nil {
		t.Fatal(err)
	}
	snapshot := a.bindings(now)
	if len(snapshot.Bindings) != 1 || snapshot.RuntimeID == "" || snapshot.Bindings[0].SourceSessionID != "source-a" {
		t.Fatal(snapshot)
	}
	snapshot.Bindings[0].SourceSessionID = "mutated"
	if a.bindings(now).Bindings[0].SourceSessionID != "source-a" {
		t.Fatal("mutable snapshot")
	}
	b.SourceSessionID = "source-b"
	m.Sequence++
	if a.apply(m, now) == nil {
		t.Fatal("same attempt accepted a replacement source")
	}
	if len(a.bindings(now.Add(processStaleAfter+time.Second)).Bindings) != 0 {
		t.Fatal("stale binding retained")
	}
	b.SourceSessionID = "source-a"
	m.Sequence++
	if err := a.apply(m, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(a.bindings(now.Add(12*time.Second)).Bindings) != 1 {
		t.Fatal("heartbeat recovery missing")
	}
	m.Sequence++
	m.Binding = nil
	m.State = StateIdle
	m.Completed = true
	m.Outcome = OutcomeCanceled
	if err := a.apply(m, now.Add(13*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(a.bindings(now.Add(13*time.Second)).Bindings) != 0 {
		t.Fatal("completed binding retained")
	}
	if newAggregator().runtimeID == a.runtimeID {
		t.Fatal("collector restart retained incarnation")
	}
}

func TestBindingSurfaceIsPrivateAndRejectsBrowserRequests(t *testing.T) {
	c := &Collector{aggregator: newAggregator()}
	r := httptest.NewRequest("GET", "/bindings/v1", nil)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("bindings exposed on metrics listener")
	}
	for _, test := range []struct {
		method, path, origin string
		status               int
	}{
		{"GET", "/bindings/v1", "", 200},
		{"GET", "/bindings/v1", "https://example.test", 403},
		{"GET", "/bindings/v1?path=robots/one/front", "", 403},
		{"POST", "/bindings/v1", "", 405},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		c.BindingsHandler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%+v: %d", test, w.Code)
		}
	}
	for _, b := range []Binding{{Path: "../front", SourceSessionID: "a", DestinationSessionID: "b"}, {Path: "front", SourceSessionID: "https://host/session?secret=abc", DestinationSessionID: "b"}} {
		if validBinding(&b) {
			t.Fatal("unsafe binding accepted")
		}
	}
}
