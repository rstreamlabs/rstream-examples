package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/adaptation"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func TestQualityHTTPContractBoundsControlAndRejectsStaleChanges(t *testing.T) {
	cfg := config.Default()
	cfg.Quality.Presets = []config.QualityPreset{{ID: "low", Label: "Low", BitrateKbps: 2000}}
	policy, err := adaptation.NewQualityPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mutations := 0
	server := NewServer(logs.NewLogger(logs.NewHub(16), false), nil, nil, ServerOptions{Viewer: false, QualityState: policy.Snapshot, SelectQuality: func(ctx context.Context, mode, version string) (adaptation.QualityState, error) {
		mutations++
		if err := policy.Select(mode, version); err != nil {
			return adaptation.QualityState{}, err
		}
		return policy.Snapshot()
	}})
	call := func(method, body, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/quality", strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	response := call("GET", "", "")
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	var state adaptation.QualityState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"mode":"low","version":%q}`, state.Version)
	if response := call("PUT", body, "http://attacker.example"); response.Code != 403 {
		t.Fatal(response.Code)
	}
	if mutations != 0 {
		t.Fatal("cross-origin mutation reached producer")
	}
	if response := call("PUT", body, "http://localhost"); response.Code != 200 {
		t.Fatal(response.Code)
	}
	if response := call("PUT", body, ""); response.Code != 409 {
		t.Fatal(response.Code)
	}
	for _, bad := range []string{body + ` {}`, `{"mode":"low","extra":true}`, strings.Repeat(" ", 2049) + body} {
		if response := call("PUT", bad, ""); response.Code != 400 {
			t.Fatal(response.Code)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("control response may be cached")
	}
	rebinding := httptest.NewRecorder()
	LocalHandler(server.Handler()).ServeHTTP(rebinding, httptest.NewRequest(http.MethodGet, "http://attacker.example/api/quality", nil))
	if rebinding.Code != 403 {
		t.Fatal("rebinding host accepted")
	}
}
