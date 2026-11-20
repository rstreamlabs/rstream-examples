# WebRTC video producer

Run a Go producer that encodes a test pattern with GStreamer, serves a browser viewer and publishes its WHEP endpoint through rstream.

## Quick start

Install the Go version in `go.mod`, a C compiler, `pkg-config`, GStreamer development files and Node.js 24 with npm. Use the [native dependency commands](docs/000-build.md#requirements) for macOS or Ubuntu/Debian. For a public viewer URL, install the rstream CLI and select the project that will publish the demo. Replace `your-project-endpoint` with its endpoint.

```sh
rstream login
rstream project use your-project-endpoint
```

Build the producer and its embedded viewer from `webrtc-video/producer`. Start it with the H.264 test-pattern profile.

```sh
make build
./webrtc-video-producer -config ./config.h264.yaml
```

Open the logged `Public URL` and select **Start streaming**. A working session shows **Peer** as `connected`, **ICE** as `connected` or `completed`, and **Playback** as `Playing` while the test pattern is visible. The supplied profile publishes a viewer anyone with the URL can open. Use it with the test pattern, and press `Ctrl-C` to close the tunnel when finished.

### Play on the same machine without rstream credentials

After `make build`, create and run a temporary H.264 profile with tunnel publication and producer TURN disabled.

```sh
local_config=$(mktemp)
sed -e 's/^  enabled: true$/  enabled: false/' \
    -e 's/^  useTurn: true$/  useTurn: false/' \
    config.h264.yaml > "$local_config"
./webrtc-video-producer -config "$local_config"
```

Open `http://127.0.0.1:8080`, set **Browser ICE path** to **Direct**, and select **Start streaming**. Stop the producer with `Ctrl-C` and remove the temporary profile with `rm "$local_config"`.

## Choose a profile

| Profile                                                                       | Result                                                                 |
| ----------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| [H.264 test pattern](config.h264.yaml)                                        | Fixed-rate 720p30 with H.264 level 3.1.                                |
| [AV1 test pattern](config.av1.yaml)                                           | Fixed-rate AV1 with a compatible browser.                              |
| [Adaptive H.264 test pattern](config.test-pattern.h264.twcc-gcc.yaml)         | Encoder bitrate follows TWCC congestion feedback.                      |
| [Adaptive H.264 with FlexFEC](config.test-pattern.h264.twcc-gcc-flexfec.yaml) | Adds forward error correction alongside retransmission.                |
| [Provisioned H.264](config.provisioning.h264.yaml)                            | Receives scoped tunnel and TURN credentials from the Next.js platform. |
| [Discovered H.264](config.discovery.h264.yaml)                                | Publishes a stable device identity using existing project credentials. |

See [Producer configuration](docs/001-configuration.md) for the full camera and codec matrix, quality controls, optional source formats, TURN and OpenMetrics. The [Next.js platform](../platform/README.md) adds viewer authorization, and the [MediaMTX distributor](../distributor/README.md) serves several viewers over one device uplink.

## Verify or package the producer

`make test` checks the viewer types and runs the Go tests. Use the [build guide](docs/000-build.md) for full verification, the no-viewer build and static Linux packaging. For capture and signaling internals, see [Producer architecture](docs/002-architecture.md). For a reproducible degraded-link experiment, see [Media path testing](docs/003-network-testing.md) and the [qualification suite](qualification/adaptive-streaming/README.md).

Follow the [device-to-browser walkthrough](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream) for the SDK and media path in context.
