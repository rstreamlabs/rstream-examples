package media

import (
	"sync"
	"testing"
	"time"
)

func TestSourceStatsAccountForConcurrentEncodingAndBackpressure(t *testing.T) {
	stats := &sourceStats{}
	subscriber := make(chan AccessUnit)
	source := &GStreamerSource{
		stats: stats,
		subs:  map[chan AccessUnit]struct{}{subscriber: {}},
	}
	const publishers = 16
	const framesPerPublisher = 250
	var publishersDone sync.WaitGroup
	publishersDone.Add(publishers)
	for publisher := range publishers {
		go func() {
			defer publishersDone.Done()
			for frame := range framesPerPublisher {
				source.publish(AccessUnit{
					Data:     []byte{1, 2, 3, 4},
					Duration: time.Second / 30,
					KeyFrame: (publisher*framesPerPublisher+frame)%10 == 0,
				})
			}
		}()
	}
	publishersDone.Wait()
	snapshot := stats.snapshot()
	wantFrames := uint64(publishers * framesPerPublisher)
	if snapshot.EncodedFrames != wantFrames {
		t.Fatalf("encoded frames = %d, want %d", snapshot.EncodedFrames, wantFrames)
	}
	if snapshot.EncodedKeyFrames != wantFrames/10 {
		t.Fatalf("encoded key frames = %d, want %d", snapshot.EncodedKeyFrames, wantFrames/10)
	}
	if snapshot.EncodedBytes != wantFrames*4 {
		t.Fatalf("encoded bytes = %d, want %d", snapshot.EncodedBytes, wantFrames*4)
	}
	wantDuration := wantFrames * uint64(time.Second/30)
	if snapshot.EncodedMediaNanoseconds != wantDuration {
		t.Fatalf("encoded media duration = %d ns, want %d", snapshot.EncodedMediaNanoseconds, wantDuration)
	}
	if snapshot.LastEncodedFrameUnixNano <= 0 {
		t.Fatal("last encoded frame timestamp was not recorded")
	}
	if snapshot.DeliveryDroppedFrames != wantFrames {
		t.Fatalf("dropped delivery frames = %d, want %d", snapshot.DeliveryDroppedFrames, wantFrames)
	}
	if snapshot.DeliveryDroppedBytes != wantFrames*4 {
		t.Fatalf("dropped delivery bytes = %d, want %d", snapshot.DeliveryDroppedBytes, wantFrames*4)
	}
}

func TestSourceInstanceGaugeIsReleasedExactlyOnce(t *testing.T) {
	stats := &sourceStats{}
	stats.sources.Add(1)
	busDone := make(chan struct{})
	close(busDone)
	source := &GStreamerSource{
		stats:   stats,
		busDone: busDone,
		subs:    make(map[chan AccessUnit]struct{}),
	}
	if err := source.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got := stats.snapshot().Sources; got != 0 {
		t.Fatalf("source instances = %d, want 0", got)
	}
}

func TestSourceMetricsDoNotAllocateOnTheFramePath(t *testing.T) {
	source := &GStreamerSource{
		stats: &sourceStats{},
		subs:  make(map[chan AccessUnit]struct{}),
	}
	unit := AccessUnit{
		Data:     make([]byte, 4096),
		Duration: time.Second / 30,
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		source.publish(unit)
	}); allocations != 0 {
		t.Fatalf("source metrics added %.2f allocation(s) per frame, want 0", allocations)
	}
}
