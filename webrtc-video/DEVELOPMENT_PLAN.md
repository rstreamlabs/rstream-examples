# Streaming evolution and validation plan

This work preserves the public, per-user provisioning demo and all supported
delivery paths. Deployment is a separate activity. Implementation and synthetic
qualification do not establish compatibility with untested cameras, encoders,
operating systems or networks.

## Implemented; final regression qualification pending

- GitHub personal ownership or shared organization ownership, with bounded
  membership verification, explicit configuration and existing-device migration.
- Optional source-wide bitrate presets, dynamically advertised to both viewers;
  automatic congestion control remains active below each selected ceiling.
- Concurrent selection protection, bounded control-plane requests and database
  waits, lifecycle fixes, dependency updates and corresponding guide changes.
- MediaMTX source RTCP consumption: sender reports must reach Pion's report
  interceptor so the producer receives usable round-trip measurements.
- Explicit release of GStreamer's native appsink callbacks on source closure.
  A reachability regression reproduces the previous source/pipeline retention
  and checks never-started, playing, stopped and end-of-stream lifecycles;
  the producer's full Go race suite passes with the fix.

## Implemented: project discovery; live-engine qualification pending

- Separate access (`user` / `organization`) from inventory (`managed` /
  `discovered`). Discovery requires organization access and an explicit project.
- Recognize published, token-authenticated HTTP video tunnels by an explicit
  label contract. A stable device UUID identifies the source across reconnects;
  optional `device-name` metadata supplies its human-readable name.
- Preserve PostgreSQL for authentication. Optionally remember automatically
  discovered devices and their last observed presence; without history, show
  only the live inventory. Database history never grants access to a tunnel.
- Remove provisioning and deletion controls in discovery mode. Preserve viewer,
  quality and MediaMTX credential scoping, and reject conflicting device IDs.
- Test real migrations/routes, concurrent observations, reconnect/rename,
  stale history, discovery outages and disabled persistence. Update existing
  README/guide sections and provide one CLI-backed producer configuration.

## In progress: optional source format adaptation

Resolution and frame-rate adaptation is a general-purpose WebRTC technique.
[Libwebrtc's adaptation model](https://webrtc.googlesource.com/src/+/HEAD/video/g3doc/adaptation.md)
separates resource observations, adaptation decisions and restrictions applied
by a source. Follow that separation without attempting to recreate libwebrtc.

The optional source-control interface and named GStreamer capsfilter adapter
are implemented at the source layer. Real x264/avdec_h264 checks cover in-place
resolution/frame-rate changes for capture-cap negotiation and scale/frame-drop
pipelines, including a fractional rate on restart. The controller confirms
encoded key-frame caps, distinguishes requested/observed/pending state, rejects
overlapping requests, and cancels observation waits on stop/close. Tests cover
timeouts, late observations, concurrent snapshots and native callback release.
Existing application configurations still use bitrate-only control. An opt-in
YAML example now connects source profiles to quality presets, and a separate
worker implements manual selection plus an optional bandwidth ladder with
hysteresis/dwell. Tests cover coalescing, cancellation, bounded retries and
continued bitrate response while a format change waits. Source initialization
errors release resources once. Real Pion offer/answer tests enforce H.264
frame-size, processing-rate and bitrate receive limits, including multiple
offered payload types; receiver-only extensions cannot raise sender limits.
The API distinguishes requested/observed/pending state using bounded per-profile
counts, validated by both shared readers. The configured GStreamer preset path
changes actual encoded caps between 360p15, 540p24 and 720p30 and restores Auto.
Both viewers now describe the confirmed source format; selecting a profile is
not displayed as proof that the encoder applied it. Compact full-page controls
remain on one row. Updated desktop/mobile screenshots await visual approval.
At revision `13a9f01`, the real provisioned-tunnel/browser qualification passed
nine selections through MediaMTX (two readers, one encoder) and nine in direct
fallback. Presented frames reached each requested resolution in at most 1.307s
through MediaMTX and 1.502s directly. The longest observed presentation gap was
100ms and 284ms respectively, with no media/RTP timestamp regression or video /
MediaStream replacement. Stable playback exceeded 80% of each configured frame
rate. These transition times include the control request and are not
capture-to-display latency measurements.
The preceding run at `889dd50` completed the same format transitions but failed
its final diagnostics gate: the harness did not correlate the expected metrics
503 responses during the deliberate MediaMTX outage. It remains a failed run.
The updated gate requires matching method, URL, response status, time and outage
phase; the successful run also verifies metrics disappearance and recovery.
Automatic-ladder network qualification, capture-to-display latency and CPU
measurements remain pending. These checks do not establish arbitrary hardware
support or AV1 format-switching support.

1. Define optional format profiles with bitrate ceiling, dimensions and frame
   rate. Existing bitrate-only configurations must remain unchanged. Expose
   requested, pending and observed formats separately.
2. Introduce a typed, capability-based source control interface, serialized
   changes, cancellation, bounded transition deadlines and explicit failure
   results. Integrators can supply a hardware-specific implementation privately.
   Do not run arbitrary shell callbacks or concurrent source transitions.
3. Implement an opt-in GStreamer controller using explicitly named elements.
   Qualify a pipeline that scales/drops frames before encoding and a source
   that can renegotiate capture caps. Do not rewrite arbitrary pipeline strings
   or claim every encoder supports live changes. GStreamer documents the
   [conditions for changing caps while playing](https://gstreamer.freedesktop.org/documentation/application-development/advanced/pipeline-manipulation.html#changing-format-in-a-playing-pipeline).
4. Validate manual profile transitions first, including encoder reconfiguration,
   IDR/parameter-set delivery, RTP timestamp continuity and actual decoded
   dimensions/cadence. Keep codec and negotiated transport unchanged.
5. Add optional automatic profile selection above the existing bitrate loop,
   with down/up hysteresis, minimum dwell and confirmation before another
   transition. Keep congestion response fast and format changes slower. Treat
   bandwidth, compression quality and encoder load as different observations;
   never infer CPU overload solely from bitrate. Hardware-specific thresholds
   and camera commands remain outside the public example.
6. Measure direct and MediaMTX playback across repeated transitions, congestion,
   concurrent preset changes, disconnects and cancellation. Report freezes,
   latency, actual formats and CPU/encoding cost; retain unsuccessful runs.
   Only claim support for tested pipelines. Document the optional mechanism in
   the existing guides without any application-specific hardware references.

The H.264 compatibility audit reproduced level-4 SPS output (`42c028`) while
SDP advertised level 3.1 in seven bundled synthetic configurations. Browser
receive offers observed in Chromium, Firefox and WebKit advertise level 3.1.
Bundled H.264 pipelines now use 720p30 with explicit encoded level 3.1 caps.
Fixed pipelines also check the receiver envelope before answering; the format
profile path keeps its finer per-profile bounds. Tests check actual SPS/decoded
frames and real Pion negotiation, including rejection of level-4 senders by
level-3.1 receivers and acceptance by compatible receivers. A custom 1080p
level-4 pipeline still encodes/decodes in the native test. The full producer
race suite passes. Historical 1080p network evidence remains unchanged and is
identified as historical; it never proves current-profile or SDP conformance.
Fresh direct/MediaMTX browser and network qualification remains required.

## Cross-cutting latency and resource criteria

- Compare changes against the current path at equal source content and network
  conditions. Measure time to first frame separately from capture-to-display
  latency, and report transition freezes and recovery time.
- Keep queues and retries bounded; do not trade lower packet loss for growing
  latency without an explicit measured benefit. Keep diagnostics out of the
  packet hot path and avoid unnecessary copies, encoders and periodic work.
- Measure CPU, memory and allocation rates, including capture, scaling and
  encoding costs. A low encoded bitrate is not evidence of low CPU usage.
  Hardware acceleration and capture-format changes remain capability-dependent.
- Preserve demand-driven source startup/shutdown and one upstream for shared
  viewing. Do not enable persistent transmission implicitly.

## In progress: MediaMTX observability

The private, authorized metrics reader and API are implemented, including
coalescing, cancellation, bounded resources and conservative rate baselines.
Route/database tests verify authorization before cached reads; a real MediaMTX
check verifies absent/not-ready paths and that scraping does not start a source.
The normal player shows a compact indicator row. Browser checks cover measured
values, outage/recovery without interrupting playback, and hiding the row in
full-page mode. Real live-distribution qualification at `13a9f01` also verifies
ready values, removal of stale values during a deliberate MediaMTX outage, and
recovery after restart. Desktop/mobile screenshots have been presented; visual
acceptance remains pending.

Use the existing metrics endpoint to provide bounded, read-only source readiness,
reader count and ingress/egress rates. Scope returned values to the authorized
device, cache/coalesce server requests and keep credentials/server topology out
of browser responses. Do not expose the administrative API. Validate unavailable,
idle and counter-reset states; avoid misleading zero rates on the first sample.

## Separate lot: recent recording and playback

The server foundation is implemented locally: opt-in MediaMTX recording with
short retention, a bounded temporary local volume, a private playback endpoint,
separate playback JWT permissions, and authorized recent-index/MP4 routes.
Requests have duration/size/concurrency/deadline bounds and cancel upstream body
reads on disconnect. Unit and real PostgreSQL/Next.js access checks pass.
A native MediaMTX 1.20 test records a real H.264 RTSP source, verifies playback
JWT isolation, decodes the resulting MP4 in Chromium/Firefox/WebKit and verifies
retention deletion without starting an on-demand source. Earlier failed fixture
runs exposed MediaMTX's missing-directory 400 response and FFmpeg's password
length limit; the client handles only the precise missing-directory case, and
the RTSP fixture now uses MediaMTX's supported token query parameter. Browser
playback still uses no URL credentials. Full-volume behavior, production-path
live resource impact and replay UI/visual acceptance remain qualification work.

Provide optional short-retention recording and authenticated playback, keeping
WebRTC as the live path. MediaMTX 1.20 supports recording and HTTP playback;
this does not provide seek on the existing WHEP session. Recordings cover only
periods in which a source was transmitting. Preserve the existing on-demand
source policy. The lot includes bounded storage/retention, separate playback
authorization, clear live/history navigation and tests that disk activity does
not harm live latency. Recording stays disabled by default.

## Visual acceptance

The initial discovered-inventory desktop/mobile presentation was accepted.
Add same-tab full-page viewing with discreet controls, preserved playback,
keyboard/focus handling and restored scroll position. Use a layout expansion
independent of the native Fullscreen API; verify Chromium, Firefox and WebKit,
including mobile layouts, and present the new view for approval.
The requested refinement places controls below the image in both layouts,
keeps quality left-aligned, uses accessible icon-only expand/exit buttons and
keeps the mobile controls on one line with a consistently spaced select chevron.
These layout changes are implemented locally. Real-media UI checks pass in
Chromium and WebKit at desktop, narrow mobile and landscape sizes. The Firefox
fixture still fails during ICE connectivity before expansion; qualification is
pending and that failure remains a failing gate. Updated screenshots have been
presented; the refined layout still awaits visual acceptance.
The subsequent spacing refinement uses 8px between related controls/text and
16px between groups, with full-width quality descriptions beneath the controls.
Chromium and WebKit playback/layout checks were repeated and pass. The known
Firefox ICE fixture failure remains; fresh mobile captures await acceptance.
The same 8px vertical spacing now covers wrapped device headers and player
error/retry controls, including full-page error padding. Chromium responsive
playback checks and the real PostgreSQL/Next.js access suite pass after this
refinement; it does not resolve the separate Firefox qualification issue.

All changed UI/UX must be presented to the user as actual desktop and mobile
screenshots in the conversation and explicitly validated. Include quality
controls, discovered inventory, metrics and recording/playback controls. Review
spacing, alignment, typography, contrast and responsive behavior, including
empty, loading, unavailable and playing states. Local implementation and tests
can proceed to produce a reviewable result; visual acceptance remains pending
until the user approves it.

## Final acceptance

- Re-run relevant Go race/lifecycle checks, TypeScript/build checks, PostgreSQL
  and authorization integration tests and browser workflows.
- Complete native MediaMTX, adaptive upstream, direct and forced-relay network
  qualification. Investigate the retained source-congestion failures and reject
  runs with host scheduling interference; never relax gates to get a pass.
- Keep a curated validation record with revisions, measured results, failed
  attempts and remaining hardware/identity-provider qualification boundaries.
- Verify all documented commands and default/public versus internal profiles.
  No production deployment or public-demo configuration change is included.
