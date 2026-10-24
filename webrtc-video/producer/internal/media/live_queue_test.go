package media

import (
	"sync"
	"testing"
	"time"
)

func liveUnit(index byte, key bool) AccessUnit {
	return AccessUnit{Data: []byte{index, 2}, Duration: time.Second / 30, KeyFrame: key}
}

func TestLiveQueueRecoversOnlyAtRandomAccess(t *testing.T) {
	queue := NewLiveQueue(2)
	defer queue.Close()
	if frames, bytes := queue.Publish(liveUnit(0, false)); frames != 1 || bytes != 2 {
		t.Fatal("dependent first picture admitted")
	}
	queue.Publish(liveUnit(1, true))
	<-queue.Units()
	queue.Publish(liveUnit(2, false))
	queue.Publish(liveUnit(3, false))
	if frames, bytes := queue.Publish(liveUnit(4, false)); frames != 3 || bytes != 6 {
		t.Fatalf("backlog not discarded/accounted: %d frames, %d bytes", frames, bytes)
	}
	queue.Publish(liveUnit(5, false))
	if len(queue.units) != 0 {
		t.Fatal("dependent picture delivered after loss")
	}
	recovery := liveUnit(6, true)
	queue.Publish(recovery)
	if got := <-queue.Units(); !got.KeyFrame || &got.Data[0] != &recovery.Data[0] || got.Duration != recovery.Duration {
		t.Fatal("recovery altered the source access unit")
	}
	queue.Publish(liveUnit(7, false))
	if got := <-queue.Units(); got.KeyFrame || got.Data[0] != 7 {
		t.Fatal("delivery did not resume after random access")
	}
}

func TestLiveQueueSlowReaderDoesNotStopAnotherReader(t *testing.T) {
	slow, fast := NewLiveQueue(2), NewLiveQueue(2)
	defer slow.Close()
	defer fast.Close()
	for index := byte(1); index <= 5; index++ {
		unit := liveUnit(index, index == 1 || index == 5)
		slow.Publish(unit)
		fast.Publish(unit)
		if got := <-fast.Units(); got.Data[0] != index {
			t.Fatal("slow reader affected fast delivery")
		}
	}
	if got := <-slow.Units(); !got.KeyFrame || got.Data[0] != 5 {
		t.Fatal("slow reader retained stale or dependent pictures")
	}
	slow.Publish(liveUnit(6, false))
	slow.Publish(liveUnit(7, false))
	if frames, _ := slow.Publish(liveUnit(8, true)); frames != 2 {
		t.Fatalf("key frame recovery dropped %d pictures, want 2", frames)
	}
	if got := <-slow.Units(); !got.KeyFrame || got.Data[0] != 8 {
		t.Fatal("incoming random access was discarded")
	}
}

func TestLiveQueueCloseAndPublicationAreConcurrentAndBounded(t *testing.T) {
	queue := NewLiveQueue(4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for index := range 1000 {
				queue.Publish(liveUnit(byte(index%256), index%10 == 0))
			}
		})
	}
	workers.Go(queue.Close)
	workers.Go(queue.Close)
	workers.Wait()
	for range queue.Units() {
	}
	if frames, _ := queue.Publish(liveUnit(1, true)); frames != 1 {
		t.Fatal("closed queue accepted a picture")
	}
}
