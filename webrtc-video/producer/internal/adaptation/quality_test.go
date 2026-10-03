package adaptation

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func testQualityPolicy(t *testing.T) (config.Config, *QualityPolicy) {
	t.Helper()
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps = 500
	cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps = 10000
	cfg.Quality.Presets = []config.QualityPreset{{ID: "low", Label: "Low", BitrateKbps: 1000}, {ID: "high", Label: "High", BitrateKbps: 10000}}
	p, err := NewQualityPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, p
}

func TestQualitySelectionVersionsRejectConcurrentAndPreviousProcessWrites(t *testing.T) {
	cfg, p := testQualityPolicy(t)
	before, _ := p.Snapshot()
	if err := p.Select("low", before.Version); err != nil {
		t.Fatal(err)
	}
	if err := p.Select("high", before.Version); !errors.Is(err, ErrQualityVersion) {
		t.Fatalf("stale selection: %v", err)
	}
	restarted, err := NewQualityPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := p.Snapshot()
	if err := restarted.Select("high", after.Version); !errors.Is(err, ErrQualityVersion) {
		t.Fatalf("previous process selection: %v", err)
	}
	after.Modes[1].BitrateKbps = 1
	if p.Limit() != 1000 {
		t.Fatal("snapshot mutated policy")
	}
}

func TestQualityControllerCapsSourceStillAdaptsAndRestoresAuto(t *testing.T) {
	cfg, p := testQualityPolicy(t)
	encoder := &recordingEncoder{target: 5000, updates: make(chan int, 32)}
	controller := NewController(logs.NewLogger(logs.NewHub(16), false), encoder, newTestTWCCGCCBackend(t, cfg), time.Hour, nil, nil, nil, p)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(9000000)
	selectMode := func(mode string) {
		t.Helper()
		state, _ := p.Snapshot()
		if err := p.Select(mode, state.Version); err != nil {
			t.Fatal(err)
		}
		controller.QualityChanged()
	}
	want := func(target int) {
		t.Helper()
		select {
		case got := <-encoder.updates:
			if got != target {
				t.Fatalf("target %d, want %d", got, target)
			}
		case <-time.After(time.Second):
			t.Fatal("quality update timed out")
		}
	}
	selectMode("low")
	want(1000)
	controller.UpdateEstimatedBitrate(700000)
	want(700)
	selectMode("high") // A selected high ceiling cannot force a congested uplink higher.
	select {
	case got := <-encoder.updates:
		t.Fatalf("forced congestion override: %d", got)
	case <-time.After(20 * time.Millisecond):
	}
	controller.UpdateEstimatedBitrate(9000000)
	selectMode("auto")
	want(9000)
	controller.Close()
	controller.QualityChanged() // Shutdown never blocks the control caller.
}

func TestQualitySelectionAndFeedbackRaceRemainBounded(t *testing.T) {
	cfg, p := testQualityPolicy(t)
	encoder := &recordingEncoder{target: 5000, updates: make(chan int, 4096)}
	controller := NewController(logs.NewLogger(logs.NewHub(16), false), encoder, newTestTWCCGCCBackend(t, cfg), time.Millisecond, nil, nil, nil, p)
	controller.Start()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for i := range 200 {
				state, _ := p.Snapshot()
				mode := "low"
				if i%2 == 0 {
					mode = "auto"
				}
				_ = p.Select(mode, state.Version)
				controller.QualityChanged()
				controller.UpdateEstimatedBitrate(500000 + i*10000)
			}
		})
	}
	workers.Wait()
	controller.Close()
	if got := encoder.Info().TargetBitrateKbps; got < 500 || got > 10000 {
		t.Fatalf("unbounded target %d", got)
	}
}
