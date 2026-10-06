package readnotify

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func privateDirectory(t *testing.T) string {
	t.Helper()
	// macOS's testing temporary root can exceed the Unix socket pathname limit.
	directory, err := os.MkdirTemp("/tmp", "read-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestNotificationsArePrivatePathScopedAndBounded(t *testing.T) {
	directory := privateDirectory(t)
	first, err := Listen(directory, "devices/first")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Listen(directory, "devices/second")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if duplicate, err := Listen(directory, "devices/first"); err == nil {
		_ = duplicate.Close()
		t.Fatal("duplicate owner stole an active path")
	}
	if err := Notify(context.Background(), directory, "devices/second"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.requests:
	case <-time.After(time.Second):
		t.Fatal("notification did not reach its adapter")
	}
	if len(first.requests) != 0 {
		t.Fatal("notification crossed paths")
	}
	if info, err := os.Stat(second.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket is not private: %v %v", info, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Notify(ctx, directory, "devices/first"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hook = %v", err)
	}
	if err := Notify(context.Background(), directory, "missing"); err == nil {
		t.Fatal("missing adapter was reported as notified")
	}
}

func TestInvalidNotificationsAreIgnoredAndCloseJoinsTheReader(t *testing.T) {
	directory := privateDirectory(t)
	listener, err := Listen(directory, "camera")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection, err := net.Dial("unixgram", listener.path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, payload := range [][]byte{{2}, {1, 0}, {1, 0, 0}, {1}} {
		if _, err := connection.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-listener.requests:
	case <-time.After(time.Second):
		t.Fatal("valid notification was lost")
	}
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			if err := listener.Close(); err != nil {
				t.Errorf("concurrent close: %v", err)
			}
		})
	}
	wait.Wait()
	if len(listener.requests) != 0 {
		t.Fatal("invalid datagram was accepted")
	}
	if _, err := os.Lstat(listener.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed adapter left its socket: %v", err)
	}
	replacement, err := Listen(directory, "camera")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
}

func TestStaleSocketDoesNotPreventAdapterRecovery(t *testing.T) {
	directory := privateDirectory(t)
	path, err := socketPath(directory, "camera")
	if err != nil {
		t.Fatal(err)
	}
	// A Unix datagram pathname survives an abruptly terminated process.
	stale, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(directory, "camera")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Notify(context.Background(), directory, "camera"); err != nil {
		t.Fatal(err)
	}
}

func TestKilledAdapterReleasesItsPathOwnership(t *testing.T) {
	directory := privateDirectory(t)
	command := exec.Command(os.Args[0], "-test.run=^TestReadNotificationOwnerProcess$")
	command.Env = append(os.Environ(), "RSTREAM_READ_OWNER_TEST="+directory)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for Notify(context.Background(), directory, "camera") != nil {
		if time.Now().After(deadline) {
			t.Fatal("child adapter did not bind its path")
		}
		time.Sleep(time.Millisecond)
	}
	if duplicate, err := Listen(directory, "camera"); err == nil {
		_ = duplicate.Close()
		t.Fatal("another process stole the active adapter path")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("adapter did not exit from the forced termination")
	}
	replacement, err := Listen(directory, "camera")
	if err != nil {
		t.Fatalf("dead adapter retained its path ownership: %v", err)
	}
	defer replacement.Close()
	if err := Notify(context.Background(), directory, "camera"); err != nil {
		t.Fatal(err)
	}
}

func TestReadNotificationOwnerProcess(t *testing.T) {
	directory := os.Getenv("RSTREAM_READ_OWNER_TEST")
	if directory == "" {
		return
	}
	listener, err := Listen(directory, "camera")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.Run(context.Background(), func() error { return nil })
}

func TestRejectsUnsafeNotificationDirectories(t *testing.T) {
	directory := privateDirectory(t)
	for _, candidate := range []string{"", ".", directory + strings.Repeat("/long", 30)} {
		if _, err := socketPath(candidate, "camera"); err == nil {
			t.Fatalf("accepted directory %q", candidate)
		}
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := socketPath(directory, "camera"); err == nil {
		t.Fatal("accepted a non-private directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := directory + "/link"
	if err := os.Symlink(directory, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := socketPath(symlink, "camera"); err == nil {
		t.Fatal("accepted a symbolic link")
	}
}

func TestRequestsCoalesceWithOneTrailingRequestAndCancelCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		requests := make(chan struct{}, 8)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		var calls atomic.Int64
		go func() { done <- run(ctx, requests, nil, nil, func() error { calls.Add(1); return nil }) }()
		requests <- struct{}{}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("first reader was delayed")
		}
		for range 8 {
			requests <- struct{}{}
		}
		synctest.Wait()
		time.Sleep(RequestInterval - time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("concurrent readers bypassed the rate limit")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("trailing requests = %d, want 2 total", calls.Load())
		}
		requests <- struct{}{}
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown = %v", err)
		}
		time.Sleep(2 * RequestInterval)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("trailing request survived cancellation")
		}
	})
}

func TestRequestAndListenerFailuresReachTheSupervisor(t *testing.T) {
	want := errors.New("transport stopped")
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	if err := run(context.Background(), requests, nil, nil, func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("request failure = %v", err)
	}
	stopped := make(chan struct{})
	close(stopped)
	if err := run(context.Background(), nil, stopped, func() error { return want }, nil); !errors.Is(err, want) {
		t.Fatalf("listener failure = %v", err)
	}
}
