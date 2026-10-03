package adaptation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

func TestQualityFormatStateSeparatesRequestsFromObservedEncoders(t *testing.T) {
	cfg := formatTestConfig()
	state := NewQualityFormatState(&cfg)
	small, large := cfg.Profiles[0].Format, cfg.Profiles[2].Format
	state.Add(media.SourceFormatState{Running: true, Requested: small, Observed: large, Pending: true})
	state.Add(media.SourceFormatState{Running: true, Requested: small, Observed: small})
	state.Add(media.SourceFormatState{Running: true, Requested: large, FailedUpdates: 1})
	state.Add(media.SourceFormatState{Running: false, Requested: large, Observed: large, Pending: true})
	if state.ActiveEncoders != 3 || state.PendingEncoders != 1 || state.UnconfirmedEncoders != 1 || state.FailedUpdates != 1 {
		t.Fatalf("invalid totals: %+v", state)
	}
	if state.Profiles[0].RequestedEncoders != 2 || state.Profiles[0].ObservedEncoders != 1 || state.Profiles[2].RequestedEncoders != 1 || state.Profiles[2].ObservedEncoders != 1 {
		t.Fatalf("requests and observations mixed: %+v", state.Profiles)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "minBitrateKbps") || !strings.Contains(string(data), `"width":320`) {
		t.Fatalf("invalid public format schema: %s", data)
	}
	state.Profiles[0].Width = 999
	if cfg.Profiles[0].Width != 320 {
		t.Fatal("snapshot mutated configuration")
	}
	var disabled *QualityFormatState
	disabled.Add(media.SourceFormatState{Running: true})
}
