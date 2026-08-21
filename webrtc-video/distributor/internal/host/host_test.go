package host

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/telemetry"
)

func TestRunHostsMetricsAndStopsTheCompleteProcessGroup(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	readyPath := filepath.Join(directory, "ready")
	stoppedPath := filepath.Join(directory, "stopped")
	socketPath := filepath.Join(directory, "metrics.sock")
	metricsAddress := unusedAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, helperOptions("graceful", readyPath, stoppedPath, socketPath, metricsAddress, time.Second))
	}()
	eventually(t, func() bool { return fileContains(readyPath, socketPath) })
	eventually(t, func() bool {
		response, err := http.Get("http://" + metricsAddress + "/metrics")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return err == nil && strings.Contains(string(body), `rstream_video_distributor_children{state="active"} 1`) && strings.Contains(string(body), `rstream_video_distributor_source_packets_total{kind="media"} 5`)
	})
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("host result = %v, want cancellation", err)
	}
	if !fileContains(stoppedPath, "interrupt") {
		t.Fatal("MediaMTX helper did not observe graceful process-group shutdown")
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("telemetry socket survived shutdown: %v", err)
	}
}

func TestRunReturnsChildFailure(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	err := Run(context.Background(), helperOptions("exit", filepath.Join(directory, "ready"), "", filepath.Join(directory, "metrics.sock"), unusedAddress(t), time.Second))
	if err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("host result = %v, want MediaMTX exit status", err)
	}
}

func TestRunRejectsUnexpectedCleanChildExit(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	err := Run(context.Background(), helperOptions("clean-exit", filepath.Join(directory, "ready"), "", filepath.Join(directory, "metrics.sock"), unusedAddress(t), time.Second))
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("host result = %v, want unexpected MediaMTX stop", err)
	}
}

func TestRunKillsAProcessGroupThatIgnoresGracefulShutdown(t *testing.T) {
	directory := shortTemporaryDirectory(t)
	readyPath := filepath.Join(directory, "ready")
	options := helperOptions("ignore", readyPath, "", filepath.Join(directory, "metrics.sock"), unusedAddress(t), 50*time.Millisecond)
	options.Command = []string{"/bin/sh", "-c", "trap '' INT TERM; printf ignore > " + readyPath + "; while :; do sleep 1; done"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options)
	}()
	eventually(t, func() bool { return fileContains(readyPath, "ignore") })
	cancel()
	if err := <-done; !errors.Is(err, ErrChildShutdownTimeout) {
		t.Fatalf("host result = %v, want shutdown timeout", err)
	}
}

func TestHostHelperProcess(t *testing.T) {
	if os.Getenv("RSTREAM_HOST_TEST_HELPER") != "1" {
		return
	}
	mode := os.Getenv("RSTREAM_HOST_TEST_MODE")
	readyPath := os.Getenv("RSTREAM_HOST_TEST_READY")
	stoppedPath := os.Getenv("RSTREAM_HOST_TEST_STOPPED")
	if mode == "exit" {
		os.Exit(7)
	}
	if mode == "clean-exit" {
		return
	}
	if mode == "ignore" {
		signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
	}
	var reporter *telemetry.Reporter
	var attempt *telemetry.Attempt
	if mode == "graceful" {
		var err error
		reporter, err = telemetry.NewReporter(os.Getenv(telemetry.SocketEnvironmentVariable))
		if err != nil {
			os.Exit(10)
		}
		attempt, err = reporter.BeginAttempt(context.Background())
		if err != nil {
			os.Exit(11)
		}
		attempt.Observe(telemetry.Counters{Received: 5})
	}
	if err := os.WriteFile(readyPath, []byte(os.Getenv(telemetry.SocketEnvironmentVariable)+"\n"+mode), 0o600); err != nil {
		os.Exit(8)
	}
	if mode == "ignore" {
		select {}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	received := <-signals
	if err := attempt.Complete(context.Background(), telemetry.Counters{Received: 5}, telemetry.OutcomeCanceled); err != nil {
		os.Exit(12)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(closeCtx); err != nil {
		os.Exit(13)
	}
	if err := os.WriteFile(stoppedPath, []byte(received.String()), 0o600); err != nil {
		os.Exit(9)
	}
}

func helperOptions(mode string, readyPath string, stoppedPath string, socketPath string, metricsAddress string, timeout time.Duration) Options {
	environment := append(os.Environ(),
		"RSTREAM_HOST_TEST_HELPER=1",
		"RSTREAM_HOST_TEST_MODE="+mode,
		"RSTREAM_HOST_TEST_READY="+readyPath,
		"RSTREAM_HOST_TEST_STOPPED="+stoppedPath,
	)
	return Options{
		Command:         []string{os.Args[0], "-test.run=TestHostHelperProcess"},
		Environment:     environment,
		MetricsAddress:  metricsAddress,
		SocketPath:      socketPath,
		ShutdownTimeout: timeout,
		Stdout:          io.Discard,
		Stderr:          io.Discard,
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate metrics address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release metrics address: %v", err)
	}
	return address
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

func fileContains(path string, expected string) bool {
	value, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(value), expected)
}

func shortTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "rstream-video-host-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
