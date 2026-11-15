# Test the video platform

Return to the [platform overview](../README.md) for the application path. Run commands from `webrtc-video/platform`, unless a step changes directory.

## Validate changes

```bash
npm ci
npm run verify
npm run test:access
```

The access suite owns disposable PostgreSQL containers, applies both upgrade and
fresh migrations, and exercises the production Next.js routes. It seeds test
sessions and substitutes GitHub/engine HTTP responses; it does not certify a real
organization's OAuth approval or SSO policy. It covers cross-account access,
shared inventory, owner lock timeouts, nonmembers, watch-token scope, and the
server-side prohibition on direct fallback.

To check discovery against a real rstream project, configure the application
credentials and project endpoint in `.env.local`, build the application, then run:

```bash
docker build --file ../producer/qualification/adaptive-streaming/Dockerfile \
  --tag rstream-video-discovery:qualification ..
RSTREAM_DISCOVERY_PRODUCER_IMAGE=rstream-video-discovery:qualification \
  npm run test:discovery-live -- /path/to/empty/discovery-evidence
```

This finite check creates an isolated database and a temporary, token-protected
producer tunnel in that project, using a private CLI context and a short-lived
token limited to that tunnel name. It checks discovery without enrollment,
shared source-quality controls, stale-selection rejection, stable identity after
reconnection/rename, offline history and live-only inventory. It cleans up its
containers and private context. Only GitHub membership responses are substituted;
rstream APIs, tunnel publication and producer HTTP controls are real. This check
does not exercise video playback or an organization's actual OAuth/SSO policy.
Optionally run `npx playwright-core install chromium` and set
`RSTREAM_DISCOVERY_BROWSER=bundled` to use the browser matched to the pinned
Playwright package, or set it to an explicit Chrome/Chromium executable to
include direct playback from the discovered producer, decoded 720p frame cadence
and encoder shutdown after the viewer closes. Browser runs retain the first
observed video frame callback with the video visible in the viewport, its center
unobstructed, and the loading overlay absent. The reported time is the later of
the browser's expected display time and the observation after visibility checks:
a conservative startup estimate, not an exact physical display measurement.
Navigation, authorization and WHEP/peer milestones are retained. The historical
exact-first-submission diagnostic remains separate and requires a callback frame
counter of one; decoder/compositor batching can leave it unknown even when the
visible observation is valid. Missing visible evidence or a retried startup
fails the measurement. Independent lifecycle/discovery scenarios
still run before that final gate, retaining their results alongside a failed
startup measurement. These timings cover an authenticated
dashboard opening with an already running producer after previous membership
and inventory requests, not OAuth sign-in or device process startup. Every
viewer request still reads live inventory. The check also exercises three
same-page cycles: selecting an offline history fixture unmounts the player and
executes its normal WHEP DELETE, then selecting the live source reopens it after
the encoder has stopped. It records click-to-visible-frame time, deletion and
encoder release separately. These cycles retain the configured production idle
grace and check its completion within a 15-second qualification allowance.
The runtime report retains the actual browser, Playwright and Node.js versions,
including on failed measurements, so results from an older installed browser
are not mistaken for the currently pinned browser.
For a focused compositor diagnosis, `RSTREAM_DISCOVERY_STARTUP_TRACE=true`
additionally saves a Chromium media trace for each initial navigation. These raw
files can contain private URLs and must remain private. Traced runs are diagnostic
observations, not baseline performance measurements.
To exercise discovery with the
adaptive MediaMTX adapter, also set `RSTREAM_DISCOVERY_DISTRIBUTOR=mediamtx`.
That variant uses the documented local stack helper (including its production
build and temporary HTTPS callback tunnel), so its local ports must be free.
It opens separate browser sessions for two organization members, checks one
encoder for two MediaMTX readers, synchronizes a source-quality change, denies
a nonmember, preserves playback when the first member leaves and stops the
encoder after the last leaves. Both persistent-history and live-only inventory
checks still run. GitHub membership remains a fixture. Neither variant forces
a relay path. First prepare adequate Linux UDP socket limits
as described in the [distribution prerequisites](../../distributor/docs/testing.md#technical-qualification).
These graceful UI closures do not measure abrupt browser loss, which can use the
producer's longer bounded ICE recovery grace period before stopping the encoder.

To additionally exercise full-page viewing in Chromium, Firefox and WebKit:

```bash
npx playwright-core install firefox webkit
RSTREAM_DISCOVERY_BROWSER=/path/to/chrome \
RSTREAM_FULL_PAGE_BROWSERS=1 \
RSTREAM_METRICS_BROWSER=1 \
RSTREAM_RECORDING_BROWSER=1 \
RSTREAM_UI_CAPTURE_DIRECTORY=/path/to/ui-evidence \
  node scripts/access-routes.integration.mjs
```

This browser check uses a local synthetic WebRTC source and the real application
UI. It checks session continuity, keyboard/focus handling, scroll restoration,
portrait/landscape layout and source disappearance. `RSTREAM_METRICS_BROWSER=1`
also checks distribution indicators and recovery after a metrics outage. It does not replace network
qualification or testing on physical mobile devices.
`RSTREAM_RECORDING_BROWSER=1` additionally requires FFmpeg and checks real MP4
decoding in Chromium/Firefox/WebKit, replay navigation, gaps, expiry, index outages,
visibility cancellation and return to the same live session. The recording
index and media service are controlled fixtures in this UI check; the native
MediaMTX recording test above covers actual recording and retention.
The disposable Firefox profile permits loopback ICE, selects the local loopback
interface and disables address obfuscation for this same-machine fixture. User
browser profiles are untouched. A failed connection is reported as a failed
qualification; successful layout checks alone do not establish media playback.
For a separate Linux Firefox test host, set `RSTREAM_FIREFOX_WS_ENDPOINT` to
an owned Playwright browser endpoint with the same Playwright version as the
platform lockfile. `RSTREAM_ACCESS_TEST_PORT` optionally fixes the application's
loopback port, so a TCP port forward can give that browser access to the test
application at the same URL. Keep the browser endpoint and forwarded port
private. This uses real WebRTC media without a browser HTTP proxy, which can
restrict Firefox's UDP candidates. The default remains local browsers and
automatically allocated application ports.

With the rstream credentials in `.env.local`, Docker, and Chrome installed, run
the complete distribution and quality paths:

```bash
npm run qualify:distribution -- /path/to/empty/evidence-directory
RSTREAM_QUALIFICATION_QUALITY=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/quality-evidence public
MEDIAMTX_ALLOW_DIRECT_FALLBACK=false RSTREAM_QUALIFICATION_QUALITY=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/required-media-evidence public
RSTREAM_QUALIFICATION_QUALITY=1 RSTREAM_QUALIFICATION_SOURCE_FORMATS=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/source-format-evidence public
RSTREAM_QUALIFICATION_QUALITY=1 RSTREAM_QUALIFICATION_RECORDING=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/recording-evidence public
```

The quality run changes all four modes through the actual device tunnel while
two browser readers share one encoder, measures actual encoded throughput at
MediaMTX, rejects a stale version, and exercises
distributor failure/recovery. The required-MediaMTX variant also checks that
explicit direct authorization is refused. Use a committed, clean checkout for
publishable evidence. Each runner cleans up the resources it creates.

The source-format variant uses the optional H.264 profile example and repeats
360p15, 540p24 and 720p30 selections through MediaMTX and direct fallback. It
checks observed encoder state, actual presented dimensions, frame cadence,
transition gaps, media/RTP timestamp continuity and preservation of the video
element and MediaStream. It writes per-transport transition observations,
including partial observations on failure. Set `RSTREAM_UI_CAPTURE_DIRECTORY`
to retain desktop/mobile screenshots of normal and full-page viewing. This
manual-profile check does not qualify automatic format adaptation under network
impairment or measure capture-to-display latency or CPU savings.

The recording variant enables the local helper's bounded temporary recording
volume. It checks real replay through the platform, two readers sharing one
encoder, and live-session preservation. It deliberately fills only that owned
512 MiB tmpfs, requires an actual recorder `ENOSPC` failure, checks presented
frame cadence before/during/after the fault, then releases the filler and waits
for new recorded segments. It requires `ffprobe` to decode a newly closed segment
within 30 seconds after freeing space. MediaMTX can leave incomplete files that
make its entire playback index unavailable: the test separately requires index
recovery within eight minutes (five-minute retention, 2.5-minute cleaner interval
and polling margin), while keeping the same live frame-rate and gap gates
throughout. No recorded file is deleted to accelerate recovery. Partial
observations and both recovery times are retained in `recording.json`.
This fault test does not measure capture-to-display latency or compare recording
overhead against a recording-disabled baseline.
