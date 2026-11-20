# On-demand WebRTC distribution

Serve several browser viewers over one device uplink. On the first request for a device, MediaMTX starts a Go adapter. The adapter obtains scoped credentials from the Next.js platform, pulls and repairs the producer's WHEP stream, and publishes H.264 into MediaMTX. Each viewer gets its own downstream session from the shared rendition.

## Run with the platform

Prepare `webrtc-video/platform/.env.local`, install its npm dependencies and apply the PostgreSQL migrations using the [platform quick start](../platform/README.md#run-the-application). Keep the platform's `npm run dev` stopped and install Docker and the rstream CLI. The launcher starts Next.js and builds the combined MediaMTX and Go adapter image.

Start the stack from `webrtc-video/platform`.

```sh
npm run mediamtx:local
```

Open `http://localhost:3000`, sign in and create a device. Start the [provisioning producer](../platform/README.md#run-the-application) with the device secret. Select the same device in two signed-in browser sessions. Both can play the test pattern through the MediaMTX listener on `http://localhost:8889`. Closing the last viewer releases the upstream after its idle grace. Press `Ctrl-C` to stop the producer and the launcher.

The local launcher creates a temporary rstream tunnel for control-plane callbacks. See [Local stack options](../platform/docs/000-setup.md#run-the-complete-local-mediamtx-stack) to publish the WHEP listener through rstream, test a production build or enable the disposable recording volume.

## Choose a distribution profile

| Profile                    | Use it when                                                                     |
| -------------------------- | ------------------------------------------------------------------------------- |
| Direct producer playback   | A single viewer needs end-to-end encoder adaptation.                            |
| MediaMTX with this adapter | A platform needs dynamic devices, source repair and several viewers per uplink. |
| Native MediaMTX pull       | A static H.264 source accepts the reduced MediaMTX WHEP offer.                  |

The adapter delivers one H.264 rendition without transcoding. Downstream feedback terminates at MediaMTX and the selected rendition must fit each viewer's link. See [Delivery profiles](docs/000-profiles.md) for codec compatibility, repair and signaling details.

## Configure and verify the distributor

| Task                                                 | Guide                                      |
| ---------------------------------------------------- | ------------------------------------------ |
| Build the pinned image and configure a separate host | [Setup](docs/001-setup.md)                 |
| Understand retries, packet bounds and cleanup        | [Lifecycle](docs/002-lifecycle.md)         |
| Inspect metrics, session associations and recording  | [Observability](docs/003-observability.md) |
| Run the local integration and performance checks     | [Testing](docs/004-testing.md)             |

Follow the [MediaMTX walkthrough](https://rstream.io/guides/distribute-webrtc-video-with-mediamtx-and-rstream) for the deployment and media-path decisions in context.
