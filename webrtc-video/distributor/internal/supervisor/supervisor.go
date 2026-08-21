package supervisor

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

type Policy struct {
	InitialDelay time.Duration
	MaximumDelay time.Duration
	StableAfter  time.Duration
	Jitter       func(time.Duration) time.Duration
}

type retryAfterError interface {
	RetryAfter() time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		InitialDelay: time.Second,
		MaximumDelay: 15 * time.Second,
		StableAfter:  30 * time.Second,
		Jitter: func(delay time.Duration) time.Duration {
			window := delay / 5
			if window <= 0 {
				return delay
			}
			return delay - window + time.Duration(rand.Int64N(int64(2*window)+1))
		},
	}
}

func Run(ctx context.Context, policy Policy, attempt func(context.Context) error, onRetry func(error, time.Duration)) error {
	if err := validate(policy, attempt); err != nil {
		return err
	}
	delay := policy.InitialDelay
	for {
		started := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var permanent permanentError
		if errors.As(err, &permanent) {
			return err
		}
		if err == nil {
			err = errors.New("distributor attempt stopped unexpectedly")
		}
		if time.Since(started) >= policy.StableAfter {
			delay = policy.InitialDelay
		}
		wait := policy.Jitter(delay)
		if wait < 0 || wait > policy.MaximumDelay {
			wait = delay
		}
		var retryAfter retryAfterError
		if errors.As(err, &retryAfter) {
			minimum := min(retryAfter.RetryAfter(), policy.MaximumDelay)
			if minimum > wait {
				wait = minimum
			}
		}
		if onRetry != nil {
			onRetry(err, wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		if delay < policy.MaximumDelay/2 {
			delay *= 2
		} else {
			delay = policy.MaximumDelay
		}
	}
}

func validate(policy Policy, attempt func(context.Context) error) error {
	if attempt == nil {
		return errors.New("distributor attempt is required")
	}
	if policy.InitialDelay <= 0 || policy.MaximumDelay < policy.InitialDelay || policy.StableAfter <= 0 || policy.Jitter == nil {
		return errors.New("distributor retry policy is invalid")
	}
	return nil
}
