# On-demand WebRTC distribution

Add multi-viewer delivery to the [video producer](../producer/README.md) without multiplying device uplinks. MediaMTX starts one Go adapter when a viewer requests a device path. The adapter obtains scoped credentials from the [Next.js platform](../platform/README.md), pulls the producer’s WHEP stream, repairs the upstream and publishes H.264 into MediaMTX. Later viewers share it; the last viewer leaving releases the source after the configured idle grace.

The adapter does not transcode. The producer controls one upstream rendition, while MediaMTX and each browser own a separate downstream congestion domain. Viewers must be able to receive the selected rendition; use direct delivery when one viewer needs end-to-end encoder adaptation.

## Choose a delivery path

| Path                       | Use it for                                                          |
| -------------------------- | ------------------------------------------------------------------- |
| Direct producer-to-browser | One viewer, with end-to-end adaptation.                             |
| MediaMTX with this adapter | Dynamic device discovery, upstream repair and multi-viewer fan-out. |
| Native MediaMTX pull       | A static source compatible with MediaMTX’s smaller offer profile.   |

[Delivery profiles](docs/000-profiles.md) describes the codec and signaling contracts. The platform’s `mediamtx` backend uses the adapter; native pull is a separate configuration.

## Run with the platform

Start with the [platform setup](../platform/docs/000-setup.md#run-the-complete-local-mediamtx-stack) for the complete local application. For a separate host, follow [Build and configure the distributor](docs/001-setup.md): it covers the combined image, signing keys, resolver access, public or tunneled WHEP exposure and the matching platform configuration.

The combined image runs MediaMTX and the adapter together. Build it from this directory:

```sh
make image
```

The build preserves the [required ICE patch](../ICE-PATCH.md) and the bounded MediaMTX H.264 extension. The [setup guide](docs/001-setup.md#h264-access-unit-budget) explains the extension and its source checks. Authentication, signaling and metrics listeners have separate configuration; use the reviewed Compose example rather than exposing every port publicly.

## Documentation

| Task                                                 | Guide                                                                                         |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Choose direct, adapter or native-pull delivery       | [Profiles](docs/000-profiles.md)                                                              |
| Build the image and connect it to the platform       | [Setup](docs/001-setup.md)                                                                    |
| Understand retries, packet bounds and cleanup        | [Lifecycle](docs/002-lifecycle.md)                                                            |
| Inspect metrics, session associations and recordings | [Observability](docs/003-observability.md)                                                    |
| Run local integration and performance checks         | [Testing](docs/004-testing.md)                                                                |
| Follow the distribution walkthrough                  | [MediaMTX guide](https://rstream.io/guides/distribute-webrtc-video-with-mediamtx-and-rstream) |

See [Contributing](../../CONTRIBUTING.md) and [Security](../../SECURITY.md) for repository-wide guidance.
