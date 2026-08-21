package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/bridge"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/repair"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/source"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/telemetry"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/whipwhep"
)

func TestPermanentAttemptFailureClassifiesEveryBoundary(t *testing.T) {
	cause := errors.New("failure")
	tests := []struct {
		err       error
		permanent bool
	}{
		{err: cause},
		{err: errors.Join(cause, bridge.ErrWorkerShutdownTimeout), permanent: true},
		{err: errors.Join(cause, source.Permanent(errors.New("resolver contract"))), permanent: true},
		{err: errors.Join(cause, whipwhep.Permanent(errors.New("signaling contract"))), permanent: true},
	}
	for index, test := range tests {
		if permanentAttemptFailure(test.err) != test.permanent {
			t.Fatalf("case %d permanent = %t, want %t", index, permanentAttemptFailure(test.err), test.permanent)
		}
	}
}

func TestTelemetryCountersPreserveEveryBoundedRepairValue(t *testing.T) {
	result := bridge.Result{
		Repair: repair.Stats{
			Received: 1, RTXReceived: 2, FECCandidates: 3, Duplicates: 4, DuplicateRTX: 5, DuplicateFEC: 6,
			RepairedRTX: 7, RepairedFEC: 8, LateRTX: 9, LateFEC: 10, ReorderLate: 11, ReorderDiscarded: 12,
			NACKRequests: 13, Expired: 14,
		},
		SourceFECPackets:                15,
		InvalidFEC:                      16,
		SourceICERestarts:               17,
		SourceCredentialRefreshFailures: 18,
	}
	want := telemetry.Counters{
		Received: 1, RTXReceived: 2, FECCandidates: 3, Duplicates: 4, DuplicateRTX: 5, DuplicateFEC: 6,
		RepairedRTX: 7, RepairedFEC: 8, LateRTX: 9, LateFEC: 10, ReorderLate: 11, ReorderDiscarded: 12,
		NACKRequests: 13, Expired: 14, SourceFEC: 15, InvalidFEC: 16, SourceICERestarts: 17, SourceCredentialRefreshFailures: 18,
	}
	if got := telemetryCounters(result); got != want {
		t.Fatalf("telemetry counters = %+v, want %+v", got, want)
	}
}

func TestTelemetryOutcomeClassifiesCancellationAndPermanentFailures(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{want: telemetry.OutcomeCompleted},
		{err: context.Canceled, want: telemetry.OutcomeCanceled},
		{err: errors.New("temporary"), want: telemetry.OutcomeFailed},
		{err: bridge.ErrWorkerShutdownTimeout, want: telemetry.OutcomePermanent},
	} {
		if got := telemetryOutcome(test.err); got != test.want {
			t.Fatalf("telemetry outcome for %v = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestRunRejectsUnknownCommands(t *testing.T) {
	err := run(context.Background(), []string{"unknown"}, slog.Default())
	if err == nil {
		t.Fatal("unknown command was accepted")
	}
}
