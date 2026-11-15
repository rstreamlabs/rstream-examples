# Observe distribution and recordings

Return to the [distributor overview](../README.md) for the application path. Run commands from `webrtc-video/distributor`, unless a step changes directory.

## Observe the two media legs

The producer exporter describes capture, encoding, TWCC/GCC, pacing, queue
residence, RTX, and FlexFEC on the shared device uplink. MediaMTX exposes path
readiness, reader count, and inbound/outbound bytes:

```text
GET http://127.0.0.1:9998/metrics?type=paths&path=devices/<device-id>
```

To show a scoped summary in the Next.js player, configure its server-side
`MEDIAMTX_METRICS_URL` with this private `/metrics` endpoint. Next.js authorizes
the device before reading readiness, reader count and byte counters. It derives
rates from consecutive observations and does not expose this listener or the
administrative API to the browser. Scraping never starts an on-demand source.

Keep device identity in scrape-target labels rather than adding session or
viewer identifiers in application metrics. The adapter's structured shutdown
log distinguishes received recovery packets, repairs delivered before the
reorder window closes, and RTX/FlexFEC packets that arrived too late. It also
includes the attempt's error when closing the publication causes MediaMTX to
stop the on-demand process before its retry log can run. The
current on-demand process deliberately does not open a second metrics listener
per device.

## Optional session associations

To associate downstream readers with a source session, the host can expose
`GET /bindings/v1` on a private Unix socket. Set
`RSTREAM_DISTRIBUTOR_BINDINGS_SOCKET=/private/distributor/bindings.sock`; its
parent directory must already exist with mode 0700 and the socket uses 0600.
Share that directory only with the trusted consuming service. This endpoint
is absent from the metrics HTTP listener and does not start a video source.

Each entry contains the MediaMTX path, bridge attempt ID, source and destination
session IDs, and heartbeat age. IDs come only from the optional `ID` response
header, as provided by MediaMTX. Source servers must expose an equivalent
non-secret identifier to enable association. Missing or invalid IDs leave the
association unavailable; video continues normally. Neither `Location` resource
URLs, SDP nor credentials are exported.

An ICE restart renews the transport within the same HTTP session, so its IDs
and association remain stable. A replacement WHEP or WHIP session belongs to a
new bridge attempt; consumers must resolve the new association instead of
carrying the previous one across the replacement.

Entries disappear when an attempt ends or its process heartbeat expires. A host
restart changes the returned runtime ID. Consumers must also verify the current
MediaMTX publisher and reader membership, and source validity: the snapshot alone
is not proof of delivery to a particular viewer or of RTP timestamp preservation.
No association can be inferred from a matching path or timestamp alone.

## Optional recent recordings

The bundled configuration leaves `record` and `playback` disabled. To enable a
short history, explicitly set `MTX_PATHDEFAULTS_RECORD=true` and
`MTX_PLAYBACK=true`, then supply a writable `/recordings` mount owned by UID/GID 10001. Keep playback port 9996 on loopback or a private network accessible to
Next.js. Playback uses the existing JWT issuer/JWKS and a separate, path-scoped
`playback` permission; live `read` and `publish` tokens cannot read recordings.
The administrative API remains disabled.

The configuration uses fMP4, one-second parts, an 8 MiB maximum part size,
five-second minimum segments and five-minute retention. Recording only follows
an already transmitting path. It does not create a permanent reader or change
the on-demand source lifecycle. Codec changes and interruptions can split the
available time spans.

**Retention is not a byte quota.** MediaMTX cleans periodically (half the
configured retention interval in version 1.21.3), and segments can exceed their
minimum duration while waiting for a key frame. Use a dedicated filesystem with
an enforced quota or a size-limited temporary volume. Size it for the aggregate
bitrate of simultaneously active devices, cleanup delay and key-frame overhead.
The local helper's 512 MiB tmpfs is bounded, consumes server memory, and is
discarded with the container; it does not guarantee five minutes at every
bitrate/device count. For sustained server use, prefer an appropriately sized
quota-limited disk volume and monitor recording errors/free space. Keep media
storage separate from logs and the system filesystem.

**Full storage temporarily disables history.** MediaMTX 1.21.3 can leave incomplete
fMP4 segments after `ENOSPC`; its playback index rejects the whole requested
interval if any segment cannot be parsed. Freeing space lets recording resume,
but history can remain unavailable until those files leave the requested window
or retention removes them. With the bundled
five-minute retention, allow up to another cleaner interval (2.5 minutes).
Next.js reports this as unavailable, keeps live viewing independent and retries
the index. It does not delete segments or claim a complete history during the
fault. Prevent saturation with sufficient capacity and monitoring; this optional
history is not an archival recording service.

Next.js exposes only the configured recent window and proxies bounded MP4
clips; see the [platform API and local setup](../../platform/docs/002-media.md#optional-recent-recordings).
Live video continues to use WebRTC. Qualify disk activity, full-volume behavior
and simultaneous replay against your live latency/resource budget before
enabling this optional feature in a deployment.
