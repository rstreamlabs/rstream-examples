package webrtc

import (
	"sync"
	"testing"
	"time"
)

func TestFeedbackLossGuardReducesAfterPersistentHighLoss(t *testing.T) {
	now := time.Unix(0, 0)
	guard := newFeedbackLossGuard(2_000_000)
	guard.now = func() time.Time { return now }
	if target, changed := guard.observe(100, 20, 8_000_000); changed || target != 8_000_000 {
		t.Fatalf("first high-loss report = (%d, %t), want (8000000, false)", target, changed)
	}
	now = now.Add(50 * time.Millisecond)
	if target, changed := guard.observe(100, 20, 8_000_000); !changed || target != 7_200_000 {
		t.Fatalf("persistent high-loss report = (%d, %t), want (7200000, true)", target, changed)
	}
	now = now.Add(feedbackLossGuardDecreasePeriod)
	if target, changed := guard.observe(100, 50, 8_000_000); !changed || target != 5_400_000 {
		t.Fatalf("second reduction = (%d, %t), want (5400000, true)", target, changed)
	}
	snapshot := guard.snapshot()
	if !snapshot.Active || snapshot.Reductions != 2 || snapshot.LastObservedLoss != 0.5 {
		t.Fatalf("unexpected guard snapshot: %+v", snapshot)
	}
}

func TestFeedbackLossGuardBoundsReductionAndRecoversGradually(t *testing.T) {
	now := time.Unix(0, 0)
	guard := newFeedbackLossGuard(2_000_000)
	guard.now = func() time.Time { return now }
	for index := 0; index < 20; index++ {
		guard.observe(100, 100, 8_000_000)
		now = now.Add(feedbackLossGuardDecreasePeriod)
	}
	if target := guard.effectiveBitrate(8_000_000); target != 2_000_000 {
		t.Fatalf("bounded target = %d, want 2000000", target)
	}
	guard.observe(100, 0, 8_000_000)
	now = now.Add(feedbackLossGuardRecoveryDelay)
	if target, changed := guard.observe(100, 0, 8_000_000); !changed || target <= 2_000_000 {
		t.Fatalf("first recovery = (%d, %t), want a bounded increase", target, changed)
	}
	previous := guard.effectiveBitrate(8_000_000)
	for guard.snapshot().Active {
		now = now.Add(feedbackLossGuardRecoveryPeriod)
		target, _ := guard.observe(100, 0, 8_000_000)
		if target < previous {
			t.Fatalf("recovery target fell from %d to %d", previous, target)
		}
		previous = target
	}
	if previous != 8_000_000 {
		t.Fatalf("recovered target = %d, want 8000000", previous)
	}
}

func TestFeedbackLossGuardIgnoresSmallOrIsolatedReports(t *testing.T) {
	guard := newFeedbackLossGuard(2_000_000)
	for _, report := range []struct {
		reported int
		lost     int
	}{
		{reported: 19, lost: 19},
		{reported: 100, lost: 11},
		{reported: 100, lost: 0},
		{reported: 100, lost: -1},
		{reported: 100, lost: 101},
	} {
		if target, changed := guard.observe(report.reported, report.lost, 8_000_000); changed || target != 8_000_000 {
			t.Fatalf("report %+v = (%d, %t), want (8000000, false)", report, target, changed)
		}
	}
}

func TestFeedbackLossGuardIsSafeUnderConcurrentObservation(t *testing.T) {
	guard := newFeedbackLossGuard(2_000_000)
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func(offset int) {
			defer group.Done()
			for sample := 0; sample < 1_000; sample++ {
				lost := 0
				if (sample+offset)%3 == 0 {
					lost = 25
				}
				guard.observe(100, lost, 8_000_000)
				_ = guard.effectiveBitrate(8_000_000)
				_ = guard.snapshot()
			}
		}(index)
	}
	group.Wait()
	target := guard.effectiveBitrate(8_000_000)
	if target < 2_000_000 || target > 8_000_000 {
		t.Fatalf("concurrent target = %d, want [2000000, 8000000]", target)
	}
}
