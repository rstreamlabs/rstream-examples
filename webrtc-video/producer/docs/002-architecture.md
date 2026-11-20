# Producer architecture

Return to the [producer overview](../README.md) for the application path. Run commands from `webrtc-video/producer`, unless a step changes directory.

## One media core, three delivery paths

This Go codebase is the device-side foundation for the complete video series.
It owns capture, encoding, congestion control, pacing, packet repair, session
recovery, and producer metrics. The surrounding control and distribution planes
evolve without forking that implementation.

The [standalone guide](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream)
establishes the reference media path between one producer and one browser. The
[Next.js guide](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream)
runs this producer in provisioning mode and adds device identity, viewer
authorization, fleet state, and product policy. WHEP and media still terminate
on the selected media backend; Next.js does not proxy them.

The MediaMTX guide adds an on-demand distribution adapter around the same
codebase. One adaptive device upstream feeds MediaMTX, which can then serve
several viewers without multiplying device uplink usage. Direct WebRTC remains
available for one-to-one sessions and transport diagnosis; ICE still chooses
direct or TURN connectivity for that session.

Across all three shapes, the capture pipeline, encoder, adaptive controller,
pacer, repair strategy, recovery logic, and producer metrics remain one
implementation to operate and qualify. The control plane decides who may start
or watch a stream. The distribution plane decides whether the encoded stream
goes to one browser or to a fan-out tier.

Fan-out creates two congestion domains. The adapter terminates repair on this
producer's shared upstream; MediaMTX and each viewer establish a separate
downstream control loop. Producer OpenMetrics describe the device uplink,
while MediaMTX and browser telemetry describe viewer delivery.

## Integration paths

This producer is the application-controlled path for products that combine
WHEP session control, managed TURN, ICE recovery, congestion-aware encoding,
bounded media queues, packet repair, and session diagnostics. `rstream-go`
places tunnel lifecycle, cancellation, and recovery policy inside the same Go
process as the media application.

The repository also includes a pipeline-first path in
[`netcat-media-streaming`](../../../netcat-media-streaming/README.md). It connects
GStreamer or FFmpeg to `rstream nc` through standard input and output, which
fits private point-to-point streams whose media pipeline already owns buffering
and recovery. Both paths use the same rstream network: the CLI keeps the
integration compact, while the SDK exposes the controls required by a complete
video product.

## Standalone path

The local HTTP server serves the embedded page and the small API surface the page needs: WHEP resources, TURN bootstrap, and status endpoints. On the media side, a GStreamer pipeline produces H.264 or AV1 access units and passes them to a WebRTC sender built on top of Pion. `rstream-go` publishes that local server through an HTTP tunnel and keeps the public URL available.

That layout keeps WHEP, TURN bootstrap, and viewer delivery on the same origin while avoiding any extra backend dedicated to this example.
