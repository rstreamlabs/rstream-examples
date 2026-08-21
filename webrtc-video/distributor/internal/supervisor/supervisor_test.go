package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type retryAfterFailure struct {
	delay time.Duration
}

func (e retryAfterFailure) Error() string {
	return "temporarily unavailable"
}

func (e retryAfterFailure) RetryAfter() time.Duration {
	return e.delay
}

func TestRunRetriesWithBoundedBackoffUntilCancellation(t *testing.T) {
	policy := Policy{
		InitialDelay: time.Millisecond,
		MaximumDelay: 4 * time.Millisecond,
		StableAfter:  time.Hour,
		Jitter:       func(delay time.Duration) time.Duration { return delay },
	}
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Uint32
	delays := make(chan time.Duration, 3)
	err := Run(ctx, policy, func(context.Context) error {
		if attempts.Add(1) == 4 {
			cancel()
		}
		return errors.New("temporary failure")
	}, func(_ error, delay time.Duration) {
		delays <- delay
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("supervisor error = %v, want cancellation", err)
	}
	close(delays)
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}
	index := 0
	for delay := range delays {
		if index >= len(want) || delay != want[index] {
			t.Fatalf("delay %d = %s, want %s", index, delay, want[index])
		}
		index++
	}
	if index != len(want) {
		t.Fatalf("retry delays = %d, want %d", index, len(want))
	}
}

func TestRunResetsBackoffAfterStableAttempt(t *testing.T) {
	policy := Policy{
		InitialDelay: time.Millisecond,
		MaximumDelay: 4 * time.Millisecond,
		StableAfter:  2 * time.Millisecond,
		Jitter:       func(delay time.Duration) time.Duration { return delay },
	}
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Uint32
	delays := make(chan time.Duration, 3)
	err := Run(ctx, policy, func(context.Context) error {
		attempt := attempts.Add(1)
		if attempt == 2 {
			time.Sleep(3 * time.Millisecond)
		}
		if attempt == 4 {
			cancel()
		}
		return errors.New("source unavailable")
	}, func(_ error, delay time.Duration) {
		delays <- delay
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("supervisor error = %v, want cancellation", err)
	}
	close(delays)
	want := []time.Duration{time.Millisecond, time.Millisecond, 2 * time.Millisecond}
	index := 0
	for delay := range delays {
		if index >= len(want) || delay != want[index] {
			t.Fatalf("delay %d = %s, want %s", index, delay, want[index])
		}
		index++
	}
	if index != len(want) {
		t.Fatalf("retry delays = %d, want %d", index, len(want))
	}
}

func TestRunRejectsInvalidPolicy(t *testing.T) {
	err := Run(context.Background(), Policy{}, func(context.Context) error { return nil }, nil)
	if err == nil {
		t.Fatal("supervisor accepted an invalid retry policy")
	}
}

func TestRunHonorsBoundedRetryAfterGuidance(t *testing.T) {
	policy := Policy{
		InitialDelay: time.Millisecond,
		MaximumDelay: 4 * time.Millisecond,
		StableAfter:  time.Hour,
		Jitter:       func(time.Duration) time.Duration { return 0 },
	}
	ctx, cancel := context.WithCancel(context.Background())
	delays := make(chan time.Duration, 2)
	var attempts atomic.Uint32
	err := Run(ctx, policy, func(context.Context) error {
		switch attempts.Add(1) {
		case 1:
			return fmt.Errorf("source WHEP failed: %w", retryAfterFailure{delay: 3 * time.Millisecond})
		case 2:
			return retryAfterFailure{delay: time.Hour}
		default:
			cancel()
			return errors.New("cancelled")
		}
	}, func(_ error, delay time.Duration) {
		delays <- delay
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("supervisor error = %v, want cancellation", err)
	}
	close(delays)
	want := []time.Duration{3 * time.Millisecond, 4 * time.Millisecond}
	index := 0
	for delay := range delays {
		if index >= len(want) || delay != want[index] {
			t.Fatalf("delay %d = %s, want %s", index, delay, want[index])
		}
		index++
	}
	if index != len(want) {
		t.Fatalf("retry delays = %d, want %d", index, len(want))
	}
}

func TestRunDoesNotRetryPermanentFailure(t *testing.T) {
	policy := Policy{
		InitialDelay: time.Millisecond,
		MaximumDelay: 4 * time.Millisecond,
		StableAfter:  time.Hour,
		Jitter:       func(delay time.Duration) time.Duration { return delay },
	}
	cause := errors.New("worker shutdown invariant failed")
	var attempts atomic.Uint32
	var retries atomic.Uint32
	err := Run(context.Background(), policy, func(context.Context) error {
		attempts.Add(1)
		return Permanent(cause)
	}, func(error, time.Duration) {
		retries.Add(1)
	})
	if !errors.Is(err, cause) {
		t.Fatalf("supervisor error = %v, want permanent cause", err)
	}
	if attempts.Load() != 1 || retries.Load() != 0 {
		t.Fatalf("attempts = %d retries = %d, want 1 and 0", attempts.Load(), retries.Load())
	}
}
