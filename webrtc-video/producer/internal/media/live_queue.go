package media

import "sync"

// LiveQueue bounds a viewer's backlog without stopping capture. After delivery
// loss it discards dependent pictures until a random-access unit arrives.
// Recording subscriptions must not use it: originals require loss detection.
type LiveQueue struct {
	mu      sync.Mutex
	units   chan AccessUnit
	waiting bool
	closed  bool
}

// NewLiveQueue creates a queue with a positive, fixed delivery budget.
func NewLiveQueue(capacity int) *LiveQueue {
	if capacity < 1 {
		panic("live queue requires positive capacity")
	}
	return &LiveQueue{units: make(chan AccessUnit, capacity), waiting: true}
}

func (q *LiveQueue) Units() <-chan AccessUnit { return q.units }

// Publish never waits for a reader. Returned counters include queued pictures
// discarded on overflow. Surviving units are unchanged; the queue does not
// decode pictures or generate replacement frames.
func (q *LiveQueue) Publish(unit AccessUnit) (frames, bytes uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.waiting && !unit.KeyFrame {
		return 1, uint64(len(unit.Data))
	}
	select {
	case q.units <- unit:
		q.waiting = false
		return 0, 0
	default:
	}
	q.waiting = true
	// There is one serialized publisher. A concurrent reader may drain some
	// pictures too; only the ones removed here count as delivery losses.
drain:
	for {
		select {
		case old := <-q.units:
			frames++
			bytes += uint64(len(old.Data))
		default:
			break drain
		}
	}
	if unit.KeyFrame {
		q.units <- unit // The drained, positive-capacity queue cannot block.
		q.waiting = false
	} else {
		frames++
		bytes += uint64(len(unit.Data))
	}
	return frames, bytes
}

func (q *LiveQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.units)
	}
}
