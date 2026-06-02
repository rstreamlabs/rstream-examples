# WebRTC Video Streaming

This example streams video from a device to a browser with WebRTC, publishes the viewer through an `rstream` HTTP tunnel, and uses the managed `rstream` STUN/TURN service for ICE connectivity.

The sample includes a complete device-side path: an embedded viewer UI, signaling on the same origin as the page, TURN credential bootstrap, H.264 and AV1 reference profiles, shared or per-viewer pipeline allocation, optional adaptive bitrate driven by TWCC/GCC, and Linux distribution builds that produce a standalone binary.

The process model is intentionally simple. One Go binary serves the viewer page locally, exposes the signaling WebSocket and TURN bootstrap endpoints, runs the GStreamer capture pipeline, and sends media with Pion. `rstream` provides the public entrypoint, tunnel authentication, tunnel reconnection, and TURN credential generation.

Treat this repository as a reference base rather than a fixed product. The profiles and build scripts are meant to be adapted to the capture device, encoder, authentication mode, and operational constraints of the deployment you actually want to run.

If you want a guided walkthrough of the architecture and the `rstream-go` integration, see the associated guide: [Build Device-to-Browser Video Streaming with WebRTC and rstream](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream).

## Architecture

The local HTTP server serves the embedded page and the small API surface the page needs: signaling, TURN bootstrap, and status endpoints. On the media side, a GStreamer pipeline produces H.264 or AV1 access units and passes them to a WebRTC sender built on top of Pion. `rstream-go` publishes that local server through an HTTP tunnel and keeps the public URL available.

That layout keeps signaling, TURN bootstrap, and viewer delivery on the same origin while avoiding any extra backend dedicated to this example.

## Requirements

Before running the example, install the `rstream` CLI, create a free `rstream` account, create a project, and select that project locally. The sample expects an active CLI context:

```bash
rstream login
rstream project use <project-endpoint> --default
```

For local development you need Go `1.26+`, a C compiler, `pkg-config`,
and a GStreamer installation that includes the development files and the
elements required by the selected pipeline. Node.js `20+` and npm are only
required when building the embedded local viewer UI with `make build`,
`make run`, or `make test`.

When using the Next.js platform provisioning profile, the producer does not
serve the embedded viewer UI. Use `make build-provisioning` for that mode; it
skips npm entirely and builds the binary with `web.viewer.enabled: false`
configs in mind.

The H.264 profiles use `videotestsrc`, `videoconvert`, `x264enc`, `h264parse`, and `appsink`. The AV1 profiles use `av1enc` and `av1parse` on top of the same structure.

### macOS

```bash
brew install go pkg-config gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad gst-plugins-ugly
```

Install Node.js only if you want the producer binary to serve the embedded
viewer UI:

```bash
brew install node
```

### Ubuntu / Debian

```bash
sudo apt update
sudo apt install -y \
  build-essential \
  golang \
  gstreamer1.0-plugins-bad \
  gstreamer1.0-plugins-base \
  gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-ugly \
  gstreamer1.0-tools \
  libgstreamer-plugins-base1.0-dev \
  libgstreamer1.0-dev \
  pkg-config
```

Install Node.js and npm only if you want the producer binary to serve the
embedded viewer UI:

```bash
sudo apt install -y nodejs npm
```

### Windows

Please run the sample inside WSL2. This repository does not ship a Windows-native distribution target for this example.

## Quick start

`config.h264.yaml` is the reference profile and the best place to start. It uses a test pattern source, H.264, and a fixed encoder bitrate. `config.av1.yaml` keeps the same overall architecture and switches the codec path to AV1.

```bash
cp config.h264.yaml config.yaml
make build
./webrtc-video-streaming -config ./config.yaml
```

If you want to start from AV1 instead:

```bash
cp config.av1.yaml config.yaml
```

When the tunnel is ready, the process prints the public URL:

```text
info  Public URL: https://xxxxxxxx.t.<cluster-domain>
```

Open that URL in a browser. The page loads from the tunnel origin, opens the signaling WebSocket on the same origin, requests TURN credentials from the local process, and starts playback once the WebRTC session is established.

The viewer page also includes an ICE path selector. `Auto` keeps the default behavior, `Direct` disables TURN on the browser side, and `Relay only` forces the browser to use TURN. That selector only affects the browser peer; it does not override `webrtc.useTurn` in the Go process.

If you want to run the same application locally without publishing a tunnel:

```bash
make run-local
```

That serves the viewer on `http://127.0.0.1:8080`.

## Reference profiles

The repository ships a small set of reference YAML files so you can start from known working configurations.

- `config.h264.yaml` and `config.av1.yaml` use a test-pattern source and are useful when you want to validate the WebRTC path itself.
- `config.provisioning.h264.yaml` keeps the H.264 media path and moves tunnel credentials and TURN credentials to a product API.
- `config.macos-webcam.h264.yaml` and `config.macos-webcam.av1.yaml` are the macOS webcam variants built around `avfvideosrc`.
- `config.raspberry-pi-camera.h264.yaml` and `config.raspberry-pi-camera.av1.yaml` are the Raspberry Pi variants built around `libcamerasrc`.
- The `.twcc-gcc.yaml` variants enable adaptive bitrate. The plain variants keep TWCC enabled but leave the encoder on a fixed target bitrate.

Use those files as starting points. On a real device you will often need to adjust the device index, resolution, frame rate, or encoder settings.

## Configuration

Start from one of the shipped profiles and adjust only the sections you need. That is a better fit for this example than building a full config file up front.

The configuration is split by responsibility:

- `server` controls the local HTTP listener.
- `web` controls whether the producer serves its local viewer.
- `tunnel` controls publication through `rstream`, edge authentication, provisioning, and tunnel reconnection.
- `turn` controls TURN credential lifetime.
- `webrtc` controls codec settings, interceptors, adaptive bitrate, and viewer limits.
- `media` controls the GStreamer pipeline itself and how pipelines are allocated across viewers.
- `logging` controls verbosity.

### Tunnel publication and authentication

`tunnel.enabled` decides whether the process publishes the local server through `rstream` or stays local-only.

`tunnel.transport.useQuic` controls the producer-to-rstream upstream session. The published tunnel remains a standard HTTP tunnel for the browser UI, signaling WebSocket, and API endpoints; this flag only changes how the Go producer connects to the rstream engine.

```yaml
tunnel:
  transport:
    useQuic: true
```

`tunnel.auth.token` and `tunnel.auth.rstream` decide which edge authentication policies the tunnel enforces. The producer never builds a second public URL with an embedded token. It logs only the published tunnel URL returned by `rstream`.

```yaml
tunnel:
  auth:
    token: false
    rstream: false
```

When token authentication is enabled, viewer tokens must be distributed by another trusted surface, such as your product API, the rstream dashboard, or an operator workflow. The device-side process does not leak its own client token into a shareable URL.

The shipped local profiles publish a public viewer URL by default so the sample behaves like a simple developer tunnel. Enable `token` or `rstream` authentication explicitly when the public viewer must be protected.

`tunnel.reconnect.enabled` controls what happens when the HTTP tunnel drops. If it is enabled, the process recreates the tunnel after `tunnel.reconnect.interval` and logs the new public URL. If it is disabled, a tunnel disconnect becomes a clean process exit.

### Remote provisioning

`config.provisioning.h264.yaml` is the product-integration profile used by the Next.js platform example. In that mode, the producer does not read a local rstream CLI context. It calls the product API configured under `tunnel.provisioning`, receives the short-lived rstream client configuration required to create one tunnel, and then creates that tunnel from those values.

```yaml
web:
  viewer:
    enabled: false
tunnel:
  auth:
    token: false
    rstream: false
  provisioning:
    mode: remote
    endpoint: ${API_URL}
    secret: ${DEVICE_SECRET}
```

`API_URL` and `DEVICE_SECRET` belong to the third-party product, not to rstream. The `RSTREAM_*` values stay on that product backend, where the app can issue scoped producer and viewer tokens.

When `tunnel.provisioning.mode` is `remote`, local tunnel auth is disabled in the producer config because the product API issues the scoped tunnel creation token. The producer always requests a token-authenticated HTTP tunnel in that mode, and the short-lived token issued by the product API enforces the exact tunnel creation policy. TURN credentials stay separate: the producer asks the product API for fresh TURN credentials whenever the WebRTC path needs them.

Build that provisioning binary without the embedded viewer UI:

```bash
make build-provisioning
```

That target is equivalent to `make build EMBEDDED_WEB=0`. If a binary built
that way is started with `web.viewer.enabled: true`, startup fails with a clear
configuration error because the viewer assets are intentionally absent.

### TURN and ICE

`turn.ttl` controls the lifetime of TURN credentials minted by the local process. `webrtc.useTurn` controls whether the Go peer itself uses the managed `rstream` TURN service. The browser can still be forced into direct or relay-only mode from the viewer page, but the default path keeps both peers on the same TURN service when relay is required.

The signaling path uses Trickle ICE: both peers exchange candidates as soon as they are discovered. If the selected network path disappears during playback, the browser keeps the same WebRTC session and sends a new offer with ICE restart enabled. The producer keeps the session open during that recovery window and only closes it if ICE does not reconnect.

### Codecs and media pipelines

`webrtc.video.mimeType` selects the codec advertised to the browser. The sample supports `video/H264` and `video/AV1`.

H.264 is the reference path and the better default when you want predictable live behavior across browsers and machines. The AV1 profiles are included because codec negotiation and transport behavior are worth testing too, but live AV1 capture remains more sensitive to machine and encoder characteristics.

On macOS webcam pipelines, keep `format=I420` before `av1enc`. That avoids format negotiation paths that are known to be unreliable for browser playback.

`media.pipeline` is passed directly to GStreamer through `gst_parse_launch`. If you add new elements to a profile, remember that the static Linux build must include those same elements. Any pipeline change that adds dependencies should therefore be reflected in `build-gstreamer-static-linux.sh`.

### Transport feedback and packet repair

`webrtc.interceptors` controls the feedback and recovery path.

`twcc` enables Transport-Wide Congestion Control feedback. `nack` enables packet-loss feedback. `rtx` enables retransmission payloads so those loss reports can actually be repaired. The reference profiles keep all three enabled because that combination is practical and broadly supported.

`flexFEC` stays off by default. It is available as an opt-in, but it adds overhead and is less generally useful than `TWCC + NACK + RTX`.

### Adaptive bitrate

`webrtc.adaptive` controls encoder bitrate adaptation. The current backend is `twcc-gcc`.

TWCC is Transport-Wide Congestion Control feedback from the browser. GCC is Google Congestion Control. In this sample, the transport estimate comes from the standard Pion TWCC/GCC path, and the application then applies bounded bitrate updates to the active `x264enc` or `av1enc` instance.

The backend is intentionally narrow. It changes bitrate only. It does not rebuild the pipeline, renegotiate resolution, or try to handle multiple viewers through one shared encoder. For that reason it is only valid when one feedback loop controls one encoder, which means either `media.mode: per-viewer` or `webrtc.maxViewers: 1`.

That is also a deliberate simplification of the sample. In a real deployment it can be useful to react not only on the encoder target, but also on the source itself by reducing frame rate, resolution, or capture profile. This example stops at encoder modulation because it keeps the media path readable and avoids pipeline rebuilds inside the reference implementation.

The main settings are:

- `webrtc.initialBitrateKbps`, which seeds the sender before the first TWCC reports arrive
- `webrtc.adaptive.enabled`, which turns adaptation on or off
- `webrtc.adaptive.backend`, which selects the backend
- `webrtc.adaptive.twccGCC.minBitrateKbps` and `maxBitrateKbps`, which define the allowed range
- `webrtc.adaptive.twccGCC.updateInterval`, which sets how often bitrate changes may be applied
- `webrtc.adaptive.twccGCC.changeThresholdPct`, `maxIncreasePct`, and `maxIncreaseStepKbps`, which control how aggressively the encoder target moves

With the reference settings, the sender starts at `5 Mbps` and may adapt within the `1.5–8 Mbps` range. The viewer page exposes the codec, the enabled recovery path, the adaptive backend state, the current TWCC target, and the current encoder target so you can validate the behavior without immediately jumping into browser internals.

### Viewer limits and pipeline allocation

`webrtc.maxViewers` limits how many viewers may connect at the same time. `0` means unlimited. Any positive value enforces a fixed limit and rejects extra viewers.

`media.mode` controls how GStreamer pipelines are allocated. `shared` keeps one pipeline alive while at least one viewer is connected. `per-viewer` creates one pipeline per viewer and tears it down when that viewer disconnects. In both modes, zero connected viewers means no running pipeline.

## Testing degraded connectivity

If you want to validate adaptive bitrate, shape the real device-side network path instead of only throttling page load. On Linux, `tc netem` is the most useful baseline because it affects the actual UDP media traffic.

Apply shaping on the interface that carries viewer traffic, for example `wlan0`:

```bash
sudo tc qdisc add dev wlan0 root netem delay 80ms 20ms loss 3% rate 2mbit
```

Tighten the path further:

```bash
sudo tc qdisc change dev wlan0 root netem delay 160ms 40ms loss 6% rate 1mbit
```

Remove the shaping afterwards:

```bash
sudo tc qdisc del dev wlan0 root netem
```

When adaptive bitrate is enabled, the useful signals on the viewer page are `TWCC target` and `Encoder target`. The transport estimate should move first, and the encoder target should then follow within the configured update interval.

## Build and distribution

For local development:

```bash
make build
make run
```

`make build` embeds the local viewer UI and therefore requires Node.js and npm.
For the Next.js platform provisioning profile, use the no-viewer build:

```bash
make build-provisioning
```

For a local-only run:

```bash
make run-local
```

For tests:

```bash
make test
```

The repository also ships Docker-based static packaging targets for Linux:

```bash
make dist-linux-amd64
make dist-linux-arm64
make dist
```

Artifacts are written to `dist/linux-amd64` and `dist/linux-arm64`.

Those targets build a static Linux binary linked against a statically packaged `gstreamer-full` toolchain. The Docker build compiles the GStreamer subset needed by the sample, including `x264`, `libaom`, the parsers, and the `appsink` path, then links the Go binary against that toolchain with `musl`.

The practical outcome is a standalone executable you can copy to a target machine without asking that machine to install the full GStreamer development stack first. In other words, `make dist` is the path you use when you want to build once and then copy the resulting binary to a remote device.

That static toolchain is defined in `build-gstreamer-static-linux.sh`. If you change the reference pipelines and introduce new elements or plugins, update that script as well. Otherwise the local development setup may keep working while the static distribution build silently stops matching the pipeline you intend to run.

## Troubleshooting

`make build`, `make build-provisioning`, and `make test` run a preflight check
before compiling Go. If `pkg-config` or the GLib/GStreamer development files
are missing, the build prints the exact missing pkg-config packages and points
back to the install commands above.

If the process fails with `failed to create the GStreamer pipeline`, one or more configured elements are missing. Install the required plugins or adapt `media.pipeline` to the elements available on the target machine.

If the process cannot connect to the `rstream` engine server, verify the current CLI context with `rstream login` and `rstream project use <project-endpoint> --default`, then inspect `~/.rstream/config.yaml`.

If TURN credential generation fails, verify the current project endpoint, the active authentication token, and the TURN routing fields stored in the local `rstream` context.

If the public URL opens but no video appears, start with the runtime log, the selected GStreamer pipeline, browser autoplay and permissions, TURN reachability from both peers, and the availability of the chosen encoder on the target machine.
