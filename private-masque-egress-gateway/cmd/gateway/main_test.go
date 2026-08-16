// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
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

func TestRunRstreamRetryLoopRejectsMissingContextWithoutRetry(t *testing.T) {
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RSTREAM_CONTEXT", "missing")
	t.Setenv("RSTREAM_ENGINE", "")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	attempts := 0
	err := runRstreamRetryLoop(ctx, time.Millisecond, discardLogger(), func() error {
		attempts++
		return runRstream(ctx, http.NotFoundHandler(), runOptions{logger: discardLogger()})
	})
	if err == nil || !strings.Contains(err.Error(), `context "missing" not found`) {
		t.Fatalf("runRstreamRetryLoop() error = %v, want missing context", err)
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

func TestRunRstreamRetryLoopCoalescesFailuresAndResetsAfterConnection(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	errorsByAttempt := []error{
		&connectedRstreamError{err: errors.New("first disconnect")},
		errors.New("reconnect failed"),
		&connectedRstreamError{err: errors.New("second disconnect")},
		nil,
	}
	attempts := 0
	err := runRstreamRetryLoop(t.Context(), time.Nanosecond, logger, func() error {
		err := errorsByAttempt[attempts]
		attempts++
		return err
	})
	if err != nil {
		t.Fatalf("runRstreamRetryLoop() error = %v", err)
	}
	if attempts != len(errorsByAttempt) {
		t.Fatalf("runRstreamRetryLoop() attempts = %d, want %d", attempts, len(errorsByAttempt))
	}
	if got := strings.Count(output.String(), "rstream tunnel unavailable; retrying"); got != 2 {
		t.Fatalf("retry warnings = %d, want 2; output = %q", got, output.String())
	}
}

func TestRunRstreamRetryLoopBoundsCallsDuringSustainedFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Millisecond)
	defer cancel()
	attempts := 0
	err := runRstreamRetryLoop(ctx, 20*time.Millisecond, discardLogger(), func() error {
		attempts++
		return errors.New("engine unavailable")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runRstreamRetryLoop() error = %v, want deadline exceeded", err)
	}
	if attempts < 2 || attempts > 4 {
		t.Fatalf("runRstreamRetryLoop() attempts = %d, want 2..4", attempts)
	}
}

func TestRstreamRetryDelayIsJitteredAndCapped(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		ceiling  time.Duration
		floor    time.Duration
	}{
		{name: "first", failures: 1, floor: 750 * time.Millisecond, ceiling: time.Second},
		{name: "third", failures: 3, floor: 3 * time.Second, ceiling: 4 * time.Second},
		{name: "capped", failures: 20, floor: 45 * time.Second, ceiling: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 100 {
				delay := rstreamRetryDelay(time.Second, tt.failures)
				if delay < tt.floor || delay > tt.ceiling {
					t.Fatalf("rstreamRetryDelay() = %s, want %s..%s", delay, tt.floor, tt.ceiling)
				}
			}
		})
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
