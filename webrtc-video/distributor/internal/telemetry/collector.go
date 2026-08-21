package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxDatagramBytes       = 16 * 1024
	maxProcesses           = 1024
	maxCompletedAttempts   = 8192
	processStaleAfter      = 10 * time.Second
	backoffSchedulingGrace = 2 * time.Second
	completedAttemptKeep   = 5 * time.Minute
	maximumUnixSocketPath  = 100
)

type Collector struct {
	connection *net.UnixConn
	socketPath string
	aggregator *aggregator
	closeOnce  sync.Once
	closeErr   error
}

type aggregator struct {
	mu                sync.Mutex
	processes         map[string]processRecord
	attempts          map[string]attemptRecord
	completedAttempts map[string]time.Time
	totals            Counters
	attemptTotals     map[string]uint64
	invalidMessages   uint64
	droppedSnapshots  uint64
	staleProcesses    uint64
}

type processRecord struct {
	sequence   uint64
	dropped    uint64
	lastSeen   time.Time
	staleAt    time.Time
	state      string
	attemptID  string
	retryAfter time.Duration
}

type attemptRecord struct {
	processID string
	counters  Counters
}

func NewCollector(socketPath string) (*Collector, error) {
	if err := validateSocketPath(socketPath); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return nil, err
	}
	connection, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = connection.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	return &Collector{
		connection: connection,
		socketPath: socketPath,
		aggregator: newAggregator(),
	}, nil
}

func (c *Collector) Run(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.connection.Close()
		case <-done:
		}
	}()
	defer close(done)
	buffer := make([]byte, maxDatagramBytes+1)
	for {
		size, _, err := c.connection.ReadFromUnix(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return err
		}
		if size > maxDatagramBytes {
			c.aggregator.recordInvalid()
			continue
		}
		value, err := decodeMessage(buffer[:size])
		if err != nil {
			c.aggregator.recordInvalid()
			continue
		}
		if err := c.aggregator.apply(value, time.Now()); err != nil {
			c.aggregator.recordInvalid()
		}
	}
}

func (c *Collector) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, c.aggregator.openMetrics(time.Now()))
	})
	mux.HandleFunc("HEAD /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (c *Collector) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.connection.Close()
		if err := os.Remove(c.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			c.closeErr = errors.Join(c.closeErr, err)
		}
	})
	return c.closeErr
}

func newAggregator() *aggregator {
	return &aggregator{
		processes:         make(map[string]processRecord),
		attempts:          make(map[string]attemptRecord),
		completedAttempts: make(map[string]time.Time),
		attemptTotals: map[string]uint64{
			OutcomeCanceled:  0,
			OutcomeCompleted: 0,
			OutcomeFailed:    0,
			OutcomePermanent: 0,
		},
	}
}

func (a *aggregator) apply(value message, now time.Time) error {
	if err := validateMessage(value); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneStale(now)
	process, exists := a.processes[value.ProcessID]
	if !exists && value.State == StateStopped {
		return nil
	}
	if !exists && len(a.processes) >= maxProcesses {
		return errors.New("telemetry process limit reached")
	}
	if value.Sequence <= process.sequence {
		return nil
	}
	if value.DroppedSnapshots < process.dropped {
		return errors.New("telemetry dropped-snapshot counter decreased")
	}
	if value.State == StateBackoff && process.attemptID != "" {
		return errors.New("telemetry process entered backoff with an active attempt")
	}
	droppedDelta := value.DroppedSnapshots - process.dropped
	nextDropped, carry := bits.Add64(a.droppedSnapshots, droppedDelta, 0)
	if carry != 0 {
		return errors.New("telemetry dropped-snapshot counter overflowed")
	}
	if value.State == StateStopped {
		if process.attemptID != "" && a.attemptTotals[OutcomeCanceled] == ^uint64(0) {
			return errors.New("telemetry attempt counter overflowed")
		}
		a.droppedSnapshots = nextDropped
		if process.attemptID != "" {
			delete(a.attempts, process.attemptID)
			a.attemptTotals[OutcomeCanceled]++
		}
		delete(a.processes, value.ProcessID)
		return nil
	}
	nextTotals := a.totals
	if value.AttemptID != "" {
		if _, completed := a.completedAttempts[value.AttemptID]; completed {
			return errors.New("telemetry attempt was already completed")
		}
		if process.attemptID != "" && process.attemptID != value.AttemptID {
			return errors.New("telemetry process has overlapping attempts")
		}
		attempt := a.attempts[value.AttemptID]
		if attempt.processID != "" && attempt.processID != value.ProcessID {
			return errors.New("telemetry attempt changed process")
		}
		delta, ok := counterDelta(value.Counters, attempt.counters)
		if !ok {
			return errors.New("telemetry attempt counter decreased")
		}
		nextTotals, ok = addCounters(a.totals, delta)
		if !ok {
			return errors.New("telemetry aggregate counter overflowed")
		}
		if value.Completed && a.attemptTotals[value.Outcome] == ^uint64(0) {
			return errors.New("telemetry attempt counter overflowed")
		}
	}
	a.totals = nextTotals
	if value.AttemptID != "" {
		if value.Completed {
			a.attemptTotals[value.Outcome]++
			a.makeCompletedAttemptSpace()
			a.completedAttempts[value.AttemptID] = now
			delete(a.attempts, value.AttemptID)
			process.attemptID = ""
		} else {
			a.attempts[value.AttemptID] = attemptRecord{processID: value.ProcessID, counters: value.Counters}
			process.attemptID = value.AttemptID
		}
	}
	a.droppedSnapshots = nextDropped
	process.sequence = value.Sequence
	process.dropped = value.DroppedSnapshots
	process.lastSeen = now
	process.staleAt = time.Time{}
	process.state = value.State
	process.retryAfter = time.Duration(value.RetryAfterMilliseconds) * time.Millisecond
	a.processes[value.ProcessID] = process
	return nil
}

func (a *aggregator) makeCompletedAttemptSpace() {
	if len(a.completedAttempts) < maxCompletedAttempts {
		return
	}
	oldestID := ""
	var oldest time.Time
	for attemptID, completedAt := range a.completedAttempts {
		if oldestID == "" || completedAt.Before(oldest) {
			oldestID = attemptID
			oldest = completedAt
		}
	}
	delete(a.completedAttempts, oldestID)
}

func (a *aggregator) openMetrics(now time.Time) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneStale(now)
	states := map[string]uint64{StateActive: 0, StateBackoff: 0, StateIdle: 0}
	var maximumRetryAfter time.Duration
	for _, process := range a.processes {
		if !process.staleAt.IsZero() {
			continue
		}
		states[process.state]++
		maximumRetryAfter = max(maximumRetryAfter, process.retryAfter)
	}
	var output strings.Builder
	writeMetricHeader(&output, "rstream_video_distributor_children", "Current on-demand distributor child processes by lifecycle state.", "gauge")
	for _, state := range []string{StateActive, StateBackoff, StateIdle} {
		writeLabeledValue(&output, "rstream_video_distributor_children", "state", state, states[state])
	}
	writeMetricHeader(&output, "rstream_video_distributor_attempts_total", "Total distributor bridge attempts by terminal outcome.", "counter")
	for _, outcome := range []string{OutcomeCanceled, OutcomeCompleted, OutcomeFailed, OutcomePermanent} {
		writeLabeledValue(&output, "rstream_video_distributor_attempts_total", "outcome", outcome, a.attemptTotals[outcome])
	}
	writeMetricHeader(&output, "rstream_video_distributor_retry_after_seconds", "Longest current supervisor backoff before a distributor child retries.", "gauge")
	writeFloatValue(&output, "rstream_video_distributor_retry_after_seconds", maximumRetryAfter.Seconds())
	writeCounterFamilies(&output, a.totals)
	writeMetricHeader(&output, "rstream_video_distributor_telemetry_dropped_snapshots_total", "Total live snapshots dropped by child-side bounded telemetry queues.", "counter")
	writeValue(&output, "rstream_video_distributor_telemetry_dropped_snapshots_total", a.droppedSnapshots)
	writeMetricHeader(&output, "rstream_video_distributor_telemetry_invalid_messages_total", "Total malformed or contract-invalid local telemetry messages rejected by the collector.", "counter")
	writeValue(&output, "rstream_video_distributor_telemetry_invalid_messages_total", a.invalidMessages)
	writeMetricHeader(&output, "rstream_video_distributor_telemetry_stale_processes_total", "Total distributor child processes expired after missing their bounded heartbeat window.", "counter")
	writeValue(&output, "rstream_video_distributor_telemetry_stale_processes_total", a.staleProcesses)
	output.WriteString("# EOF\n")
	return output.String()
}

func (a *aggregator) recordInvalid() {
	a.mu.Lock()
	a.invalidMessages++
	a.mu.Unlock()
}

func (a *aggregator) pruneStale(now time.Time) {
	for processID, process := range a.processes {
		if !process.staleAt.IsZero() {
			if now.Sub(process.staleAt) > completedAttemptKeep {
				if process.attemptID != "" {
					delete(a.attempts, process.attemptID)
				}
				delete(a.processes, processID)
			}
			continue
		}
		staleAfter := processStaleAfter
		if process.state == StateBackoff {
			staleAfter = max(staleAfter, process.retryAfter+backoffSchedulingGrace)
		}
		if now.Sub(process.lastSeen) <= staleAfter {
			continue
		}
		process.staleAt = now
		a.processes[processID] = process
		a.staleProcesses++
	}
	a.pruneCompleted(now)
}

func (a *aggregator) pruneCompleted(now time.Time) {
	for attemptID, completedAt := range a.completedAttempts {
		if now.Sub(completedAt) > completedAttemptKeep {
			delete(a.completedAttempts, attemptID)
		}
	}
}

func validateMessage(value message) error {
	if value.Version != protocolVersion || !validID(value.ProcessID) || value.Sequence == 0 || !validState(value.State) || !validRetryAfter(value.RetryAfterMilliseconds) {
		return errors.New("telemetry envelope is invalid")
	}
	if value.State == StateActive && value.AttemptID == "" {
		return errors.New("active telemetry requires an attempt")
	}
	if value.Completed && (value.State != StateIdle || value.AttemptID == "" || !validOutcome(value.Outcome)) {
		return errors.New("completed telemetry is invalid")
	}
	if !value.Completed && value.Outcome != "" {
		return errors.New("non-terminal telemetry has an outcome")
	}
	if value.State == StateBackoff && (value.AttemptID != "" || value.Completed) {
		return errors.New("backoff telemetry cannot own an attempt")
	}
	if value.State == StateIdle && !value.Completed {
		return errors.New("idle telemetry must complete an attempt")
	}
	if value.State == StateStopped && (value.AttemptID != "" || value.Completed || value.Outcome != "" || value.RetryAfterMilliseconds != 0 || value.Counters != (Counters{})) {
		return errors.New("stopped telemetry cannot own an attempt")
	}
	return nil
}

func validID(value string) bool {
	if len(value) != 22 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func decodeMessage(payload []byte) (message, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value message
	if err := decoder.Decode(&value); err != nil {
		return message{}, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return message{}, errors.New("telemetry datagram contains multiple values")
		}
		return message{}, err
	}
	return value, nil
}

func validateSocketPath(socketPath string) error {
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath || len(socketPath) > maximumUnixSocketPath || strings.ContainsRune(socketPath, '\x00') {
		return errors.New("telemetry socket path is invalid")
	}
	return nil
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("telemetry socket path exists and is not a socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("telemetry socket is not owned by the current user")
	}
	connection, dialErr := net.DialTimeout("unixgram", socketPath, 50*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("telemetry socket is already active")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
		return fmt.Errorf("inspect existing telemetry socket: %w", dialErr)
	}
	return os.Remove(socketPath)
}

func counterDelta(current Counters, previous Counters) (Counters, bool) {
	values := [18][2]uint64{
		{current.Received, previous.Received},
		{current.RTXReceived, previous.RTXReceived},
		{current.SourceFEC, previous.SourceFEC},
		{current.FECCandidates, previous.FECCandidates},
		{current.Duplicates, previous.Duplicates},
		{current.DuplicateRTX, previous.DuplicateRTX},
		{current.DuplicateFEC, previous.DuplicateFEC},
		{current.RepairedRTX, previous.RepairedRTX},
		{current.RepairedFEC, previous.RepairedFEC},
		{current.LateRTX, previous.LateRTX},
		{current.LateFEC, previous.LateFEC},
		{current.ReorderLate, previous.ReorderLate},
		{current.ReorderDiscarded, previous.ReorderDiscarded},
		{current.InvalidFEC, previous.InvalidFEC},
		{current.NACKRequests, previous.NACKRequests},
		{current.Expired, previous.Expired},
		{current.SourceICERestarts, previous.SourceICERestarts},
		{current.SourceCredentialRefreshFailures, previous.SourceCredentialRefreshFailures},
	}
	for _, value := range values {
		if value[0] < value[1] {
			return Counters{}, false
		}
	}
	return Counters{
		Received:                        current.Received - previous.Received,
		RTXReceived:                     current.RTXReceived - previous.RTXReceived,
		SourceFEC:                       current.SourceFEC - previous.SourceFEC,
		FECCandidates:                   current.FECCandidates - previous.FECCandidates,
		Duplicates:                      current.Duplicates - previous.Duplicates,
		DuplicateRTX:                    current.DuplicateRTX - previous.DuplicateRTX,
		DuplicateFEC:                    current.DuplicateFEC - previous.DuplicateFEC,
		RepairedRTX:                     current.RepairedRTX - previous.RepairedRTX,
		RepairedFEC:                     current.RepairedFEC - previous.RepairedFEC,
		LateRTX:                         current.LateRTX - previous.LateRTX,
		LateFEC:                         current.LateFEC - previous.LateFEC,
		ReorderLate:                     current.ReorderLate - previous.ReorderLate,
		ReorderDiscarded:                current.ReorderDiscarded - previous.ReorderDiscarded,
		InvalidFEC:                      current.InvalidFEC - previous.InvalidFEC,
		NACKRequests:                    current.NACKRequests - previous.NACKRequests,
		Expired:                         current.Expired - previous.Expired,
		SourceICERestarts:               current.SourceICERestarts - previous.SourceICERestarts,
		SourceCredentialRefreshFailures: current.SourceCredentialRefreshFailures - previous.SourceCredentialRefreshFailures,
	}, true
}

func addCounters(left Counters, right Counters) (Counters, bool) {
	values := [18][2]uint64{
		{left.Received, right.Received},
		{left.RTXReceived, right.RTXReceived},
		{left.SourceFEC, right.SourceFEC},
		{left.FECCandidates, right.FECCandidates},
		{left.Duplicates, right.Duplicates},
		{left.DuplicateRTX, right.DuplicateRTX},
		{left.DuplicateFEC, right.DuplicateFEC},
		{left.RepairedRTX, right.RepairedRTX},
		{left.RepairedFEC, right.RepairedFEC},
		{left.LateRTX, right.LateRTX},
		{left.LateFEC, right.LateFEC},
		{left.ReorderLate, right.ReorderLate},
		{left.ReorderDiscarded, right.ReorderDiscarded},
		{left.InvalidFEC, right.InvalidFEC},
		{left.NACKRequests, right.NACKRequests},
		{left.Expired, right.Expired},
		{left.SourceICERestarts, right.SourceICERestarts},
		{left.SourceCredentialRefreshFailures, right.SourceCredentialRefreshFailures},
	}
	var result [18]uint64
	for index, value := range values {
		sum, carry := bits.Add64(value[0], value[1], 0)
		if carry != 0 {
			return Counters{}, false
		}
		result[index] = sum
	}
	return Counters{
		Received:                        result[0],
		RTXReceived:                     result[1],
		SourceFEC:                       result[2],
		FECCandidates:                   result[3],
		Duplicates:                      result[4],
		DuplicateRTX:                    result[5],
		DuplicateFEC:                    result[6],
		RepairedRTX:                     result[7],
		RepairedFEC:                     result[8],
		LateRTX:                         result[9],
		LateFEC:                         result[10],
		ReorderLate:                     result[11],
		ReorderDiscarded:                result[12],
		InvalidFEC:                      result[13],
		NACKRequests:                    result[14],
		Expired:                         result[15],
		SourceICERestarts:               result[16],
		SourceCredentialRefreshFailures: result[17],
	}, true
}

func writeCounterFamilies(output *strings.Builder, counters Counters) {
	primary := counters.Received - min(counters.Received, counters.RTXReceived+counters.FECCandidates)
	writeMetricHeader(output, "rstream_video_distributor_source_packets_total", "Total source packets observed by the repair bridge by packet kind.", "counter")
	for _, value := range []struct {
		kind  string
		count uint64
	}{{"media", primary}, {"rtx", counters.RTXReceived}, {"fec", counters.SourceFEC}} {
		writeLabeledValue(output, "rstream_video_distributor_source_packets_total", "kind", value.kind, value.count)
	}
	writeMetricHeader(output, "rstream_video_distributor_repaired_packets_total", "Total missing media packets restored inside the bounded reorder window.", "counter")
	writeLabeledValue(output, "rstream_video_distributor_repaired_packets_total", "repair", "rtx", counters.RepairedRTX)
	writeLabeledValue(output, "rstream_video_distributor_repaired_packets_total", "repair", "fec", counters.RepairedFEC)
	writeMetricHeader(output, "rstream_video_distributor_duplicate_packets_total", "Total duplicate packets discarded by packet kind.", "counter")
	writeLabeledValue(output, "rstream_video_distributor_duplicate_packets_total", "kind", "media", counters.Duplicates)
	writeLabeledValue(output, "rstream_video_distributor_duplicate_packets_total", "kind", "rtx", counters.DuplicateRTX)
	writeLabeledValue(output, "rstream_video_distributor_duplicate_packets_total", "kind", "fec", counters.DuplicateFEC)
	writeMetricHeader(output, "rstream_video_distributor_late_repair_packets_total", "Total repair packets that arrived after the reorder window.", "counter")
	writeLabeledValue(output, "rstream_video_distributor_late_repair_packets_total", "repair", "rtx", counters.LateRTX)
	writeLabeledValue(output, "rstream_video_distributor_late_repair_packets_total", "repair", "fec", counters.LateFEC)
	for _, value := range []struct {
		name  string
		help  string
		count uint64
	}{
		{"rstream_video_distributor_nack_requests_total", "Total RTP sequence numbers requested through NACK feedback.", counters.NACKRequests},
		{"rstream_video_distributor_expired_missing_packets_total", "Total missing packets abandoned after bounded repair attempts.", counters.Expired},
		{"rstream_video_distributor_reorder_late_packets_total", "Total packets received after their sequence had left the reorder window.", counters.ReorderLate},
		{"rstream_video_distributor_reorder_discarded_packets_total", "Total buffered packets discarded during teardown or discontinuity handling.", counters.ReorderDiscarded},
		{"rstream_video_distributor_invalid_fec_packets_total", "Total FlexFEC packets rejected by the decoder.", counters.InvalidFEC},
		{"rstream_video_distributor_source_ice_restarts_total", "Total source ICE generations renewed before their credentials expired.", counters.SourceICERestarts},
		{"rstream_video_distributor_source_credential_refresh_failures_total", "Total failed attempts to refresh source signaling or ICE credentials.", counters.SourceCredentialRefreshFailures},
	} {
		writeMetricHeader(output, value.name, value.help, "counter")
		writeValue(output, value.name, value.count)
	}
}

func writeMetricHeader(output *strings.Builder, name string, help string, metricType string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}

func writeLabeledValue(output *strings.Builder, name string, label string, labelValue string, value uint64) {
	fmt.Fprintf(output, "%s{%s=%s} %s\n", name, label, strconv.Quote(labelValue), strconv.FormatUint(value, 10))
}

func writeValue(output *strings.Builder, name string, value uint64) {
	fmt.Fprintf(output, "%s %s\n", name, strconv.FormatUint(value, 10))
}

func writeFloatValue(output *strings.Builder, name string, value float64) {
	fmt.Fprintf(output, "%s %s\n", name, strconv.FormatFloat(value, 'f', -1, 64))
}
