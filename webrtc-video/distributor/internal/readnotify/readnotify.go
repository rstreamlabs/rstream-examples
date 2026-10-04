// Package readnotify connects MediaMTX's runOnRead hook to its path's adapter.
// Only a fixed notification crosses a private Unix socket; no credentials,
// network listener, media buffering or per-reader source session are involved.
package readnotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	DirectoryEnvironmentVariable = "RSTREAM_DISTRIBUTOR_READ_NOTIFY_DIRECTORY"
	RequestInterval              = 250 * time.Millisecond
	NotifyTimeout                = time.Second
	notification                 = byte(1)
)

type Listener struct {
	connection *net.UnixConn
	lock       *os.File
	path       string
	requests   chan struct{}
	done       chan struct{}
	readErr    error // published by closing done
	closeOnce  sync.Once
	closeErr   error
}

// Listen holds a process-owned lock before binding or removing a stale socket.
// A crashed adapter releases the lock in the kernel; a duplicate live owner
// cannot steal its notifications. The lock file stays until the host exits.
func Listen(directory, mediaPath string) (*Listener, error) {
	path, err := socketPath(directory, mediaPath)
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open reader notification lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("lock reader notification path: %w", err), lock.Close())
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(fmt.Errorf("remove stale reader notification socket: %w", err), lock.Close())
	}
	connection, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("listen for reader notifications: %w", err), lock.Close())
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(err, connection.Close(), os.Remove(path), lock.Close())
	}
	listener := &Listener{connection: connection, lock: lock, path: path, requests: make(chan struct{}, 1), done: make(chan struct{})}
	go listener.read()
	return listener, nil
}

func (l *Listener) read() {
	defer close(l.done)
	// An oversized datagram cannot be accepted as a valid one-byte message.
	var message [2]byte
	for {
		n, _, err := l.connection.ReadFromUnix(message[:])
		if err != nil {
			l.readErr = err
			return
		}
		if n != 1 || message[0] != notification {
			continue
		}
		select {
		case l.requests <- struct{}{}:
		default: // one pending key frame is sufficient for concurrent arrivals
		}
	}
}

// Run emits an immediate request and retains at most one trailing request per
// interval. Dropping that trailing request would strand a reader that joined
// just after the preceding key frame. There is no timer while idle.
func (l *Listener) Run(ctx context.Context, request func() error) error {
	return run(ctx, l.requests, l.done, func() error { return l.readErr }, request)
}

func run(ctx context.Context, requests <-chan struct{}, stopped <-chan struct{}, readError func() error, request func() error) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var due <-chan time.Time
	var lastRequest time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stopped:
			return fmt.Errorf("reader notification listener stopped: %w", readError())
		case <-requests:
			if due != nil {
				continue
			}
			if delay := time.Until(lastRequest.Add(RequestInterval)); delay > 0 {
				timer = time.NewTimer(delay)
				due = timer.C
				continue
			}
		case <-due:
			due = nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := request(); err != nil {
			return fmt.Errorf("request reader key frame: %w", err)
		}
		lastRequest = time.Now()
	}
}

func (l *Listener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closeErr = l.connection.Close()
		<-l.done
		l.closeErr = errors.Join(l.closeErr, os.Remove(l.path), l.lock.Close())
	})
	return l.closeErr
}

// Notify is a bounded, one-shot hook. MediaMTX starts it only after the reader
// transport connects and interrupts it if that reader leaves during the call.
func Notify(ctx context.Context, directory, mediaPath string) error {
	path, err := socketPath(directory, mediaPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, NotifyTimeout)
	defer cancel()
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unixgram", path)
	if err != nil {
		return fmt.Errorf("connect reader notification: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = connection.Write([]byte{notification})
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	return err
}

func socketPath(directory, mediaPath string) (string, error) {
	if !filepath.IsAbs(directory) || mediaPath == "" || len(mediaPath) > 512 {
		return "", errors.New("reader notification directory and media path are required")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect reader notification directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("reader notification directory must be private (0700) and not a symlink")
	}
	digest := sha256.Sum256([]byte(mediaPath))
	path := filepath.Join(directory, hex.EncodeToString(digest[:16])+".sock")
	if len(path) > 100 {
		return "", errors.New("reader notification socket path exceeds the portable Unix socket limit")
	}
	return path, nil
}
