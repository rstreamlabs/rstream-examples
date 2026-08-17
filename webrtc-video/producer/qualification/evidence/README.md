# Published streaming evidence

Each directory contains a qualification pack tied to one source revision. The
pack keeps the evidence needed to reproduce and review the result without the
multi-megabyte diagnostic logs generated during execution:

- `manifest.json` identifies the source tree, container images, media profile,
  path, protection mode, and impairment schedule;
- `metrics.csv` contains the one-second time series used by the analyzer;
- `summary.json` exposes every machine-readable assertion;
- `summary.md` provides the complete human-readable report;
- `adaptive-bitrate.svg` aligns applied link capacity with the TWCC estimate,
  encoder target, and received bitrate;
- `network-conditions.svg` records the applied capacity, delay, jitter, and
  random-loss transitions on the metrics collector clock;
- `playback-quality.svg` shows decoded frame rate, frozen intervals, and H.264
  quantization against their acceptance limits;
- `transport-evidence.svg` aligns RTT, buffering, sender queues, NACK/RTX,
  FlexFEC, and observed loss with the same phase timeline.

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
