package telemetry

import "time"

const protocolVersion = 1

const SocketEnvironmentVariable = "RSTREAM_DISTRIBUTOR_TELEMETRY_SOCKET"

const (
	StateActive  = "active"
	StateBackoff = "backoff"
	StateIdle    = "idle"
	StateStopped = "stopped"
)

const (
	OutcomeCanceled  = "canceled"
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
	OutcomePermanent = "permanent"
)

type Counters struct {
	Received                        uint64 `json:"received"`
	RTXReceived                     uint64 `json:"rtxReceived"`
	SourceFEC                       uint64 `json:"sourceFec"`
	FECCandidates                   uint64 `json:"fecCandidates"`
	Duplicates                      uint64 `json:"duplicates"`
	DuplicateRTX                    uint64 `json:"duplicateRtx"`
	DuplicateFEC                    uint64 `json:"duplicateFec"`
	RepairedRTX                     uint64 `json:"repairedRtx"`
	RepairedFEC                     uint64 `json:"repairedFec"`
	LateRTX                         uint64 `json:"lateRtx"`
	LateFEC                         uint64 `json:"lateFec"`
	ReorderLate                     uint64 `json:"reorderLate"`
	ReorderSkipped                  uint64 `json:"reorderSkipped"`
	Discontinuities                 uint64 `json:"discontinuities"`
	KeyFrameRequests                uint64 `json:"keyFrameRequests"`
	KeyFrameRequestsCoalesced       uint64 `json:"keyFrameRequestsCoalesced"`
	DamagedSourceFramesDropped      uint64 `json:"damagedSourceFramesDropped"`
	DamagedSourcePacketsDropped     uint64 `json:"damagedSourcePacketsDropped"`
	ReorderDiscarded                uint64 `json:"reorderDiscarded"`
	InvalidFEC                      uint64 `json:"invalidFec"`
	NACKRequests                    uint64 `json:"nackRequests"`
	Expired                         uint64 `json:"expired"`
	SourceICERestarts               uint64 `json:"sourceIceRestarts"`
	SourceCredentialRefreshFailures uint64 `json:"sourceCredentialRefreshFailures"`
}

type message struct {
	Version                int      `json:"version"`
	ProcessID              string   `json:"processId"`
	AttemptID              string   `json:"attemptId,omitempty"`
	Sequence               uint64   `json:"sequence"`
	State                  string   `json:"state"`
	Outcome                string   `json:"outcome,omitempty"`
	Completed              bool     `json:"completed,omitempty"`
	RetryAfterMilliseconds int64    `json:"retryAfterMilliseconds,omitempty"`
	DroppedSnapshots       uint64   `json:"droppedSnapshots"`
	Counters               Counters `json:"counters"`
}

func validState(value string) bool {
	return value == StateActive || value == StateBackoff || value == StateIdle || value == StateStopped
}

func validOutcome(value string) bool {
	return value == OutcomeCanceled || value == OutcomeCompleted || value == OutcomeFailed || value == OutcomePermanent
}

func validRetryAfter(value int64) bool {
	return value >= 0 && value <= (24*time.Hour).Milliseconds()
}
