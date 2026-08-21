package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	reporterQueueCapacity   = 8
	reporterWriteTimeout    = 100 * time.Millisecond
	reporterHeartbeat       = 3 * time.Second
	criticalPublishTimeout  = time.Second
	reporterShutdownTimeout = 2 * time.Second
	writeRetryDelay         = time.Millisecond
)

type Reporter struct {
	connection    *net.UnixConn
	processID     string
	queue         chan outbound
	done          chan struct{}
	errors        chan error
	enqueueMu     sync.Mutex
	heartbeatStop chan struct{}
	heartbeatDone chan struct{}
	last          message
	hasLast       bool
	closeOnce     sync.Once
	closeDone     chan struct{}
	closeMu       sync.Mutex
	closeErr      error
	sequence      atomic.Uint64
	dropped       atomic.Uint64
	closing       atomic.Bool
	closed        atomic.Bool
}

type outbound struct {
	value    message
	deadline time.Time
	result   chan error
}

type Attempt struct {
	reporter *Reporter
	id       string
	done     atomic.Bool
}

func NewReporter(socketPath string) (*Reporter, error) {
	if socketPath == "" {
		return nil, nil
	}
	processID, err := newID()
	if err != nil {
		return nil, err
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	reporter := newReporter(connection, processID, reporterHeartbeat)
	return reporter, nil
}

func newReporter(connection *net.UnixConn, processID string, heartbeatInterval time.Duration) *Reporter {
	reporter := &Reporter{
		connection:    connection,
		processID:     processID,
		queue:         make(chan outbound, reporterQueueCapacity),
		done:          make(chan struct{}),
		closeDone:     make(chan struct{}),
		errors:        make(chan error, 1),
		heartbeatStop: make(chan struct{}),
		heartbeatDone: make(chan struct{}),
	}
	go reporter.run()
	go reporter.runHeartbeat(heartbeatInterval)
	return reporter
}

func (r *Reporter) BeginAttempt(ctx context.Context) (*Attempt, error) {
	if r == nil {
		return nil, nil
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	attempt := &Attempt{reporter: r, id: id}
	err = r.publishCritical(ctx, message{AttemptID: id, State: StateActive})
	return attempt, err
}

func (r *Reporter) Backoff(ctx context.Context, retryAfter time.Duration) error {
	if r == nil {
		return nil
	}
	if retryAfter < 0 || retryAfter > 24*time.Hour {
		return errors.New("telemetry retry delay is invalid")
	}
	return r.publishCritical(ctx, message{State: StateBackoff, RetryAfterMilliseconds: retryAfter.Milliseconds()})
}

func (r *Reporter) Errors() <-chan error {
	if r == nil {
		return nil
	}
	return r.errors
}

func (r *Reporter) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("the telemetry reporter close context is required")
	}
	r.closeOnce.Do(func() {
		r.closing.Store(true)
		close(r.heartbeatStop)
		go r.finishClose()
	})
	select {
	case <-r.closeDone:
		r.closeMu.Lock()
		err := r.closeErr
		r.closeMu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for telemetry reporter close: %w", ctx.Err())
	}
}

func (r *Reporter) finishClose() {
	ctx, cancel := context.WithTimeout(context.Background(), reporterShutdownTimeout)
	defer cancel()
	var closeErr error
	select {
	case <-r.heartbeatDone:
	case <-ctx.Done():
		closeErr = fmt.Errorf("stop telemetry heartbeat: %w", ctx.Err())
	}
	closeErr = errors.Join(closeErr, r.publishCriticalState(ctx, message{State: StateStopped}, true))
	r.enqueueMu.Lock()
	r.closed.Store(true)
	close(r.queue)
	r.enqueueMu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
		closeErr = errors.Join(closeErr, fmt.Errorf("stop telemetry writer: %w", ctx.Err()))
		_ = r.connection.Close()
	}
	r.closeMu.Lock()
	r.closeErr = closeErr
	close(r.closeDone)
	r.closeMu.Unlock()
}

func (a *Attempt) Observe(counters Counters) {
	if a == nil || a.done.Load() {
		return
	}
	a.reporter.publish(message{AttemptID: a.id, State: StateActive, Counters: counters})
}

func (a *Attempt) Complete(ctx context.Context, counters Counters, outcome string) error {
	if a == nil {
		return nil
	}
	if !validOutcome(outcome) {
		return errors.New("telemetry attempt outcome is invalid")
	}
	if !a.done.CompareAndSwap(false, true) {
		return nil
	}
	return a.reporter.publishCritical(ctx, message{AttemptID: a.id, State: StateIdle, Outcome: outcome, Completed: true, Counters: counters})
}

func (r *Reporter) publish(value message) {
	if r.closing.Load() || r.closed.Load() || !r.enqueueMu.TryLock() {
		r.dropped.Add(1)
		return
	}
	defer r.enqueueMu.Unlock()
	if r.closing.Load() || r.closed.Load() {
		r.dropped.Add(1)
		return
	}
	r.prepare(&value)
	select {
	case r.queue <- outbound{value: value}:
		r.last = value
		r.hasLast = true
	default:
		r.dropped.Add(1)
	}
}

func (r *Reporter) publishCritical(ctx context.Context, value message) error {
	return r.publishCriticalState(ctx, value, false)
}

func (r *Reporter) publishCriticalState(ctx context.Context, value message, allowClosing bool) error {
	if ctx == nil {
		return errors.New("the telemetry publish context is required")
	}
	if r.closed.Load() || (!allowClosing && r.closing.Load()) {
		return errors.New("telemetry reporter is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.enqueueMu.Lock()
	if r.closed.Load() || (!allowClosing && r.closing.Load()) {
		r.enqueueMu.Unlock()
		return errors.New("telemetry reporter is closed")
	}
	if err := ctx.Err(); err != nil {
		r.enqueueMu.Unlock()
		return err
	}
	r.prepare(&value)
	deadline := time.Now().Add(criticalPublishTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	result := make(chan error, 1)
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case r.queue <- outbound{value: value, deadline: deadline, result: result}:
		r.last = value
		r.hasLast = true
	case <-ctx.Done():
		r.enqueueMu.Unlock()
		return ctx.Err()
	case <-timer.C:
		r.enqueueMu.Unlock()
		return context.DeadlineExceeded
	}
	r.enqueueMu.Unlock()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func (r *Reporter) prepare(value *message) {
	value.Version = protocolVersion
	value.ProcessID = r.processID
	value.Sequence = r.sequence.Add(1)
	value.DroppedSnapshots = r.dropped.Load()
}

func (r *Reporter) run() {
	defer func() { _ = r.connection.Close() }()
	defer close(r.done)
	defer close(r.errors)
	for item := range r.queue {
		err := r.write(item)
		if err != nil {
			if item.result == nil {
				r.dropped.Add(1)
			}
			select {
			case r.errors <- err:
			default:
			}
		}
		if item.result != nil {
			item.result <- err
		}
	}
}

func (r *Reporter) runHeartbeat(interval time.Duration) {
	defer close(r.heartbeatDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.heartbeatStop:
			return
		case <-ticker.C:
			r.publishHeartbeat()
		}
	}
}

func (r *Reporter) publishHeartbeat() {
	if r.closing.Load() || r.closed.Load() || !r.enqueueMu.TryLock() {
		return
	}
	defer r.enqueueMu.Unlock()
	if r.closing.Load() || r.closed.Load() || !r.hasLast || (r.last.State != StateActive && r.last.State != StateBackoff) {
		return
	}
	value := r.last
	r.prepare(&value)
	select {
	case r.queue <- outbound{value: value}:
		r.last = value
	default:
		r.dropped.Add(1)
	}
}

func (r *Reporter) write(item outbound) error {
	payload, err := json.Marshal(item.value)
	if err != nil {
		return err
	}
	for {
		deadline := time.Now().Add(reporterWriteTimeout)
		if !item.deadline.IsZero() && item.deadline.Before(deadline) {
			deadline = item.deadline
		}
		if err := r.connection.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if _, err = r.connection.Write(payload); err == nil {
			return nil
		}
		if item.result == nil || !retryableWriteError(err) || !time.Now().Before(item.deadline) {
			return err
		}
		time.Sleep(writeRetryDelay)
	}
}

func retryableWriteError(err error) bool {
	return errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

func newID() (string, error) {
	return newIDFrom(rand.Reader)
}

func newIDFrom(reader io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return "", fmt.Errorf("generate telemetry identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
