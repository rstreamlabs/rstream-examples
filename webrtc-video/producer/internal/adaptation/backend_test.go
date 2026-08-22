package adaptation

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

type recordingEncoder struct {
	mu      sync.Mutex
	target  int
	updates chan int
	err     error
}

func (e *recordingEncoder) Info() media.EncoderInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return media.EncoderInfo{TargetBitrateKbps: e.target}
}

func (e *recordingEncoder) SetTargetBitrateKbps(target int) error {
	e.mu.Lock()
	if e.err != nil {
		err := e.err
		e.mu.Unlock()
		return err
	}
	e.target = target
	e.mu.Unlock()
	e.updates <- target
	return nil
}

func TestControllerReportsEncoderReconfigurationFailure(t *testing.T) {
	encoder := &recordingEncoder{
		target:  5000,
		updates: make(chan int, 1),
		err:     errors.New("encoder unavailable"),
	}
	cfg := config.Default()
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, cfg),
		time.Hour,
		nil,
		nil,
		nil,
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(2_000_000)
	deadline := time.Now().Add(time.Second)
	for {
		snapshot := controller.Snapshot()
		if snapshot.FailedUpdates == 1 {
			if snapshot.AppliedUpdates != 0 {
				t.Fatalf("unexpected successful update after failure: %+v", snapshot)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("adaptive failure was not reported: %+v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestControllerAppliesDecreasesWithoutWaitingForItsTicker(t *testing.T) {
	encoder := &recordingEncoder{
		target:  5000,
		updates: make(chan int, 2),
	}
	cfg := config.Default()
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, cfg),
		time.Hour,
		nil,
		nil,
		nil,
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(1_200_000)
	select {
	case target := <-encoder.updates:
		if target != cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps {
			t.Fatalf(
				"immediate encoder target = %d, want configured minimum %d",
				target,
				cfg.WebRTC.Adaptive.TWCCGCC.MinBitrateKbps,
			)
		}
	case <-time.After(time.Second):
		t.Fatal("encoder decrease waited for the periodic update ticker")
	}
	controller.UpdateEstimatedBitrate(2_000_000)
	select {
	case target := <-encoder.updates:
		t.Fatalf("encoder increase %d bypassed the periodic update interval", target)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestControllerAppliesSuccessiveMaterialDecreasesImmediately(t *testing.T) {
	const interval = 50 * time.Millisecond
	encoder := &recordingEncoder{
		target:  8000,
		updates: make(chan int, 2),
	}
	cfg := config.Default()
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, cfg),
		interval,
		nil,
		nil,
		nil,
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(7_000_000)
	select {
	case target := <-encoder.updates:
		if target != 7000 {
			t.Fatalf("first encoder target = %d, want 7000", target)
		}
	case <-time.After(time.Second):
		t.Fatal("first material decrease was not applied immediately")
	}
	controller.UpdateEstimatedBitrate(5_000_000)
	select {
	case target := <-encoder.updates:
		if target != 5000 {
			t.Fatalf("successive encoder target = %d, want latest target 5000", target)
		}
	case <-time.After(interval / 2):
		t.Fatal("successive material decrease waited for the update interval")
	}
	deadline := time.Now().Add(time.Second)
	for {
		snapshot := controller.Snapshot()
		if snapshot.AppliedUpdates == 2 {
			if snapshot.FailedUpdates != 0 {
				t.Fatalf("unexpected adaptive update stats: %+v", snapshot)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("adaptive update stats did not catch up: %+v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestControllerRefreshesEstimateBeforePeriodicIncrease(t *testing.T) {
	const interval = 100 * time.Millisecond
	var estimate atomic.Int64
	estimate.Store(6_000_000)
	encoder := &recordingEncoder{
		target:  5000,
		updates: make(chan int, 2),
	}
	cfg := config.Default()
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, cfg),
		interval,
		func() int { return int(estimate.Load()) },
		nil,
		nil,
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(6_000_000)
	estimate.Store(2_000_000)
	select {
	case target := <-encoder.updates:
		if target != 2000 {
			t.Fatalf("encoder applied stale increase %d, want current decrease 2000", target)
		}
	case <-time.After(4 * interval):
		t.Fatal("controller did not refresh the current estimate on its ticker")
	}
}

func TestControllerBlocksPeriodicIncreaseUntilMeasuredLossRecovers(t *testing.T) {
	const interval = 20 * time.Millisecond
	var lossBits atomic.Uint64
	lossBits.Store(math.Float64bits(0.02))
	encoder := &recordingEncoder{
		target:  2000,
		updates: make(chan int, 2),
	}
	cfg := config.Default()
	cfg.WebRTC.Adaptive.TWCCGCC.IncreaseHoldAfterLoss = "40ms"
	recoveryKeyFrames := make(chan struct{}, 1)
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, cfg),
		interval,
		func() int { return 3_000_000 },
		func() float64 { return math.Float64frombits(lossBits.Load()) },
		func() { recoveryKeyFrames <- struct{}{} },
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(3_000_000)
	select {
	case target := <-encoder.updates:
		t.Fatalf("encoder increased to %d while loss remained above threshold", target)
	case <-time.After(4 * interval):
	}
	lossBits.Store(math.Float64bits(0))
	select {
	case target := <-encoder.updates:
		if target != 3000 {
			t.Fatalf("post-recovery target = %d, want 3000", target)
		}
	case <-time.After(8 * interval):
		t.Fatal("encoder did not resume estimator-driven adaptation after loss recovery")
	}
	select {
	case <-recoveryKeyFrames:
	case <-time.After(time.Second):
		t.Fatal("post-loss bitrate recovery did not request a key frame")
	}
	select {
	case <-recoveryKeyFrames:
		t.Fatal("one loss episode requested more than one recovery key frame")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestControllerRequestsOneKeyFrameForLossDrivenDecreases(t *testing.T) {
	const interval = 20 * time.Millisecond
	var estimate atomic.Int64
	estimate.Store(4_000_000)
	var lossBits atomic.Uint64
	lossBits.Store(math.Float64bits(0.30))
	encoder := &recordingEncoder{target: 8000, updates: make(chan int, 2)}
	recoveryKeyFrames := make(chan struct{}, 2)
	controller := NewController(
		logs.NewLogger(logs.NewHub(16), false),
		encoder,
		newTestTWCCGCCBackend(t, config.Default()),
		interval,
		func() int { return int(estimate.Load()) },
		func() float64 { return math.Float64frombits(lossBits.Load()) },
		func() { recoveryKeyFrames <- struct{}{} },
	)
	controller.Start()
	t.Cleanup(controller.Close)
	controller.UpdateEstimatedBitrate(4_000_000)
	select {
	case target := <-encoder.updates:
		if target != 4000 {
			t.Fatalf("loss-driven target = %d, want 4000", target)
		}
	case <-time.After(4 * interval):
		t.Fatal("controller did not apply the loss-driven decrease")
	}
	select {
	case <-recoveryKeyFrames:
	case <-time.After(time.Second):
		t.Fatal("loss-driven decrease did not request a recovery key frame")
	}
	estimate.Store(3_000_000)
	controller.UpdateEstimatedBitrate(3_000_000)
	select {
	case target := <-encoder.updates:
		if target != 3000 {
			t.Fatalf("second loss-driven target = %d, want 3000", target)
		}
	case <-time.After(4 * interval):
		t.Fatal("controller did not apply the second loss-driven decrease")
	}
	select {
	case <-recoveryKeyFrames:
		t.Fatal("one loss episode requested more than one recovery key frame")
	case <-time.After(2 * interval):
	}
}

func TestControllerReportsOnlyItsRunningLifecycleAsActive(t *testing.T) {
	encoder := &recordingEncoder{target: 5000, updates: make(chan int, 1)}
	cfg := config.Default()
	controller := NewController(logs.NewLogger(logs.NewHub(16), false), encoder, newTestTWCCGCCBackend(t, cfg), time.Hour, nil, nil, nil)
	if snapshot := controller.Snapshot(); snapshot.Active {
		t.Fatalf("controller reported active before Start: %+v", snapshot)
	}
	controller.Start()
	if snapshot := controller.Snapshot(); !snapshot.Active {
		t.Fatalf("controller reported inactive after Start: %+v", snapshot)
	}
	controller.Close()
	if snapshot := controller.Snapshot(); snapshot.Active {
		t.Fatalf("controller reported active after Close: %+v", snapshot)
	}
	controller.Close()
}

func TestControllerConcurrentStartAndCloseAlwaysTerminatesInactive(t *testing.T) {
	const iterations = 100
	for iteration := 0; iteration < iterations; iteration++ {
		encoder := &recordingEncoder{target: 5000, updates: make(chan int, 1)}
		cfg := config.Default()
		controller := NewController(logs.NewLogger(logs.NewHub(16), false), encoder, newTestTWCCGCCBackend(t, cfg), time.Hour, nil, nil, nil)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			controller.Start()
		}()
		go func() {
			defer workers.Done()
			<-start
			controller.Close()
		}()
		close(start)
		workers.Wait()
		controller.Close()
		if snapshot := controller.Snapshot(); snapshot.Active {
			t.Fatalf("iteration %d left controller active: %+v", iteration, snapshot)
		}
	}
}
