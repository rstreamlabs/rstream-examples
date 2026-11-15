# Adapter lifecycle and resource limits

Return to the [distributor overview](../README.md) for the application path. Run commands from `webrtc-video/distributor`, unless a step changes directory.

## Process and failure model

MediaMTX owns demand. One path creates at most one adapter, regardless of the
number of readers. A source or destination failure ends the current attempt,
closes both peer connections, and releases its HTTP media resources. While the
same demand remains active, the adapter resolves fresh credentials and retries
after a jittered exponential delay bounded between one and 15 seconds. Thirty
seconds of stable forwarding resets that delay. This keeps recovery inside one
MediaMTX-owned process instead of enabling an uncontrolled external restart
loop. Network failures, an offline source, throttling, and server errors are
retryable. Rejected credentials, invalid resolver or WHEP/WHIP contracts, and
local resource invariants stop the process immediately so a broken deployment
cannot hammer the control plane. `SIGINT`/`SIGTERM` interrupts both forwarding
and backoff immediately.

After WHIP signaling, the adapter waits up to ten seconds for the destination
media transport to connect before forwarding RTP or requesting the initial
source key frame. This preserves the initial H.264 parameter sets that a
not-yet-connected sender can otherwise discard. Cancellation or connection
failure closes the newly created session; no extra live buffering is added.

MediaMTX allows two seconds after connection for the first inbound media track.
This deadline starts before the first RTP packet, so it must accommodate source
startup even when the transport is already connected. Discovery completes as
soon as all offered tracks arrive; this is not a fixed delay for every reader.
A publisher that sends no media is closed when that bounded deadline expires.

The forwarding pipeline has bounded packet, repair, and worker queues. It
reorders media for at most 300 ms, retries NACK feedback at a bounded cadence,
expires missing packets after one second, and stops instead of accumulating an
unbounded live-stream backlog. After repair, H.264 packets are held until the
access unit's RTP marker, with limits of one picture, 8 MiB, 8192 packets and
500 ms of assembly time. This waits for the current picture to finish arriving,
without adding a playout delay or transcoding. An incomplete or expired picture
is discarded in full; dependent pictures are discarded until a complete IDR
starting at its first slice arrives. A slice whose NALU type is IDR is not by
itself a complete recovery point. Sources must mark access-unit boundaries;
markerless camera profiles are not supported by this distribution path.
Transport integrity remains distinct from codec validity and downstream loss.
PLI and FIR received from MediaMTX cross back to
the source; viewer NACK and TWCC remain local to the MediaMTX hop. This is not
end-to-end forwarding of every viewer's key-frame request: MediaMTX 1.21.3
[consumes reader RTCP without forwarding PLI upstream](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/protocols/webrtc/outbound_track.go)
and [generates its own periodic source PLI every two seconds](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/protocols/webrtc/inbound_track.go).
The combined image therefore uses MediaMTX's standard `runOnRead` hook to
request a source key frame when a reader's media transport connects. A short
local command sends a fixed notification to that path's adapter through a
private Unix socket. Concurrent arrivals retain at most one pending request
per 250ms; the producer also preserves one deferred request inside its own
rate limit. Neither source bitrate nor GOP interval changes. Existing readers
continue on the same live upstream, and the last reader still releases it after
`runOnDemandCloseAfter` (five seconds in the reference configuration). That idle
grace trades briefly continued encoding against avoiding source recreation on
rapid reopen; reduce it when immediate resource release matters more.

Stopping the shared browser client during its initial WHEP POST retains that
request only within its existing close budget, so a returned session URL can
be deleted promptly. Aborting the POST before its response would lose the only
resource identifier and leave MediaMTX's unfinished peer waiting for its
handshake timeout. An unreachable response or disappearing page can still
require that server timeout; the client reports unconfirmed cleanup explicitly.

The shared browser WHEP client reports signaling, response-body and ICE-gathering
deadlines as `WHEPTimeoutError` (`name: "TimeoutError"`), with `phase` set to
`request`, `response` or `gathering`. Intentional cancellation retains the
original abort reason. When cancellation and a deadline compete, the first
cause wins; response readers are canceled and released without waiting for an
unresponsive underlying cancellation hook.

ICE restarts retain candidates gathered while the restart HTTP exchange is
pending, then send them using the accepted generation's ETag. The previous
Pion gathering callback sequence must complete before a new generation starts;
failed generations are discarded and all waits remain cancelable. The existing
64-candidate and 64 KiB per-generation bounds are unchanged. There is no fixed
playback duration in this mechanism: expiring TURN credentials require renewed
ICE connectivity, not a new media peer for every renewal.

`make verify` requires `ffmpeg` and includes a local TURN qualification that
decodes a deterministic H264 GOP across three successful ICE restarts, following
a rejected and a canceled attempt. It checks relay selection, continued decoded
frames, peer reuse and zero retained TURN allocations after cleanup. To extend
this to roughly twelve minutes of actual decoding, run:

```sh
./ice-patch.sh
GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod" RSTREAM_RESTART_PHASE_SECONDS=240 go test -race -tags=qualification \
  -timeout=15m -count=1 -v ./internal/whipwhep \
  -run '^TestICERestartDecodesH264ThroughTURN$'
```

The local fixture uses fixed test TURN credentials and explicit restarts; it
qualifies media continuity and lifecycle behavior, not a deployed credential
issuer or browser/hardware decoder.

The `host` command creates and removes the private notification directory and
passes it to MediaMTX and its hooks. Path locks reject a duplicate live adapter
and allow a replacement to recover a socket left by a crashed process. Custom
MediaMTX configurations must retain both `runOnDemand` and `runOnRead` from the
bundled configuration to get this behavior. No public control endpoint or
permanent encoding session is added. Native MediaMTX pull does not use this
adapter hook and retains MediaMTX's periodic key-frame behavior.

First-picture timing is measured separately from steady-state latency;
reducing the source GOP interval also changes encoding cost and compression
efficiency, so it requires a measured tradeoff.

The reference configuration admits at most eight readers on one device path.
That boundary is deliberate: the fan-out qualification drives a decoder-valid
8 Mbit/s H.264 stream through one, four, and eight viewers, rejects a ninth
viewer without interrupting the existing eight, then replaces readers under
churn while retaining one source. Override `MTX_PATHDEFAULTS_MAXREADERS` only
after repeating the same qualification on the target host and network. This is
a per-path guard, not a global instance capacity policy; a multi-tenant pool
still needs a measured aggregate admission boundary.
