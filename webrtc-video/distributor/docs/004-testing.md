# Test the distributor

Return to the [distributor overview](../README.md) for the application path. Run commands from `webrtc-video/distributor`, unless a step changes directory.

## Run local tests

Install Go 1.27.4 or newer and Git. Run the adapter unit tests.

```sh
make test
```

Install Python 3 for the local WebRTC integration suite. Build the pinned MediaMTX binary into a temporary working directory and pass its path to the Go tests.

```sh
output_directory=$(mktemp -d)
make mediamtx OUT="$output_directory"
RSTREAM_MEDIAMTX_BINARY="$output_directory/mediamtx" \
GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod" \
  go test -race -tags=integration ./internal/bridge
```

`make test` prepares the patched Go module used by the integration command. The integration suite uses loopback traffic. With Docker and Compose, `make integration` also builds and smoke-tests the combined image. Set `RSTREAM_MEDIAMTX_BINARY` to the pinned executable above for that command too.

Install Node.js 24 with npm and FFmpeg with `libx264` for `make verify`. That target runs formatting, vet, analyzer and race tests, checks decoded restart and loss recovery, and builds the adapter.

The integration suite starts the real MediaMTX 1.21.3 binary. It proves that two
viewers create one source WHEP session, injects and repairs a missing H.264 RTP
packet with FlexFEC, then repeats with the first FEC packet suppressed and
requires RTX recovery. Every viewer must receive the complete ordered range.
The source offer and both viewer sessions must also negotiate transport-wide
congestion control, and the source harness must receive TWCC feedback.
The suite also reads the live MediaMTX path metrics, closes the source after the
last viewer, restarts without stale state, and recovers after a rejected source
negotiation. A separate native-pull test drops a primary packet, requires an
actual RTX retransmission and delivery to both readers, and locks the remaining
capability differences that make the two profiles explicit.

## Measure local fan-out

Run the fan-out profile with jq, Node.js and the same MediaMTX binary.

```sh
RSTREAM_MEDIAMTX_BINARY="$output_directory/mediamtx" \
  make qualify-fanout OUT="$output_directory"
```

Open `qualification/fanout/summary.json` and `qualification/fanout/fanout.svg` under the output directory. The fan-out qualification runs three independent passes with a real
constrained-baseline H.264 GOP at approximately 8 Mbit/s. It requires constant
device ingress, linear MediaMTX egress, identical payload delivery to all
readers, one source lifecycle, bounded process count, CPU and memory, and
source TWCC feedback. At the eight-reader limit it requires a fast rejection
of the ninth reader, uninterrupted delivery to the existing readers, and
twelve successful leave-and-replacement cycles. Any MediaMTX B-frame, packet
loss, source-timeout, or adapter-failure diagnostic fails the run rather than
being hidden behind aggregate byte counters.

## Technical qualification

Before running live QUIC qualification, record the Linux host’s current UDP buffer limits so you can restore them after the run. With Docker Desktop, run these commands in its Linux VM.

```sh
sysctl net.core.rmem_max net.core.wmem_max
```

Raise each limit below 7,500,000 bytes using its corresponding command.

```sh
sudo sysctl -w net.core.rmem_max=7500000
sudo sysctl -w net.core.wmem_max=7500000
```

After the run, use `sudo sysctl -w` with the values you recorded to restore any limit you changed. The performance gate requires sufficient UDP buffers even when video plays. See the [quic-go buffer guidance](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes).

The finite browser qualification needs Docker, Git, Go, jq, Node.js 24, rstream CLI 1.29.0 or newer, and an explicit rstream context. The runner builds its own browser image and enables edge authentication by default. Open `result.json` in the output directory for the verdict and individual gates.

```bash
RSTREAM_CONTEXT="your-qualification-context" \
RSTREAM_DISTRIBUTOR_MODE="mediamtx" \
qualification/end-to-end/run.sh /tmp/rstream-video-result
```

The runner identifies the exact temporary producer tunnel and project before
minting a WHEP-path-scoped connect token. The token remains in process memory
and the WHEP URL; result files record only that edge authentication was active.
Set `RSTREAM_CONTEXT` to a non-production context prepared for qualification. Use `RSTREAM_DISTRIBUTOR_MODE=direct` for the one-to-one reference, or `mediamtx-native` for the static native WHEP path. Keep edge authentication enabled for release qualification.

For a direct media diagnostic without an external control-plane dependency,
unset `RSTREAM_CONTEXT` and set `RSTREAM_DISTRIBUTOR_CONTROL_PATH=local`,
`RSTREAM_DISTRIBUTOR_MODE=direct` and `RSTREAM_DISTRIBUTOR_EDGE_AUTH=false`.
No rstream CLI configuration or credentials are read. The producer stays on an
isolated Docker bridge with no published host ports; a qualification-only
proxy preserves the local quality API's loopback restriction and exposes only
WHEP and read-only observations. Results explicitly record `controlPath: local`.
This diagnoses direct media independently; it does not qualify tunnels, TURN,
edge authentication or MediaMTX. Other modes reject this option.

For a separate activation and cancellation check, use
`RSTREAM_DISTRIBUTOR_STARTUP_CYCLES=true` with the same command and an empty
output directory. This adaptive MediaMTX check repeats three source-cold
activations, three immediate same-page reopens during the one-second
qualification idle grace, and three additional readers joining the active
source. Every warm operation must retain the same single upstream and encoder.
It then cancels setup after 0, 25, 100 and 250ms, confirms WHEP resource cleanup,
and observes source counters for another 18 seconds. Counters must remain zero
after the idle grace plus a two-second scheduling margin; an initial zero
cannot hide delayed activation. `startup-cycles.json` retains every attempted
case, frame callback and cancellation observation; `startup-manifest.json`
identifies the source and images. The runner has a four-minute browser deadline.
This mode does not combine with recording or network/format phases, and does
not replace their cadence, quality, latency or CPU gates. Timings exclude
Next.js authorization and device process startup. The production idle grace
remains five seconds.

Phase changes are published atomically inside the browser container, with
an acknowledged write before the runner continues. The collector's live control
file does not depend on host bind-mount rename visibility. Its concurrent-reader
check requires only a Node container (or an existing qualification browser image):

```bash
qualification/end-to-end/phase-container-test.sh node:24-bookworm
```

The browser runner can apply capacity, delay, jitter, loss, and an explicit
playout target to the viewer leg. It correlates first-frame timing, decoded
frame rate, freezes, source OpenMetrics, packet repair, traffic-control
counters, recovery, and WHEP resource balance. Direct delivery must adapt the
producer target to a capacity step. The current single-rendition MediaMTX mode
is intentionally rejected when the viewer cannot sustain the source rate;
downstream feedback cannot create a lower rendition or control the shared
producer encoder.

The custom adapter has a separate causal test for its shared source leg. These
variables shape UDP after it leaves the producer and only when its destination
is the adapter:

```bash
RSTREAM_CONTEXT="your-qualification-context" \
RSTREAM_DISTRIBUTOR_MODE="mediamtx" \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS="5000" \
qualification/end-to-end/run.sh /tmp/rstream-video-source-capacity
```

Use `RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT` for repair testing; delay,
jitter, and queue controls use the same `SOURCE_` prefix. Source and viewer
impairment cannot be enabled in one run because that would make the observed
reaction causally ambiguous. The result records the selected network
namespace, destination, traffic-control counters, TWCC response, encoder
target, RTX/FlexFEC repair, decoded frame rate, freezes, and recovery.

To qualify automatic source resolution and frame-rate changes, use the optional
format profile and name the expected format during the constrained phase:

```bash
RSTREAM_CONTEXT="your-qualification-context" \
RSTREAM_DISTRIBUTOR_MODE="mediamtx" \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS="1500" \
RSTREAM_DISTRIBUTOR_EXPECT_FORMAT="small" \
qualification/end-to-end/run.sh /tmp/rstream-video-automatic-format
```

This selects a qualification-only 720p30 / 540p24 / 360p15 ladder, measures
45-second baseline and capacity phases, then allows 90 seconds for recovery.
For direct delivery, use `MODE=direct` and `VIEWER_CAPACITY_KBPS` with the same
prefix. Native MediaMTX and downstream-only MediaMTX shaping cannot qualify a
shared adaptive source. The public provisioning example remains unchanged.

The `Video qualification` workflow also provides
`automatic_format_comparison=true`. With `automatic_format_delivery=direct`, it
compares one-second and three-second down-holds in 1s / 3s / 3s / 1s / 1s / 3s
order on one hosted Linux runner. With `automatic_format_delivery=mediamtx`, it
repeats the public three-second default three times through the custom adapter.
The report requires identical producer/browser image digests across trials,
plus the distributor image for MediaMTX. Other workflow inputs apply to the
usual fixed-format qualification and are unused here. This job needs no secrets.
It fixes the source
ladder, 1.5 Mbit/s capacity, zero injected loss/delay and zero playout-delay hint,
retains every attempted result and restores the runner's socket limits. No
production default changes as a side effect. Its local equivalent, after the
host's socket-limit preparation, is:

```bash
../producer/qualification/adaptive-streaming/formats/compare-delivery.sh paired /tmp/video-formats-paired
../producer/qualification/adaptive-streaming/formats/compare-delivery.sh 3s /tmp/video-formats-mediamtx mediamtx
```

Use `1s` or `3s` instead of `paired` to run only three trials of one setting.
Any failed delivery gate fails the aggregate comparison; completed failures are
retained and the remaining trials still run. An incomplete setup stops the series.

This isolated mode exercises media without the rstream control plane. Its
qualification-only proxy exposes WHEP and read-only observations on an unpublished
Docker port. MediaMTX uses a second proxy on its own loopback interface, preserving
the production adapter's HTTPS requirement for remote sources. Producer and
adapter retain separate network namespaces, so shaping targets the actual media
hop. These results do not measure authenticated tunnel or dashboard startup.

The collector reads the existing quality API with a separate, short-lived
path-scoped credential that stays in a private file outside the evidence.
`source-formats.json` compares confirmed encoder caps with actual decoded
dimensions, measures cadence against the observed format, checks dwell time
within the reported sampling uncertainty, and requires return to the initial
format without another peer connection or WHEP session. Requested caps alone
cannot pass. Existing freeze, dropped-frame, bitrate, recovery and resource
checks remain active. Media-time continuity is sampled; this observation does
not establish every-frame timestamp continuity or physical capture latency.
`RSTREAM_DISTRIBUTOR_QUALITY_OBSERVER=true` enables just the API observations
for other profiles that expose quality modes. Combined format/latency runs
require an explicit profile with the pixel marker after format selection.

For pixel-based latency measurement on a shared Linux host, add
`RSTREAM_DISTRIBUTOR_LATENCY_PROBE=true`. The optional
`RSTREAM_DISTRIBUTOR_PRODUCER_CONFIG` selects a custom qualification profile;
its digest is retained in the report. See the
[measurement scope and clock/marker gates](../../producer/qualification/adaptive-streaming/README.md#optional-pixel-based-latency-measurement)
before interpreting these results. The probe is disabled by default and does
not change production pipelines or the live player's buffering policy.

For a bounded recording-cost comparison, add
`RSTREAM_DISTRIBUTOR_RECORDING=true` in either MediaMTX mode. The qualification
records fMP4 with one-second parts, five-second segments and five-minute retention
in a disposable 512 MiB tmpfs. `recording.json` reports the resulting segment
count and bytes; an enabled run with no recorded data fails. This measures
recording during live viewing; playback and storage-fault behavior have separate
platform integration checks. Direct delivery rejects this option.

`resources.json` retains whole-run totals and also groups CPU, memory and task
counts by phase. A sample spanning a phase change stays in whole-run totals but
is excluded from phase comparisons, including when the change happens during
the process-memory fallback read. Compare matching `phases.baseline` windows,
source profiles and images when measuring probe or recording overhead. UTC
sample times provide context; they are not used to calculate CPU utilization.
Memory is labeled by measurement source: process PSS/RSS excludes the recording
tmpfs, whose stored bytes are reported separately. These are host-specific
measurements, not hardware-independent resource guarantees.
