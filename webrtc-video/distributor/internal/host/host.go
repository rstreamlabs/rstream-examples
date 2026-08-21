package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/telemetry"
)

var ErrChildShutdownTimeout = errors.New("MediaMTX did not stop before the shutdown deadline")

type Options struct {
	Command         []string
	Environment     []string
	MetricsAddress  string
	SocketPath      string
	ShutdownTimeout time.Duration
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

type componentResult struct {
	name string
	err  error
}

func Run(ctx context.Context, options Options) error {
	if err := validate(options); err != nil {
		return err
	}
	collector, err := telemetry.NewCollector(options.SocketPath)
	if err != nil {
		return fmt.Errorf("start adapter telemetry collector: %w", err)
	}
	listener, err := net.Listen("tcp", options.MetricsAddress)
	if err != nil {
		result := fmt.Errorf("listen for adapter metrics: %w", err)
		if closeErr := collector.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			result = errors.Join(result, fmt.Errorf("stop telemetry collector: %w", closeErr))
		}
		return result
	}
	server := &http.Server{
		Handler:           collector.Handler(),
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	componentCtx, cancelComponents := context.WithCancel(context.Background())
	defer cancelComponents()
	components := make(chan componentResult, 2)
	go func() { components <- componentResult{name: "telemetry collector", err: collector.Run(componentCtx)} }()
	go func() { components <- componentResult{name: "metrics server", err: server.Serve(listener)} }()
	command := exec.Command(options.Command[0], options.Command[1:]...)
	command.Env = replaceEnvironment(options.Environment, telemetry.SocketEnvironmentVariable, options.SocketPath)
	command.Stdin = options.Stdin
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return errors.Join(fmt.Errorf("start MediaMTX: %w", err), shutdownComponents(cancelComponents, collector, server, options.ShutdownTimeout))
	}
	childResult := make(chan error, 1)
	go func() { childResult <- command.Wait() }()
	var result error
	childFinished := false
	select {
	case err := <-childResult:
		childFinished = true
		if err == nil {
			result = errors.New("MediaMTX stopped unexpectedly")
		} else {
			result = fmt.Errorf("MediaMTX exited: %w", err)
		}
	case <-ctx.Done():
		result = ctx.Err()
	case component := <-components:
		if !expectedComponentStop(component.err) {
			result = fmt.Errorf("%s failed: %w", component.name, component.err)
		} else {
			result = fmt.Errorf("%s stopped unexpectedly", component.name)
		}
	}
	if !childFinished {
		if err := stopProcessGroup(command, childResult, options.ShutdownTimeout); err != nil {
			result = errors.Join(result, err)
		}
	}
	return errors.Join(result, shutdownComponents(cancelComponents, collector, server, options.ShutdownTimeout))
}

func validate(options Options) error {
	if len(options.Command) == 0 || strings.TrimSpace(options.Command[0]) == "" {
		return errors.New("MediaMTX command is required")
	}
	if strings.TrimSpace(options.MetricsAddress) == "" || strings.TrimSpace(options.SocketPath) == "" {
		return errors.New("adapter telemetry endpoints are required")
	}
	if options.ShutdownTimeout <= 0 {
		return errors.New("MediaMTX shutdown timeout must be positive")
	}
	return nil
}

func stopProcessGroup(command *exec.Cmd, childResult <-chan error, timeout time.Duration) error {
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal MediaMTX process group: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-childResult:
		return nil
	case <-timer.C:
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return errors.Join(ErrChildShutdownTimeout, fmt.Errorf("kill MediaMTX process group: %w", err))
	}
	killTimer := time.NewTimer(timeout)
	defer killTimer.Stop()
	select {
	case <-childResult:
		return ErrChildShutdownTimeout
	case <-killTimer.C:
		return errors.Join(ErrChildShutdownTimeout, errors.New("MediaMTX process group did not reap after SIGKILL"))
	}
}

func shutdownComponents(cancel context.CancelFunc, collector *telemetry.Collector, server *http.Server, timeout time.Duration) error {
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)
	defer shutdownCancel()
	var result error
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		result = fmt.Errorf("stop metrics server: %w", err)
	}
	if err := collector.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		result = errors.Join(result, fmt.Errorf("stop telemetry collector: %w", err))
	}
	return result
}

func expectedComponentStop(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

func replaceEnvironment(environment []string, key string, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
