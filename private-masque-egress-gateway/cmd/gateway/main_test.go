// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
)

func TestRunRstreamRetryLoopReconnectsAfterTransportClosure(t *testing.T) {
	attempts := 0
	err := runRstreamRetryLoop(t.Context(), time.Millisecond, discardLogger(), func() error {
		attempts++
		if attempts == 1 {
			return errors.New("quic: transport closed: use of closed network connection")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runRstreamRetryLoop() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("runRstreamRetryLoop() attempts = %d, want 2", attempts)
	}
}

func TestRunRstreamRetryLoopRejectsConfigurationError(t *testing.T) {
	attempts := 0
	wantErr := fmt.Errorf("create tunnel: %w", &rstream.EngineError{
		Code:    rstream.EngineErrorCodeInvalidRequest,
		Message: "Custom hostname is not verified for this project.",
	})
	err := runRstreamRetryLoop(t.Context(), time.Millisecond, discardLogger(), func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runRstreamRetryLoop() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("runRstreamRetryLoop() attempts = %d, want 1", attempts)
	}
}

func TestRunRstreamRetryLoopStopsWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runRstreamRetryLoop(ctx, time.Hour, discardLogger(), func() error {
			close(attempted)
			return errors.New("connection reset")
		})
	}()
	<-attempted
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runRstreamRetryLoop() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runRstreamRetryLoop() did not stop after cancellation")
	}
}

func TestRetryableRstreamError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "transport closure", err: errors.New("connection reset"), want: true},
		{name: "service unavailable", err: &rstream.EngineError{Code: rstream.EngineErrorCodeServiceUnavailable}, want: true},
		{name: "resource conflict", err: &rstream.EngineError{Code: rstream.EngineErrorCodeResourceConflict}, want: true},
		{name: "legacy hostname conflict", err: &rstream.EngineError{Code: rstream.EngineErrorCodeInvalidRequest, Message: "Hostname is already in use."}, want: true},
		{name: "unverified hostname", err: &rstream.EngineError{Code: rstream.EngineErrorCodeInvalidRequest, Message: "Custom hostname is not verified for this project."}},
		{name: "unauthorized", err: &rstream.EngineError{Code: rstream.EngineErrorCodeUnauthorized}},
		{name: "feature unavailable", err: &rstream.EngineError{Code: rstream.EngineErrorCodeFeatureNotAvailable}},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableRstreamError(tt.err); got != tt.want {
				t.Fatalf("retryableRstreamError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
