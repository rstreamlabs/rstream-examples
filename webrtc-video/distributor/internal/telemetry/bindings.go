package telemetry

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const BindingsSocketEnvironmentVariable = "RSTREAM_DISTRIBUTOR_BINDINGS_SOCKET"

// Binding contains identifiers only. SDP, credentials and resource URLs do not
// belong here: WHIP/WHEP Location paths may be bearer secrets.
type Binding struct {
	Path                 string `json:"path"`
	SourceSessionID      string `json:"sourceSessionId"`
	DestinationSessionID string `json:"destinationSessionId"`
}

type BindingSnapshot struct {
	Version   int             `json:"version"`
	RuntimeID string          `json:"runtimeId"`
	Bindings  []ActiveBinding `json:"bindings"`
}
type ActiveBinding struct {
	Binding
	AttemptID string `json:"attemptId"`
	// Age of the local process heartbeat, not frame age or proof of media arrival.
	HeartbeatAgeMilliseconds int64 `json:"heartbeatAgeMilliseconds"`
}

func cloneBinding(b *Binding) *Binding {
	if b == nil {
		return nil
	}
	copy := *b
	return &copy
}

func bindingID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validBinding(b *Binding) bool {
	if b == nil || !bindingID(b.SourceSessionID) || !bindingID(b.DestinationSessionID) || len(b.Path) == 0 || len(b.Path) > 512 {
		return false
	}
	for _, part := range strings.Split(b.Path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

func (a *aggregator) bindings(now time.Time) BindingSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneStale(now)
	out := BindingSnapshot{Version: 1, RuntimeID: a.runtimeID, Bindings: []ActiveBinding{}}
	for id, attempt := range a.attempts {
		process := a.processes[attempt.processID]
		if attempt.binding == nil || process.state != StateActive || !process.staleAt.IsZero() {
			continue
		}
		out.Bindings = append(out.Bindings, ActiveBinding{Binding: *attempt.binding, AttemptID: id, HeartbeatAgeMilliseconds: max(0, now.Sub(process.lastSeen).Milliseconds())})
	}
	return out
}

// BindingsHandler is only mounted on the optional private Unix socket. The
// public metrics listener deliberately does not expose session associations.
func (c *Collector) BindingsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" || r.URL.RawQuery != "" || r.URL.RawPath != "" {
			http.Error(w, "unsupported request", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/bindings/v1" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c.aggregator.bindings(time.Now()))
	})
}
