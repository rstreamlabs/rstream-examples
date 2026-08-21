package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/bridge"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/host"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/source"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/supervisor"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/telemetry"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/whipwhep"
)

const (
	defaultTelemetrySocket    = "/tmp/rstream-video-distributor.sock"
	defaultMetricsAddress     = ":9999"
	defaultHostShutdown       = 8 * time.Second
	telemetryErrorLogInterval = 30 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	err := run(ctx, os.Args[1:], logger)
	if err != nil && (!errors.Is(err, context.Canceled) || errors.Is(err, host.ErrChildShutdownTimeout)) {
		logger.Error("video distributor failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, logger *slog.Logger) error {
	if len(arguments) > 0 && arguments[0] == "host" {
		return runHost(ctx, arguments[1:])
	}
	if len(arguments) > 0 {
		return errors.New("unsupported video distributor command")
	}
	return runDistributor(ctx, logger)
}

func runHost(ctx context.Context, command []string) error {
	socketPath := valueOrDefault(os.Getenv(telemetry.SocketEnvironmentVariable), defaultTelemetrySocket)
	metricsAddress := valueOrDefault(os.Getenv("RSTREAM_DISTRIBUTOR_METRICS_ADDRESS"), defaultMetricsAddress)
	return host.Run(ctx, host.Options{
		Command:         command,
		Environment:     os.Environ(),
		MetricsAddress:  metricsAddress,
		SocketPath:      socketPath,
		ShutdownTimeout: defaultHostShutdown,
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
	})
}

func runDistributor(ctx context.Context, logger *slog.Logger) (runErr error) {
	configuration, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid distributor configuration: %w", err)
	}
	reporter, err := telemetry.NewReporter(os.Getenv(telemetry.SocketEnvironmentVariable))
	if err != nil {
		logger.Warn("adapter telemetry is unavailable", "error", err)
		reporter = nil
	}
	reporterErrorsDone := monitorReporterErrors(logger, reporter.Errors())
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), defaultHostShutdown)
		defer cancel()
		runErr = errors.Join(runErr, reporter.Close(closeCtx))
		<-reporterErrorsDone
	}()
	logger.Info("starting video distributor", "path", configuration.Path)
	runErr = supervisor.Run(ctx, supervisor.DefaultPolicy(), func(attemptCtx context.Context) error {
		attempt, telemetryErr := reporter.BeginAttempt(context.Background())
		if telemetryErr != nil {
			logger.Warn("adapter attempt telemetry did not start", "error", telemetryErr)
		}
		result, attemptErr := bridge.RunObserved(attemptCtx, configuration, func(result bridge.Result) {
			attempt.Observe(telemetryCounters(result))
		})
		if telemetryErr := attempt.Complete(context.Background(), telemetryCounters(result), telemetryOutcome(attemptErr)); telemetryErr != nil {
			logger.Warn("adapter attempt telemetry did not complete", "error", telemetryErr)
		}
		logger.Info(
			"video distributor attempt stopped",
			"path", configuration.Path,
			"received", result.Repair.Received,
			"rtx_received", result.Repair.RTXReceived,
			"fec_received", result.SourceFECPackets,
			"fec_candidates", result.Repair.FECCandidates,
			"duplicate_media", result.Repair.Duplicates,
			"duplicate_rtx", result.Repair.DuplicateRTX,
			"duplicate_fec", result.Repair.DuplicateFEC,
			"repaired_rtx", result.Repair.RepairedRTX,
			"repaired_fec", result.Repair.RepairedFEC,
			"late_rtx", result.Repair.LateRTX,
			"late_fec", result.Repair.LateFEC,
			"reorder_late", result.Repair.ReorderLate,
			"reorder_skipped", result.Repair.ReorderSkipped,
			"discontinuities", result.Repair.Discontinuities,
			"key_frame_requests", result.Repair.KeyFrameRequests,
			"key_frame_requests_coalesced", result.Repair.KeyFrameRequestsCoalesced,
			"damaged_source_frames_dropped", result.DamagedSourceFramesDropped,
			"damaged_source_packets_dropped", result.DamagedSourcePacketsDropped,
			"reorder_discarded", result.Repair.ReorderDiscarded,
			"invalid_fec", result.InvalidFEC,
			"nack_requests", result.Repair.NACKRequests,
			"expired", result.Repair.Expired,
			"source_ice_restarts", result.SourceICERestarts,
			"source_credential_refresh_failures", result.SourceCredentialRefreshFailures,
		)
		if permanentAttemptFailure(attemptErr) {
			return supervisor.Permanent(attemptErr)
		}
		return attemptErr
	}, func(attemptErr error, retryAfter time.Duration) {
		if telemetryErr := reporter.Backoff(context.Background(), retryAfter); telemetryErr != nil {
			logger.Warn("adapter retry telemetry was not published", "error", telemetryErr)
		}
		logger.Warn("video distributor attempt failed", "path", configuration.Path, "error", attemptErr, "retry_after", retryAfter)
	})
	return runErr
}

func permanentAttemptFailure(err error) bool {
	return errors.Is(err, bridge.ErrWorkerShutdownTimeout) || source.IsPermanent(err) || whipwhep.IsPermanent(err)
}

func telemetryCounters(result bridge.Result) telemetry.Counters {
	return telemetry.Counters{
		Received:                        result.Repair.Received,
		RTXReceived:                     result.Repair.RTXReceived,
		SourceFEC:                       result.SourceFECPackets,
		FECCandidates:                   result.Repair.FECCandidates,
		Duplicates:                      result.Repair.Duplicates,
		DuplicateRTX:                    result.Repair.DuplicateRTX,
		DuplicateFEC:                    result.Repair.DuplicateFEC,
		RepairedRTX:                     result.Repair.RepairedRTX,
		RepairedFEC:                     result.Repair.RepairedFEC,
		LateRTX:                         result.Repair.LateRTX,
		LateFEC:                         result.Repair.LateFEC,
		ReorderLate:                     result.Repair.ReorderLate,
		ReorderSkipped:                  result.Repair.ReorderSkipped,
		Discontinuities:                 result.Repair.Discontinuities,
		KeyFrameRequests:                result.Repair.KeyFrameRequests,
		KeyFrameRequestsCoalesced:       result.Repair.KeyFrameRequestsCoalesced,
		DamagedSourceFramesDropped:      result.DamagedSourceFramesDropped,
		DamagedSourcePacketsDropped:     result.DamagedSourcePacketsDropped,
		ReorderDiscarded:                result.Repair.ReorderDiscarded,
		InvalidFEC:                      result.InvalidFEC,
		NACKRequests:                    result.Repair.NACKRequests,
		Expired:                         result.Repair.Expired,
		SourceICERestarts:               result.SourceICERestarts,
		SourceCredentialRefreshFailures: result.SourceCredentialRefreshFailures,
	}
}

func telemetryOutcome(err error) string {
	if errors.Is(err, context.Canceled) {
		return telemetry.OutcomeCanceled
	}
	if err == nil {
		return telemetry.OutcomeCompleted
	}
	if permanentAttemptFailure(err) {
		return telemetry.OutcomePermanent
	}
	return telemetry.OutcomeFailed
}

func monitorReporterErrors(logger *slog.Logger, reporterErrors <-chan error) <-chan struct{} {
	done := make(chan struct{})
	if reporterErrors == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		lastLogged := time.Time{}
		suppressed := 0
		for err := range reporterErrors {
			if time.Since(lastLogged) < telemetryErrorLogInterval {
				suppressed++
				continue
			}
			logger.Warn("adapter telemetry write failed", "error", err, "suppressed", suppressed)
			lastLogged = time.Now()
			suppressed = 0
		}
		if suppressed > 0 {
			logger.Warn("adapter telemetry write failures were suppressed", "count", suppressed)
		}
	}()
	return done
}

func valueOrDefault(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
