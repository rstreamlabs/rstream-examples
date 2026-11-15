# WebRTC video producer

Stream a device’s video to a browser without opening an inbound port. This Go application captures and encodes with GStreamer, serves its viewer and WHEP API, and uses the rstream Go SDK for tunnel publication and managed TURN credentials.

The producer owns the media path in all three video examples. Start here for one device and one viewer; the [Next.js platform](../platform/README.md) adds device provisioning and viewer authorization, and the [MediaMTX distributor](../distributor/README.md) adds a shared upstream for several viewers. The [video overview](../README.md) explains their roles.

## Requirements

Use the Go version in `go.mod`, a C compiler, `pkg-config` and GStreamer development files. The embedded viewer also needs Node.js 24 and npm. Follow [Build the producer](docs/build.md) for macOS, Linux and WSL2 prerequisites.

For publication through rstream, install the CLI and select a project:

```sh
rstream login
rstream project use my-project-endpoint
```

Replace the endpoint with your project’s endpoint. A local-only run is available below without publishing a tunnel.

## Quick start

From this directory, build the viewer and producer, then run the H.264 test-pattern profile:

```sh
cp config.h264.yaml config.yaml
make build
./webrtc-video-producer -config ./config.yaml
```

Open the printed public URL, then select **Start streaming**. A working session shows a connected peer and ICE state and a playing video. The page and WHEP signaling share the tunnel origin; WebRTC carries media directly or through TURN according to ICE.

For a local viewer at `http://127.0.0.1:8080`:

```sh
make run-local
```

The build targets apply the required [ICE concurrency patch](../ICE-PATCH.md). Use those targets when adapting the sample.

## Choose a profile

| Profile                                            | Purpose                                                                                             |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| [H.264 test pattern](config.h264.yaml)             | First browser connection, using 720p30 with matching level 3.1 limits.                              |
| [AV1 test pattern](config.av1.yaml)                | The same path with an AV1 encoder and a compatible browser.                                         |
| [Provisioned H.264](config.provisioning.h264.yaml) | Obtain scoped tunnel and TURN credentials from the Next.js platform.                                |
| [Discovered H.264](config.discovery.h264.yaml)     | Publish a stable device identity with existing project credentials for an organization’s inventory. |

Camera and adaptive profiles are described in [Producer configuration](docs/configuration.md). Match the capture source, encoder and receiver capacity before using a profile on a real device. Optional source presets change one upstream rendition; they do not create a transcoding ladder.

## Continue the integration

| Task                                                        | Guide                                                                                                      |
| ----------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| Understand SDK, capture and delivery boundaries             | [Architecture](docs/architecture.md)                                                                       |
| Install native dependencies, build or package the binary    | [Build and troubleshooting](docs/build.md)                                                                 |
| Configure publication, TURN, codecs, adaptation and metrics | [Configuration](docs/configuration.md)                                                                     |
| Test an impaired media path                                 | [Network testing](docs/network-testing.md)                                                                 |
| Follow the complete walkthrough                             | [Device-to-browser guide](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream) |

Use [Contributing](../../CONTRIBUTING.md) and [Security](../../SECURITY.md) for repository-wide development and support.
