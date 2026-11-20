# Distribution, recording and source control

Return to the [platform overview](../README.md) for the application path. Run commands from `webrtc-video/platform`, unless a step changes directory.

## MediaMTX distribution metrics

With `VIDEO_DISTRIBUTOR=mediamtx`, set `MEDIAMTX_METRICS_URL` to the private
MediaMTX metrics endpoint reachable from the Next.js server, for example
`http://127.0.0.1:9998/metrics` when both run on the same host. Leave it empty to
disable this feature. `npm run mediamtx:local` configures the local endpoint
automatically. Keep port 9998 on loopback or a private network; do not expose it
through the public WHEP tunnel. The administrative API remains disabled.

The normal player shows source readiness, total readers and the rates derived
from MediaMTX path inbound/outbound byte counters. Outbound is the total across
readers; these values are not encoder targets or a measurement of all network
protocol overhead. Full-page viewing keeps only its compact controls.

`GET /api/devices/<device-id>/metrics` authorizes every request before consulting
the server cache. It returns 204 when disabled, 503 when unavailable, and never
returns upstream URLs or raw metric labels. Initial rates are unknown; an
observed counter reset, readiness change, failed scrape or gap over 15 seconds
starts a new baseline. Rate samples are process-local, so a serverless cold start
or another application replica also needs two observations.

The browser polls every five seconds while the normal player is visible, with
no overlapping requests. Server scrapes coalesce per device, including failures,
for two seconds. A process retains at most 128 device observations and admits at
most eight concurrent scrapes with 64 observers each. Each scrape has a
three-second deadline and a 32 KiB response bound. Cancelling one observer does
not interrupt another; cancelling the last observer stops the upstream request.
No metrics request starts the video source or adds a producer connection.

## Optional recent recordings

Recording is disabled by default. Recent recording and playback can be tested
with the local MediaMTX stack:

```bash
npm run mediamtx:local -- --recording true
```

This explicitly enables MediaMTX recording and its private playback server,
binds port 9996 to loopback and mounts a temporary 512 MiB recording volume.
Recordings disappear when the container is removed. The producer still starts
on demand; requesting an index or a recorded clip does not start a live source.
When the recording API is available, a history button appears beside the
full-page control. It opens the most recent available recording. The timeline
marks recorded spans and gaps; selecting a gap or an expired segment shows an
explicit unavailable state. **Live** returns to the existing WebRTC session.
The source-quality controls apply to live capture and are hidden during replay.
Controls stay below the picture in both normal and full-page views.

Replay uses a separate MP4 video element and bounded clips, not a seek on the
WebRTC stream. The live connection stays established so returning to live does
not reconnect the source. In full-page replay, the same live video continues in
a small preview above the controls. Click it or focus it and press Enter to
return to live. The preview remains live while the recording is paused; leaving
full-page mode hides it and pauses the hidden live video again. Inline replay
keeps its existing layout and **Live** control. The preview adds no second
WebRTC session; replay still uses a separate MP4 decoder. Contiguous clips load in sequence; gaps stop replay
instead of silently jumping forward. Clip changes can introduce a short pause;
this is not a seamless DVR. Hiding the page cancels the replay media load and
index polling; returning restores the position paused, subject to retention.
The dashboard exposes these controls while the device is online. The recording
quality run checks replay and disk-full recovery. Measuring live overhead needs
matched recording-enabled and recording-disabled runs. [Distribution testing](003-testing.md#qualify-the-complete-distribution-paths).

For a separate deployment, configure MediaMTX as described in the
[distributor recording section](../../distributor/docs/003-observability.md#optional-recent-recordings).
Set `MEDIAMTX_PLAYBACK_URL` to its private HTTP(S) playback base URL and
`MEDIAMTX_RECORDING_WINDOW_SECONDS` to the recent window to expose (300 seconds
by default, 30–600 supported). An empty URL disables the API. The recent window
restricts access; it does not configure disk retention or reserve storage.

`GET /api/devices/<device-id>/recordings` returns the available time spans, or
204 when disabled. `GET /api/devices/<device-id>/recordings/playback?start=<RFC3339>&duration=<seconds>`
streams a standard MP4 clip of at most 30 seconds within that window. Every
request checks device access. Discovered devices must still be present in the
authorized live inventory; a stale database observation alone does not grant
recording access. Separate spans can indicate a gap or codec/format change and
must not be assumed to concatenate. Deletion between index and playback can
return 404; an unavailable recording service returns 503.

Next.js supplies a short-lived, path-scoped `playback` JWT to MediaMTX. No JWT,
upstream URL or recording filename reaches the browser. Media responses stream
with backpressure, a 30-second request deadline and a 64 MiB bound. Disconnects
cancel upstream reads, including an unconsumed body. MediaMTX's MP4 endpoint
does not support byte ranges; a Range request receives the bounded whole clip
with status 200 and `Accept-Ranges: none`. The browser buffers one bounded clip
as a Blob before playback so Safari can seek without HTTP range support. The
load has a 35-second deadline and a 64 MiB limit; changing clips or leaving
replay cancels the load and releases the previous object URL. This adds initial
loading time and up to one clip of browser memory. No media body is cached by Next.js.
Each application process admits four concurrent clips and four index reads;
index requests coalesce for two seconds, with at most 128 cached devices and
32 waiters per read. Capacity must account for the number of application
processes and for replay traffic passing through Next.js.

Playback health uses presented-frame callbacks when available. Safari may report
zero `totalVideoFrames` for an actively playing MediaStream; treating that value
as a stalled decoder can incorrectly switch a healthy MediaMTX session to the
direct fallback. The monitor releases its callback when the reader closes and
continues to detect a frame counter that stops advancing. Older browsers use
the available playback counters.

The native MediaMTX integration check requires the distributor's patched
`mediamtx` 1.21.3 runtime. Follow the [local testing commands](003-testing.md#validate-changes-locally) to build it, set `RSTREAM_MEDIAMTX_BINARY` and install the matching browsers. The check exercises recording authorization, idle-source behavior, H.264 recording and MP4 decoding, and deletion after a short test retention period.

## Optional source quality

The player's **Full page** button expands the video inside the current tab,
keeping the same WebRTC session. Controls sit below the image in both layouts;
the compact full-page toolbar retains the quality selector when presets are
available. The expand/exit controls use icons with accessible labels and tooltips.
Use **Exit full page** or Escape to restore the
dashboard and its scroll position. This layout works without the browser's
native Fullscreen API; browser tabs and address bars remain browser-controlled.

Use `../producer/config.provisioning.quality.h264.yaml` to advertise Low
(1 Mbit/s), Medium (4 Mbit/s), High (10 Mbit/s), and Auto. Presets are bitrate
ceilings; congestion control can reduce the encoder target below the selected
ceiling. Auto restores the configured adaptive range. Resolution and frame rate
stay defined by the device pipeline. RTP/RTCP, retransmission, and FEC overhead
are additional traffic beyond the encoded-video ceiling.

The player discovers modes from `GET /api/devices/:id/quality`. Its selector is
hidden for unconfigured devices and updates other viewers within five seconds.
A change affects every viewer of the device, including MediaMTX readers. The
platform checks ownership and membership, then forwards a bounded request through
a token restricted to `/api/quality`. That token stays on the server. Browser
viewer and adapter source tokens remain restricted to WHEP.

Selections include an opaque version. A competing change returns `409` and is
refreshed before another selection; it cannot silently overwrite a newer choice.
The producer reports selected mode and applied encoder target separately. The
selection survives idle/reconnect cycles in the running producer and resets to
`quality.default` after process restart. See the [producer configuration](../../producer/docs/001-configuration.md#optional-source-quality-presets)
for validation and the HTTP contract.
