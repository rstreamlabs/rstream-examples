# Published streaming evidence

Each directory contains a qualification pack tied to one source revision. The
pack keeps the evidence needed to reproduce and review the result without the
multi-megabyte diagnostic logs generated during execution:

- `manifest.json` identifies the source tree, container images, media profile,
  path, protection mode, and impairment schedule;
- `metrics.csv` contains the one-second time series used by the analyzer;
- `summary.json` exposes every machine-readable assertion;
- `summary.md` provides the complete human-readable report;
- `adaptive-bitrate.svg` aligns available link capacity with the encoder target
  and received bitrate;
- `frame-rate.svg` shows browser-decoded frame rate;
- `packet-repair.svg` compares NACK requests with received retransmissions.

The matrix adds `comparison-frozen-time.svg`, which compares frozen playback on
the relay path with and without bounded FlexFEC.

The link transitions are timestamped when the qualification controller observes
each traffic-control event. Charts therefore use measured transition instants
instead of reconstructing them from nominal phase durations.

The current reference pack is [`ca8a308`](./ca8a308/report.md). Its comparison
matrix repeats the direct and rstream relay paths three times with NACK/RTX and
with the release NACK/RTX/FlexFEC profile. A separate QUIC/ICE mobility record
keeps the producer revision, browser image, and media profile fixed. Its
preserved time series was re-evaluated after a regression-tested analyzer
correction; the original and corrected verdicts are recorded explicitly. Runs
that miss a declared pre-transition condition are recorded separately and never
enter the comparison.

## Reproducing a pack

Run the harness from `webrtc-video/producer` with a CLI context that can create
the short-lived tunnel and TURN credentials:

```console
RSTREAM_CONTEXT=<context> \
RSTREAM_QUALIFICATION_PATH=direct \
RSTREAM_QUALIFICATION_PROTECTION=nack-rtx-flexfec \
RSTREAM_QUALIFICATION_PLAYOUT_DELAY_HINT_SECONDS=0.2 \
./qualification/adaptive-streaming/run.sh ./qualification/adaptive-streaming/.artifacts/direct
```

Use `RSTREAM_QUALIFICATION_PATH=relay` for the managed TURN path. Add
`RSTREAM_QUALIFICATION_MOBILITY=producer` to switch the producer to a new
interface and source address while the session is live. The mobility profile
forces QUIC for rstream signaling; the analyzer verifies the Trickle ICE
candidate, selected-pair change, peer continuity, and WebSocket continuity.

The harness rejects dirty source trees, incomplete telemetry, noisy media
hosts, malformed controller feedback, local socket loss, queue saturation,
late recovery, excessive freezes, and paths that do not match the requested
direct or relay policy.
