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

## Added: optional source format adaptation

Resolution and frame-rate adaptation is a general-purpose WebRTC technique.
[Libwebrtc's adaptation model](https://webrtc.googlesource.com/src/+/HEAD/video/g3doc/adaptation.md)
separates resource observations, adaptation decisions and restrictions applied
by a source. Follow that separation without attempting to recreate libwebrtc.

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

Prerequisite under investigation: the existing 1080p30 x264 sample produces
an SPS with level 4 (`42c028`, confirmed with GStreamer 1.28.6), while the
configured SDP uses level 3.1 (`42e01f`). Audit actual offer/answer receive
capabilities and source limits under RFC 6184 before enabling format upgrades.
Changing the advertised level alone does not establish that a receiver can
decode it; retain this as an open compatibility issue until negotiated limits
and real decoded output are checked on direct and MediaMTX paths.

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
full-page mode. Desktop/mobile screenshots have been presented; visual
acceptance and final live-distribution qualification remain pending.

Use the existing metrics endpoint to provide bounded, read-only source readiness,
reader count and ingress/egress rates. Scope returned values to the authorized
device, cache/coalesce server requests and keep credentials/server topology out
of browser responses. Do not expose the administrative API. Validate unavailable,
idle and counter-reset states; avoid misleading zero rates on the first sample.

## Separate lot: recent recording and playback

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
