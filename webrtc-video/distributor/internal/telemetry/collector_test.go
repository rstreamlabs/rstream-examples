package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testProcessID = "AAAAAAAAAAAAAAAAAAAAAA"
	testAttemptID = "BBBBBBBBBBBBBBBBBBBBBB"
)

func TestAggregatorAccumulatesMonotonicAttemptsAndBoundedStates(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	messages := []message{
		{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive, Counters: Counters{Received: 10, RepairedRTX: 1, SourceICERestarts: 1, SourceCredentialRefreshFailures: 1}},
		{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 2, State: StateActive, DroppedSnapshots: 2, Counters: Counters{Received: 15, RepairedRTX: 3, SourceICERestarts: 2, SourceCredentialRefreshFailures: 2}},
		{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 3, State: StateIdle, Outcome: OutcomeFailed, Completed: true, DroppedSnapshots: 2, Counters: Counters{Received: 20, RepairedRTX: 4, SourceICERestarts: 3, SourceCredentialRefreshFailures: 4}},
		{Version: 1, ProcessID: testProcessID, Sequence: 4, State: StateBackoff, RetryAfterMilliseconds: 1500, DroppedSnapshots: 2},
	}
	for _, value := range messages {
		if err := aggregator.apply(value, now); err != nil {
			t.Fatalf("apply telemetry: %v", err)
		}
	}
	metrics := aggregator.openMetrics(now)
	for _, expected := range []string{
		`rstream_video_distributor_children{state="backoff"} 1`,
		`rstream_video_distributor_attempts_total{outcome="failed"} 1`,
		`rstream_video_distributor_retry_after_seconds 1.5`,
		`rstream_video_distributor_repaired_packets_total{repair="rtx"} 4`,
		`rstream_video_distributor_source_packets_total{kind="media"} 20`,
		`rstream_video_distributor_source_ice_restarts_total 3`,
		`rstream_video_distributor_source_credential_refresh_failures_total 4`,
		`rstream_video_distributor_telemetry_dropped_snapshots_total 2`,
		"# EOF\n",
	} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("metrics do not contain %q:\n%s", expected, metrics)
		}
	}
	if err := aggregator.apply(messages[2], now); err != nil {
		t.Fatalf("duplicate sequence: %v", err)
	}
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 5, State: StateActive}, now); err == nil {
		t.Fatal("completed attempt identifier was reusable")
	}
}

func TestAggregatorRejectsCounterRegressionAndOverlappingAttempts(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive, Counters: Counters{Received: 10, SourceCredentialRefreshFailures: 2}}, now); err != nil {
		t.Fatalf("apply initial telemetry: %v", err)
	}
	for _, value := range []message{
		{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 2, State: StateActive, Counters: Counters{Received: 9}},
		{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 2, State: StateActive, Counters: Counters{Received: 10, SourceCredentialRefreshFailures: 1}},
		{Version: 1, ProcessID: testProcessID, AttemptID: "CCCCCCCCCCCCCCCCCCCCCC", Sequence: 2, State: StateActive},
		{Version: 1, ProcessID: testProcessID, Sequence: 2, State: StateBackoff},
	} {
		if err := aggregator.apply(value, now); err == nil {
			t.Fatalf("invalid telemetry was accepted: %+v", value)
		}
	}
}

func TestAggregatorExpiresSilentChildrenWithoutLosingCounters(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	value := message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive, Counters: Counters{Received: 9}}
	if err := aggregator.apply(value, now); err != nil {
		t.Fatalf("apply telemetry: %v", err)
	}
	metrics := aggregator.openMetrics(now.Add(processStaleAfter + time.Nanosecond))
	if !strings.Contains(metrics, `rstream_video_distributor_children{state="active"} 0`) || !strings.Contains(metrics, `rstream_video_distributor_source_packets_total{kind="media"} 9`) || !strings.Contains(metrics, `rstream_video_distributor_telemetry_stale_processes_total 1`) {
		t.Fatalf("stale-process metrics are incorrect:\n%s", metrics)
	}
	resumedAt := now.Add(processStaleAfter + time.Second)
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 2, State: StateActive, Counters: Counters{Received: 12}}, resumedAt); err != nil {
		t.Fatalf("resume telemetry: %v", err)
	}
	metrics = aggregator.openMetrics(resumedAt)
	if !strings.Contains(metrics, `rstream_video_distributor_children{state="active"} 1`) || !strings.Contains(metrics, `rstream_video_distributor_source_packets_total{kind="media"} 12`) || strings.Contains(metrics, `rstream_video_distributor_source_packets_total{kind="media"} 21`) {
		t.Fatalf("resumed-process metrics were double counted:\n%s", metrics)
	}
}

func TestAggregatorBoundsStaleProcessQuarantine(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive}, now); err != nil {
		t.Fatalf("apply telemetry: %v", err)
	}
	staleAt := now.Add(processStaleAfter + time.Nanosecond)
	_ = aggregator.openMetrics(staleAt)
	_ = aggregator.openMetrics(staleAt.Add(completedAttemptKeep + time.Nanosecond))
	if len(aggregator.processes) != 0 || len(aggregator.attempts) != 0 {
		t.Fatalf("stale quarantine retained %d processes and %d attempts", len(aggregator.processes), len(aggregator.attempts))
	}
}

func TestAggregatorDoesNotExpireAChildDuringItsDeclaredBackoff(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	backoff := message{Version: 1, ProcessID: testProcessID, Sequence: 1, State: StateBackoff, RetryAfterMilliseconds: (15 * time.Second).Milliseconds()}
	if err := aggregator.apply(backoff, now); err != nil {
		t.Fatalf("apply backoff telemetry: %v", err)
	}
	metrics := aggregator.openMetrics(now.Add(15 * time.Second))
	if !strings.Contains(metrics, `rstream_video_distributor_children{state="backoff"} 1`) || !strings.Contains(metrics, `rstream_video_distributor_telemetry_stale_processes_total 0`) {
		t.Fatalf("declared backoff was classified as a stale child:\n%s", metrics)
	}
	metrics = aggregator.openMetrics(now.Add(15*time.Second + backoffSchedulingGrace + time.Nanosecond))
	if !strings.Contains(metrics, `rstream_video_distributor_children{state="backoff"} 0`) || !strings.Contains(metrics, `rstream_video_distributor_telemetry_stale_processes_total 1`) {
		t.Fatalf("expired backoff was not classified as stale:\n%s", metrics)
	}
}

func TestAggregatorStopsAProcessAndCancelsItsUnfinishedAttempt(t *testing.T) {
	aggregator := newAggregator()
	now := time.Unix(100, 0)
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive, Counters: Counters{Received: 9}}, now); err != nil {
		t.Fatalf("apply active telemetry: %v", err)
	}
	if err := aggregator.apply(message{Version: 1, ProcessID: testProcessID, Sequence: 2, State: StateStopped}, now); err != nil {
		t.Fatalf("apply stopped telemetry: %v", err)
	}
	metrics := aggregator.openMetrics(now)
	if !strings.Contains(metrics, `rstream_video_distributor_children{state="active"} 0`) || !strings.Contains(metrics, `rstream_video_distributor_attempts_total{outcome="canceled"} 1`) || !strings.Contains(metrics, `rstream_video_distributor_source_packets_total{kind="media"} 9`) {
		t.Fatalf("stopped-process metrics are incorrect:\n%s", metrics)
	}
}

func TestReporterAndCollectorExchangeLifecycleWithoutBlockingObservers(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	collectorDone := make(chan error, 1)
	go func() { collectorDone <- collector.Run(ctx) }()
	reporter, err := NewReporter(socketPath)
	if err != nil {
		t.Fatalf("create reporter: %v", err)
	}
	attempt, err := reporter.BeginAttempt(context.Background())
	if err != nil {
		t.Fatalf("begin attempt: %v", err)
	}
	const observers = 64
	var workers sync.WaitGroup
	workers.Add(observers)
	for index := 0; index < observers; index++ {
		go func() {
			defer workers.Done()
			attempt.Observe(Counters{Received: observers})
		}()
	}
	workers.Wait()
	if err := attempt.Complete(context.Background(), Counters{Received: observers}, OutcomeCompleted); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if err := reporter.Backoff(context.Background(), time.Second); err != nil {
		t.Fatalf("publish backoff: %v", err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := reporter.Close(closeCtx); err != nil {
		t.Fatalf("close reporter: %v", err)
	}
	reportedErrors := 0
	for range reporter.Errors() {
		reportedErrors++
	}
	if reportedErrors > 1 {
		t.Fatalf("reporter emitted %d uncoalesced write errors", reportedErrors)
	}
	deadline := time.Now().Add(time.Second)
	metrics := ""
	for {
		metrics = collector.aggregator.openMetrics(time.Now())
		if strings.Contains(metrics, `rstream_video_distributor_attempts_total{outcome="completed"} 1`) && strings.Contains(metrics, `rstream_video_distributor_source_packets_total{kind="media"} 64`) && strings.Contains(metrics, `rstream_video_distributor_children{state="active"} 0`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reporter lifecycle was not collected:\n%s", metrics)
		}
		time.Sleep(time.Millisecond)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	response := httptest.NewRecorder()
	collector.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasSuffix(response.Body.String(), "# EOF\n") {
		t.Fatalf("metrics response = status %d body %q", response.Code, response.Body.String())
	}
	cancel()
	if err := <-collectorDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("collector result = %v", err)
	}
	if err := collector.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close collector: %v", err)
	}
}

func TestReporterCriticalAcknowledgementDoesNotHoldTheEnqueueLock(t *testing.T) {
	reporter := &Reporter{processID: testProcessID, queue: make(chan outbound, 2)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { firstDone <- reporter.publishCritical(ctx, message{State: StateActive}) }()
	var first outbound
	select {
	case first = <-reporter.queue:
	case <-ctx.Done():
		t.Fatal("first critical telemetry item was not queued")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- reporter.publishCritical(ctx, message{State: StateBackoff}) }()
	var second outbound
	select {
	case second = <-reporter.queue:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("critical telemetry acknowledgement held the enqueue lock")
	}
	first.result <- nil
	second.result <- nil
	if err := <-firstDone; err != nil {
		t.Fatalf("first critical telemetry result = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second critical telemetry result = %v", err)
	}
}

func TestReporterHeartbeatsAQuietAttemptAndStopsCleanly(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	collectorDone := make(chan error, 1)
	go func() { collectorDone <- collector.Run(ctx) }()
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("connect reporter: %v", err)
	}
	reporter := newReporter(connection, testProcessID, 5*time.Millisecond)
	attempt, err := reporter.BeginAttempt(context.Background())
	if err != nil {
		t.Fatalf("begin quiet attempt: %v", err)
	}
	eventually(t, func() bool {
		collector.aggregator.mu.Lock()
		defer collector.aggregator.mu.Unlock()
		return collector.aggregator.processes[testProcessID].sequence >= 2
	})
	if err := attempt.Complete(context.Background(), Counters{}, OutcomeCompleted); err != nil {
		t.Fatalf("complete quiet attempt: %v", err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := reporter.Close(closeCtx); err != nil {
		t.Fatalf("close heartbeat reporter: %v", err)
	}
	eventually(t, func() bool {
		collector.aggregator.mu.Lock()
		defer collector.aggregator.mu.Unlock()
		_, exists := collector.aggregator.processes[testProcessID]
		return !exists
	})
	cancel()
	if err := <-collectorDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("collector result = %v, want cancellation", err)
	}
	_ = collector.Close()
}

func TestCollectorRejectsMalformedDatagramsAndProtectsExistingFiles(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	blockedPath := filepath.Join(directory, "existing")
	if err := os.WriteFile(blockedPath, []byte("do not replace"), 0o600); err != nil {
		t.Fatalf("create existing file: %v", err)
	}
	if _, err := NewCollector(blockedPath); err == nil {
		t.Fatal("collector replaced a non-socket path")
	}
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("connect collector: %v", err)
	}
	if _, err := connection.Write([]byte(`{"version":1,"unknown":true}`)); err != nil {
		t.Fatalf("write malformed telemetry: %v", err)
	}
	_ = connection.Close()
	eventually(t, func() bool {
		return strings.Contains(collector.aggregator.openMetrics(time.Now()), `rstream_video_distributor_telemetry_invalid_messages_total 1`)
	})
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("collector result = %v", err)
	}
	_ = collector.Close()
	body, err := os.ReadFile(blockedPath)
	if err != nil || string(body) != "do not replace" {
		t.Fatalf("existing file changed: body %q error %v", body, err)
	}
}

func TestCollectorDoesNotReplaceAnActiveSocket(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	defer func() { _ = collector.Close() }()
	if _, err := NewCollector(socketPath); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second collector result = %v, want active-socket rejection", err)
	}
	reporter, err := NewReporter(socketPath)
	if err != nil {
		t.Fatalf("connect original collector: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(closeCtx); err != nil {
		t.Fatalf("close reporter: %v", err)
	}
}

func TestConcurrentCollectorClosePreservesTheFirstError(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatalf("unlink collector socket: %v", err)
	}
	if err := os.Mkdir(socketPath, 0o700); err != nil {
		t.Fatalf("replace socket with directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(socketPath, "guard"), []byte("preserve"), 0o600); err != nil {
		t.Fatalf("make replacement directory non-empty: %v", err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			results <- collector.Close()
		}()
	}
	close(start)
	first := <-results
	second := <-results
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Fatalf("concurrent close errors = %v and %v, want the same persisted error", first, second)
	}
	if err := collector.Close(); err == nil || err.Error() != first.Error() {
		t.Fatalf("repeated close error = %v, want %v", err, first)
	}
}

func TestDecodeMessageRejectsUnknownFieldsAndMultipleValues(t *testing.T) {
	valid := message{Version: 1, ProcessID: testProcessID, AttemptID: testAttemptID, Sequence: 1, State: StateActive}
	payload, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("encode telemetry: %v", err)
	}
	if _, err := decodeMessage(payload); err != nil {
		t.Fatalf("decode valid telemetry: %v", err)
	}
	for _, payload := range [][]byte{
		[]byte(`{"version":1,"unknown":true}`),
		append(append([]byte(nil), payload...), []byte(` {}`)...),
	} {
		if _, err := decodeMessage(payload); err == nil {
			t.Fatalf("invalid payload was accepted: %s", payload)
		}
	}
}

func TestReporterCloseIsIdempotentDuringConcurrentObservation(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	reporter, err := NewReporter(socketPath)
	if err != nil {
		t.Fatalf("create reporter: %v", err)
	}
	attempt, err := reporter.BeginAttempt(context.Background())
	if err != nil {
		t.Fatalf("begin attempt: %v", err)
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for index := uint64(0); index < 1000; index++ {
			attempt.Observe(Counters{Received: index})
		}
	}()
	go func() {
		defer workers.Done()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = reporter.Close(closeCtx)
	}()
	workers.Wait()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := reporter.Close(closeCtx); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	cancel()
	<-done
	_ = collector.Close()
}

func TestConcurrentReporterCloseWaitHonorsEachCallerContext(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	socketPath := filepath.Join(directory, "metrics.sock")
	collector, err := NewCollector(socketPath)
	if err != nil {
		t.Fatalf("create collector: %v", err)
	}
	defer func() { _ = collector.Close() }()
	reporter, err := NewReporter(socketPath)
	if err != nil {
		t.Fatalf("create reporter: %v", err)
	}
	reporter.enqueueMu.Lock()
	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Second)
	defer firstCancel()
	first := make(chan error, 1)
	go func() { first <- reporter.Close(firstCtx) }()
	eventually(t, func() bool { return reporter.closing.Load() })
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer secondCancel()
	err = reporter.Close(secondCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Close() error = %v, want deadline exceeded", err)
	}
	reporter.enqueueMu.Unlock()
	if err := <-first; err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := reporter.Close(context.Background()); err != nil {
		t.Fatalf("completed Close() error = %v", err)
	}
}

func TestReporterCloseRejectsMissingContext(t *testing.T) {
	reporter := &Reporter{}
	var ctx context.Context
	if err := reporter.Close(ctx); err == nil {
		t.Fatal("Close() accepted a nil context")
	}
}

func TestReporterCriticalPublishRejectsMissingContext(t *testing.T) {
	reporter := &Reporter{}
	var ctx context.Context
	if err := reporter.publishCritical(ctx, message{State: StateBackoff}); err == nil {
		t.Fatal("critical publish accepted a nil context")
	}
}

func TestTelemetryIdentityPreservesEntropySourceFailure(t *testing.T) {
	cause := errors.New("entropy source unavailable")
	if _, err := newIDFrom(errorReader{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("identity error = %v, want wrapped entropy failure", err)
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func shortTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "rstream-video-telemetry-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
