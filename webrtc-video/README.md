# Adaptive real-time WebRTC video

Build a path from a remote video source to a browser-facing application. The Go producer captures and encodes video, adapts to congestion and repairs packet loss. rstream publishes its WHEP signaling through an outbound tunnel and provides managed STUN/TURN; media travels over standard WebRTC.

## Choose a delivery path

| Path                               | Start here                                    | What it adds                                                         |
| ---------------------------------- | --------------------------------------------- | -------------------------------------------------------------------- |
| One device and one browser         | [Producer](producer/README.md)                | An embedded viewer and the complete device-side media path.          |
| A product around device streaming  | [Next.js platform](platform/README.md)        | Device identity, provisioning, viewer authorization and fleet state. |
| Several viewers sharing one uplink | [MediaMTX distributor](distributor/README.md) | On-demand fan-out around the same producer.                          |

Capture, encoding, congestion control, pacing and repair stay in the producer across all three paths. The platform owns access and inventory. The distributor owns a shared upstream and separate viewer sessions; it does not transcode. Producer metrics describe the device uplink, while MediaMTX and browser telemetry describe downstream delivery.

The [device-to-browser guide](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream), [Next.js guide](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream) and [MediaMTX guide](https://rstream.io/guides/distribute-webrtc-video-with-mediamtx-and-rstream) follow that progression.

## Run and develop

Start with the producer’s [quick start](producer/README.md#quick-start), then add the platform if the application needs device management. Its [setup guide](platform/docs/setup.md) explains personal or organization access, managed or discovered inventory and optional distribution.

The root `make build`, `make test`, `make verify` and `make clean` targets cover both Go roles; `make run` starts the producer. These targets include the required [ICE concurrency patch](ICE-PATCH.md). Preserve that step when adapting the examples.

| Development task                                         | Guide                                                                            |
| -------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Install native media dependencies and build the producer | [Producer build](producer/docs/build.md)                                         |
| Configure codecs, source profiles, adaptation and TURN   | [Producer configuration](producer/docs/configuration.md)                         |
| Run the application and provision a device               | [Platform overview](platform/README.md)                                          |
| Configure multi-viewer delivery                          | [Distributor setup](distributor/docs/setup.md)                                   |
| Add bounded recent recordings                            | [Platform media capabilities](platform/docs/media.md#optional-recent-recordings) |
| Validate changes and measurements                        | [Repository testing](../docs/testing.md)                                         |

See [Contributing](../CONTRIBUTING.md) and [Security](../SECURITY.md) for repository-wide guidance.
