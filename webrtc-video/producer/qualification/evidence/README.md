# Published streaming evidence

Each directory contains a qualification pack tied to one source revision. The
pack keeps the evidence needed to reproduce and review the result without the
multi-megabyte diagnostic logs generated during execution:

- `manifest.json` identifies the source tree, container images, media profile,
  path, protection mode, and impairment schedule;
- `metrics.csv` contains the one-second time series used by the analyzer;
- `summary.json` exposes every machine-readable assertion;
- `summary.md` provides the complete human-readable report;
- `adaptive-bitrate.svg` plots the encoder, congestion-controller, and received
  bitrate across the test phases.

The current reference pack is [`6706cfd`](./6706cfd/report.md). Its direct,
rstream relay, and QUIC/ICE mobility runs all use the same producer source and
pinned browser image.

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
