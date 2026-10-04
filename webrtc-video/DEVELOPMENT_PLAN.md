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

## Implemented: project discovery

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

The live-engine integration check passes with an actual CLI-backed producer,
real project APIs and producer HTTP controls, production Next.js routes and an
isolated PostgreSQL database. It verifies shared inventory without enrollment,
nonmember denial, source-wide preset selection and stale-write rejection,
reconnection/rename with a stable device ID, and history versus live-only
behavior. Offline history cannot authorize source control or viewer credentials.
Inventory/control reads leave the encoder idle. Only GitHub membership is
substituted; actual OAuth/SSO approval and discovery-backed MediaMTX playback are
not established by this check. Its scoped temporary tunnel, containers and
private CLI context are removed on completion. The non-playback check passes at
clean revision `ce9fef5`. An optional Chromium path additionally opens the actual
discovered producer, observes 720p decoded-frame progress, and checks one active
encoder followed by shutdown after browser closure. The clean repeat at `6e752d3`
passes with a browser relay candidate, 29.60 fps and one encoder. After abrupt
browser termination the encoder stops in 36.876s, including ICE disconnection
detection and the configured recovery grace period. The earlier dirty-tree run
also passes at 30.94 fps but is retained only as development evidence. The clean
run uses adequate UDP socket limits, restores the original values and leaves no
test containers. This short functional observation does not establish
capture-to-display latency or impaired-network performance.

The [combined discovery/organization/MediaMTX check at `0b88ec6`](./platform/qualification/evidence/0b88ec6/organization-discovery.json)
now passes with two separate member sessions, about 29.95 fps per reader at
720p, two MediaMTX readers and one encoder. The source permits only one viewer,
so both readers necessarily share the adapter's upstream session. A quality
change propagates to the other member, a nonmember cannot obtain viewer or
metrics access, and the remaining reader continues after the first leaves.
Abrupt closure of the last browser stops the encoder in 35.979 s, including
disconnection detection and on-demand shutdown. History/rename/offline denial
and live-only inventory also pass. The direct regression passes on the same
producer image. Both use a clean checkout and restore socket limits/remove
owned containers. GitHub membership remains substituted; these short functional
observations do not qualify actual OAuth/SSO, forced relay or resource/latency
budgets.

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
The [automatic-ladder trials at `7cb827b`](./distributor/qualification/evidence/7cb827b/automatic-formats.json)
now observe the source quality API independently from decoded dimensions and
cadence. The 4 Mbit/s MediaMTX source case passes 720p30 → 540p24 → 720p30
without replacing its encoder/session or regressing sampled media time. Its
capacity transition freezes total 1.416s; subsequent playback has no freezes.
At 1.5 Mbit/s, direct and MediaMTX both reach 360p15 but remain there after the
90-second recovery window. Direct also exceeds the unchanged transition-freeze
bound (3.231s versus 3s); MediaMTX has 2.316s transition freezes. Changing just
the test content from SMPTE to snow does not resolve the recovery failure.
Removing just the encoder's 10% increase threshold recovers 6 Mbit/s and reaches
540p24, but misses the final 720p return. This is an informative failed trial,
not a promoted policy. All five cases retain identical runtime images, private
path-scoped observation credentials and complete teardown/resource evidence.
Temporary host socket settings are restored and owned containers are removed.

The [acknowledged-rate trials at `468637b`](./distributor/qualification/evidence/468637b/acknowledged-rate.json)
retain two more failures with the bounded arrival-ordered native rate window.
The default threshold leaves 360p and records a 194 ms baseline freeze; setting
only the threshold to zero recovers 6 Mbit/s and reaches 540p, but not the final
720p format. Runtime/host gates pass; the baseline freeze is not discarded.
Diagnostics identify a separate unit mismatch in the sample adapter: FlexFEC
is intentionally untracked, but the native target was divided by its ratio.
A deterministic primary/FEC writer and TWCC test measures 816 kbit/s of tracked
RTP (1000 payload bytes + 20 header bytes each 10 ms), excludes untracked FEC,
and reproduces an 800 kbit/s target incorrectly becoming 666666 bit/s.

The isolated correction keeps GCC and encoder targets in tracked-stream units
and adds the configured FEC allowance once at the pacer boundary. Rate metrics
now explicitly identify tracked RTP, including its headers and excluding
untracked FEC and outer transport headers. The regression, full producer race
suite, vet and formatting checks pass. The initial full suite also exposed a
pacer test reading counters before its writer returned; joining the worker fixes
that test and passes 100 repetitions. Native fork race/lint, distributor race
and real MediaMTX integration passed before the adapter correction. Its next
step is unchanged-profile live qualification, not a claim of complete recovery.

The [five delivery trials at `7c20680`](./distributor/qualification/evidence/7c20680/rtp-budget.json)
confirm complete 360p → 540p → 720p30 recovery with the unchanged profile in
both direct and adaptive MediaMTX modes. They still fail initial-transition
playback: 2.798s disruption with MediaMTX (including 1.268s after the transition
window) and 3.149s directly. Neither has recovery freezes or decoded-frame drops.
Changing only downHold from 3s to 1s yields one complete MediaMTX pass with
1.870s transition freezes, but its repeat retains a 0.579s late freeze and fails.
The direct 1s trial loses its phase-control file at the end of recovery and is
incomplete despite final 720p30 observations. Existing runs retain their original
results and all acceptance gates remain unchanged. Socket limits are restored and owned containers removed.
The tracked-stream accounting fix is retained; 1s downHold remains experimental.

The [continuous downshift-evidence trials at `b9b77ad`](./distributor/qualification/evidence/b9b77ad/downshift-evidence.json)
retain one adaptive MediaMTX pass and one direct failure with the same private
one-second down-hold profile. Both recover 720p30 and their source-rate target;
neither drops decoded frames or freezes during recovery. Direct initial
disruption is 3.065s, above the unchanged 3s bound, and its last freeze is reported
after the transition grace. The source trial's initial disruption is 1.676s,
with no steady-state freezes. The source pass is not yet a repeated series;
public down-hold remains three seconds and qualification remains incomplete.

The [collector-local phase publication check at `f6555f4`](./distributor/qualification/evidence/f6555f4/phase-control.json)
passes 100 atomic updates with 5,630 concurrent reads while removing the host
source between updates. The repeated direct trial completes all collection and
teardown steps with identical media images. It still fails playback: an initial
2.539s freeze is reported after the transition window, followed by a 0.243s
freeze during constrained delivery. There are no recovery freezes. This removes
the live bind-mount dependency; it does not prove the original ENOENT cause.

A deterministic policy regression additionally reproduces the down-hold timer
restarting whenever congestion crosses another lower rung, although the active
format is continuously unsupported. Downward evidence now survives changes
among lower candidates, while sufficient bandwidth, missing estimates, manual
selection and confirmed transitions still reset it. Upgrade hold, minimum dwell,
all thresholds and reference configuration remain unchanged. The full producer
race suite, focused policy checks, vet and formatting pass. Delivery validation
of this timing correction remains required.

Review of the remaining abrupt-capacity delay identifies an old fork rule
that floors each overuse reduction at 85% of the previous target, even when
received throughput is far lower. With a 6 Mbit/s target and 1.2 Mbit/s received,
a deterministic test retains 5.1 Mbit/s after the first congestion report and
still 2.263 Mbit/s after five further reports. Published interceptor revision
`94d4709` restores the original Pion AIMD decrease to 85% of received throughput
(1.02 Mbit/s in that case), capped by the current target so overuse cannot
increase the rate. The existing 200ms repeat interval and throughput-bounded
recovery remain. This follows the receive-throughput backoff used by
[Pion](https://github.com/pion/interceptor/blob/master/pkg/gcc/rate_controller.go)
and [libwebrtc](https://webrtc.googlesource.com/src/+/refs/heads/main/modules/remote_bitrate_estimator/aimd_rate_control.cc);
it does not claim the sample implements the full libwebrtc controller.
The new regression fails before the change and passes afterward, with the full
native race suite (Go 1.24.6) and zero golangci-lint 2.10.1 issues (Go 1.25.6).
Producer and distributor pin the same published version with verified checksums;
both full race suites and the sample GCC regression target pass. The separate
bridge race integration suite also passes against the MediaMTX 1.21.1 binary.
The [four live trials at `4cf2b68`](./distributor/qualification/evidence/4cf2b68/received-throughput-backoff.json)
retain three passes and one failure. The reference three-second down-hold passes
with MediaMTX (1.435s initial freezes); direct delivery has 2.031s total freezes
and still fails on a 0.589s counter increment collected after the transition
cutoff. Both return to 720p30. With only downHold set to one second, direct and
MediaMTX each pass every gate (1.548s / 1.316s initial freezes), with no steady or
recovery freezes and no decoded-frame drops. The one-second setting still needs
repetition and is not promoted to a public default. All acceptance thresholds
remain unchanged; temporary socket settings and owned containers are cleaned up.

For the direct reference failure, retained frame-callback diagnostics place the
nearby display gaps before the steady cutoff, while the cumulative freeze count
is sampled afterward. This suggests an attribution limit of one-second stats
sampling near a phase boundary. Those browser observations have different
semantics: investigate precise attribution without reclassifying the failed run
or replacing the current gate with an unqualified correlation.

Next, stabilize the initial capacity-drop transition and repeat direct/MediaMTX/TURN qualification. Do not
claim that one successful fast-down run establishes a stable policy. Before adopting
a further threshold or controller change, inspect acknowledged throughput, increase
mode, receive-time ordering and encoder undershoot; a clean loss signal alone
does not prove spare capacity. If application limitation is established,
compare with libwebrtc's bounded [probe controller](https://webrtc.googlesource.com/src/+/refs/heads/main/modules/congestion_controller/goog_cc/probe_controller.cc)
and [probe-rate measurement](https://webrtc.googlesource.com/src/+/refs/heads/main/modules/congestion_controller/goog_cc/probe_bitrate_estimator.cc)
instead of blindly raising the estimate or replacing the whole controller.
Repeat automatic-format qualification after the targeted correction.
Capture-to-display latency and isolated CPU measurements remain pending.
These checks do not establish arbitrary hardware support or AV1 switching.

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
profile path checks both declared level and per-profile bounds. Tests check actual SPS/decoded
frames and real Pion negotiation, including rejection of level-4 senders by
level-3.1 receivers and acceptance by compatible receivers. A custom 1080p
level-4 pipeline still encodes/decodes in the native test. The full producer
race suite passes. Historical 1080p network evidence remains unchanged and is
identified as historical; it never proves current-profile or SDP conformance.
Live qualification at `0bc9637` passes the provisioned rstream tunnel, two
MediaMTX readers sharing one encoder, Low/Medium/High/Auto selection, stale-write
409 rejection, deliberate distributor failure, direct fallback and MediaMTX
recovery. The browser reports 1280×720 through all three playback observations.
Measured MediaMTX ingress is 0.928/3.825/8.823 Mbit/s for Low/Medium/High on the
unshaped path; the browser diagnostics gate reports no unexpected events.
This is not a latency, impaired-network or physical-camera qualification.
Native MediaMTX qualification of the current profile passes as described below.
Forced-relay and network-impairment qualification remains required.

### Current-profile source congestion investigation

At clean revision `7cbb3c1`, three 720p30 adaptive MediaMTX runs limit only the
producer-to-adapter path to 5 Mbit/s. Two fail continuity: the encoder recovers
too aggressively while the link remains limited, causing secondary freezes.
All three pass host/runtime integrity and recover their initial target after
the limit is removed. These are genuine retained failures, not host exclusions.

The existing Pion interceptor fork has a local candidate at `7516c4b` that
bounds both multiplicative delay recovery and the combined loss/delay increase
by observed throughput. Decreases remain immediate. Regression tests reproduce
both defects before correction; the full interceptor race suite passes. This
follows the receive-rate bound in
[libwebrtc's rate controller](https://webrtc.googlesource.com/src/+/main/modules/remote_bitrate_estimator/aimd_rate_control.cc),
without adding another encoder ramp or changing the sample's loss-hold settings.

Three diagnostic runs with that exact candidate and identical images pass the
unchanged gates. During the 15-second capacity phase they decode 25.5–26.4 fps;
initial transition freezes total 2.061–2.099s, with no later freezes or decoded
frame drops. Recovery reaches 7.57–8 Mbit/s from an 8 Mbit/s baseline. The first,
incomplete correction remains a failed run with a secondary 0.842s freeze.
The sample dependency is still pinned to its existing published revision;
these local-override runs are explicitly non-publishable. Sustained congestion,
dependency integration and fresh qualification of the resulting clean sample
remain required. None of these results measures capture-to-display latency.

The 60-second capacity extension at sample revision `ecafac3` rejects that
candidate: two secondary freezes total 1.332s. A further deterministic regression
finds that continuous traffic can remain in one arrival group indefinitely,
preventing delay measurements. Local fork revision `5dea212` corrects the burst
comparison, bounds compressed bursts to 100ms and ignores missing-packet arrival
timestamps. The full interceptor race suite passes. Three 60-second diagnostic
runs then decode 29.06–29.21 fps with no decoded-frame drops. Two pass all gates;
one fails because of a secondary 0.202s freeze, without an accompanying measured
loss or retransmission increase. Its cause remains unresolved; the series remains
failed. Initial transition freezes total 1.956–2.007s. Runtime and socket-buffer
checks pass, but they do not establish absence of host scheduling delays. The
original failures and all local-override artifacts are retained. Reproducible
dependency integration and fresh qualification are still required.

Local fork revision `121cf80` additionally compares completed send and arrival
edges when calculating inter-group delay. A deterministic constant-delay test
previously produced false +2/-3/+4ms measurements as sender bursts varied in
length. Another regression verifies that packet reordering cannot move the
group's maximum send time backwards. Both now pass, as does the full interceptor
race suite. Group formation still uses its first send time, following the
separation in
[libwebrtc's inter-arrival calculation](https://webrtc.googlesource.com/src/+/a09331a6038bb6191c7662680d8928940463a099/modules/congestion_controller/goog_cc/inter_arrival_delta.cc).
The qualification collector now retains bounded frame-callback and event-loop
timing diagnostics to investigate residual pauses. At sample revision `96826ad`,
three further 60-second capacity runs decode 29.07–29.12 fps with no secondary
freeze during the constrained phase. Initial disruption totals 1.925–2.016s.
Two runs pass all gates; one fails because of a 0.210s recovery-phase freeze.
That event coincides with a 184.1ms JavaScript timer delay; the affected frame's
receive timestamp precedes the display pause. This supports receiver execution
delay as a contributor, without establishing its underlying cause. The failed
run and series verdict remain unchanged. The other runs' maximum timer delays
are 3.0/3.8ms. These results do not establish the earlier 0.202s freeze's cause,
and the local dependency override is still not integrated or publishable.

The combined correction is now published on the fork's review branch as `7504ea0`
([draft PR #4](https://github.com/rstreamlabs/pion-interceptor/pull/4)). It includes
the fork's existing test-dependency update and the adapter's existing hardened
`Decoder03` commit `195b942`. An initial attempt to use the GCC-only fork revision
did not compile the adapter because that decoder lived on a separate historical
branch; the combined revision preserves its implementation and tests unchanged.
The combined fork passes the full race suite with
Go 1.24.6 and golangci-lint 2.10.1 with Go 1.25.6. Producer and distributor now
pin the same downloadable pseudo-version, with module checksums; the producer's
GCC regression target exercises the entire package, including the new cases.
The producer's full race, GCC and no-embedded-web suites pass with that pin, as
does the distributor's full race suite. Its separate `-race -tags=integration`
suite also passes against the actual MediaMTX 1.21.1 binary, exercising native
and adapter sessions, shared readers, FlexFEC/RTX repair and session recovery.
Both module sets pass `go mod verify`.
The earlier local-override evidence remains historical. The fresh canonical
[matrix at clean revision `b5c3889`](./distributor/qualification/evidence/b5c3889/matrix-summary.json)
uses ordinary builds with the published dependency and identical images across
three repetitions of every profile. Its overall verdict is **failed**. Fan-out
to one/four/eight readers passes, as do all three delay/jitter/loss checks on
each MediaMTX leg (60ms/15ms/1%); the source-leg checks decode 29.83–30.18 fps
without freezes. The 4 Mbit/s source-capacity series has no secondary freezes
and always recovers 8 Mbit/s, but two runs fail: one drops four frames (1.15%
in the steady-state window), and one averages 23.73 fps, below the 80% cadence
gate. The third passes. Direct capacity and impairment each pass only one of
three runs; direct capacity disruption reaches 3.333s, above the 3s bound.
All runs pass runtime/environment integrity. The expected failure of an
undersized single-rendition viewer remains distinct from these unqualified
source/direct transitions. No gate was relaxed or failed run omitted. Temporary
UDP limits are restored and owned containers removed. Investigate repair timing
and transition behavior before claiming network qualification; latency and
resource comparisons remain separate work.

The repair-timing review reproduced two deterministic defects: retry deadlines
were frozen at the RTT when the previous packet was sent, allowing premature
retries after an RTT increase and unnecessarily delaying them after a decrease.
Retries now compare the actual send time with the current smoothed RTT; bounded
history survives later RTT increases. The fork's published revision `567a553`
also exposes its existing transport-feedback RTT to an optional pacer observer,
so repair timing no longer waits solely for periodic receiver reports. A real
estimator/pacer regression fails with the previous pin and passes with the new
one. Both modules use the same downloadable revision and pass checksum
verification. Producer race, GCC and no-embedded-web suites, distributor race
and separate MediaMTX 1.21.1 integration suites, and the fork's full race/lint
checks pass. Live network measurements are still required to establish the
effect on the retained transition failures. No queue, playout buffer, encoder
ramp or acceptance threshold changed for this correction.

The [targeted repeat series at clean revision `788b2e1`](./distributor/qualification/evidence/788b2e1/rtt-summary.json)
uses identical images and three runs of each affected profile. Five of nine
runs pass, so the series remains **failed**. Direct and source capacity each
pass twice; their third runs decode 23.78 and 23.45 fps respectively, and the
source run accumulates 3.095s of freezes. Direct delay/jitter/loss passes once;
the other runs accumulate 0.387s and 0.497s of freezes. Every run passes runtime
and host-integrity checks and recovers the 8 Mbit/s source target. UDP limits
are restored and owned containers removed. The RTT correction is supported by
its deterministic regressions, but this series does not establish stable
network behavior or a causal improvement over the earlier random-loss runs.

Further review reproduced a separate transport-feedback parser defect in the
fork: an unknown/evicted packet neither consumed its receive delta nor stayed
out of the loss samples. This shifts later arrival timestamps and manufactures
loss. Regression tests cover run-length, status-vector and multiple-chunk
feedback. Fork revision `418b2a2` consumes every received delta while returning
only known send-history entries, preserves wire sequence advancement and
rejects missing deltas. Its full Go 1.24.6 race suite and golangci-lint 2.10.1
checks pass; it is published on the draft review branch but is not yet pinned
separately by the sample.

The history regression then reproduces eviction of 750 out of 1,000 packets
in a half-second 8 Mbit/s flight. Fork revision `2c11e8a` retains unresolved
metadata for up to 60 seconds as new packets arrive, with a separate hard cap
of 32,768 entries, and releases received entries after valid feedback. This
follows the receipt-retirement and time-window policy in
[libwebrtc's feedback adapter](https://webrtc.googlesource.com/src/+/main/modules/congestion_controller/rtp/transport_feedback_adapter.cc)
while additionally bounding the number of entries. No media payload is retained.
Tests cover delayed and overlapping feedback, late receipt, expiry, the count
bound, RFC 8888 receipt retirement and malformed feedback without partial
retirement. The fork's full race/lint checks pass. Three local metadata-path
benchmarks measure 166–173 ns/packet versus 186–187 ns/packet before this change,
with unchanged allocations, on Apple M1 Max; these are not complete source
CPU/memory or media-latency measurements.
Both producer and distributor now pin the published `2c11e8a` revision,
including the parser correction. Their checksum, race, GCC, no-embedded-web
and separate MediaMTX 1.21.1 integration checks pass. The repeated live network
failures above remain unchanged evidence; integrated network qualification of
this pin is the next step.

The [four targeted cases at clean revision `f1d38d2`](./distributor/qualification/evidence/f1d38d2/feedback-summary.json)
use this published pin and identical image digests. Three pass: the 4 Mbit/s
source and direct paths, and source-to-adapter 60 ms delay / 15 ms jitter /
1% loss. The direct delay/jitter/loss path fails with 0.483 s of constrained
freezes and 0.387 s during recovery. All four pass runtime/host integrity and
recover their 8 Mbit/s target; socket limits are restored and owned containers
removed. There is one run per case, not a completed repeat qualification.
The impairment traces also show double-digit reported loss despite about 1%
qdisc drops; whether temporary missing/overlapping feedback accounts for this
is under investigation. No acceptance gate is relaxed and no causal improvement
is inferred from these single random-loss trials.

A deterministic feedback regression then reproduces 26% estimated loss and an
8-to-6.96 Mbit/s reduction for one missing packet out of 300, with the remaining
missing reports resolved by late receipts. Fork `f95fe4f` records whether a
packet was previously reported missing and reconciles first reports, repeated
reports and late receipt within a bounded 250 ms send-time observation. This
uses the observation policy in
[libwebrtc's loss estimator](https://webrtc.googlesource.com/src/+/main/modules/congestion_controller/goog_cc/loss_based_bwe_v2.cc),
not its complete bandwidth-estimation algorithm. The corrected regression
measures 1/300; persistent 30% loss still reduces bitrate and subsequent clean
observations recover it. Late receipt after a completed observation is not
counted again in the next observation. A 32,768-packet count bound also protects
metadata during pathological timing. No media buffering is added.

Completed loss observations now publish the target, pacing rate and callback
even without a delay-measurement callback. A second sample regression proves
that the old raw-status guard could reduce bitrate for feedback with no matching
sent packets. The sample removes that redundant controller and preserves its
existing diagnostic field names as aliases of the GCC loss controller, with
updated metric help and README text. Qualification loss-response/fidelity
checks use reconciled loss instead of raw repeated not-received symbols; their
loss thresholds remain unchanged, and tests retain rejection of persistent
high loss without a bitrate reduction. The four preceding live results remain
unchanged evidence.

Both modules pin the published `f95fe4f` revision. The fork's full race/lint
checks, producer/distributor race suites, no-embedded build, separate actual
MediaMTX 1.21.1 integration, and 92 qualification/sanitizer checks pass. New live
network qualification is required before attributing improvements to this change.

The [four targeted runs at clean revision `42761dc`](./distributor/qualification/evidence/42761dc/loss-summary.json)
pass their existing end-to-end gates with identical images. Source/direct
4 Mbit/s capacity transitions have 2.006/2.606 s of disruption and no steady
frame drops. Source 60 ms delay / 15 ms jitter / 1% loss has no freezes; the
direct case has no constrained freezes and 0.536 s during recovery, within the
existing recovery gate. All restore the 8 Mbit/s target and pass runtime/host
integrity. Socket limits are restored and owned containers removed.
These are single runs, not repeat qualification. In particular, median loss
estimates remain 17.63% directly and 6.53% to the adapter while the qdiscs drop
about 1.15% of packets. The corrected deterministic defects do not fully explain
this live discrepancy. Further evidence must distinguish late receipt across
observation boundaries, retransmission feedback and actual receiver-side drops;
at that stage, none was established as the cause. The unresolved fidelity issue
prevents a stabilized result despite the four passing media runs.

Subsequent [bounded diagnostic captures](./distributor/qualification/evidence/42761dc/late-receipt-diagnostic.json)
separate two accounting defects. On the
source-to-adapter path, 388 of 464 packets counted as lost in selected completed
observations are later reported received, typically about 100 ms after closure.
Those late receipts must still correct the signed expected/received counts,
without entering the denominator twice or carrying unused negative loss into a
future completed interval (the accounting principle in
[RFC 3550 Appendix A.3](https://www.rfc-editor.org/rfc/rfc3550.html#appendix-A.3)).
No media buffer or longer observation period is required for that correction.

The [direct-path routing capture](./distributor/qualification/evidence/42761dc/feedback-routing-diagnostic.json)
observes 1,593 consecutive decrypted TWCC reports but only 1,506 at the
controller. All 87 omitted reports target the RTX SSRC. The native sender reads
only its primary SRTCP stream, despite feedback on a repair SSRC describing the
same transport sequence space. A real encrypted-peer regression reproduces the
omission; the sender fix merges primary/RTX/FEC control readers, with
bounded storage, joined shutdown, deadlines and multi-destination report
deduplication. The source and direct accounting fixes require new uninstrumented
network qualification. Synchronous diagnostic captures are retained as diagnosis
only and cannot substantiate latency or CPU improvements.

The sample pins `d17c867` in both Go modules, including the signed-loss and
arrival-ordered bounded-rate corrections, and the [sender RTCP fix](https://github.com/rstreamlabs/pion-webrtc/pull/1)
at `ab2ba524` in the producer. The distributor keeps its separate FlexFEC
receiver fork. Full race suites and lint pass in both affected native forks;
producer/distributor race suites, no-embedded mode and real MediaMTX 1.21.1
integration also pass. The sender suite initially hit two existing incomplete
ICE-signaling fixtures; the statistics hang reproduces with the original sender
code. Both fixtures now exchange gathered candidates, with bounded waits and
cleanup, and the complete suite passes without excluding tests. Delivery-path
qualification after these corrections is still required.

The [first four uninstrumented runs at `96b5feb`](./distributor/qualification/evidence/96b5feb/feedback-summary.json)
pass unchanged gates with identical images and clean runtime/host checks.
Direct and source delay/jitter/loss now have median GCC loss estimates of
1.88% and 2.65%, versus 17.63% and 6.53% in the preceding series. Signed interval
clamping and feedback timing mean these estimates are not exact netem wire-drop
ratios. Direct impairment has a 0.255 s transition freeze; source impairment has
none. Source/direct capacity transitions have 2.123/2.859 s of disruption, with
no steady frame drops, steady freezes or recovery freezes. GCC recovers its
8 Mbit/s target in all cases; the source-impairment encoder holds 7.422 Mbit/s
within its configured update hysteresis, while the other encoders return to
8 Mbit/s. Socket limits are restored and owned containers removed. The direct
capacity result is close to its existing transition gate, so repeats remain
necessary before claiming stable delivery. No full-matrix, latency or resource
comparison is inferred from these four passes. The native PRs currently report
no remote CI checks; the passing race/lint results above are local verification.

The [two additional repeat series](./distributor/qualification/evidence/96b5feb/feedback-repeats.json)
retain eleven passes and one failure across all twelve trials. The repeats use
clean documentation-only revision `f762165` and the exact same three image
contents as `96b5feb`. Direct/source capacity and source delay/jitter/loss pass
three of three. Direct impairment passes two of three; its third trial has two
transition freezes totaling 0.741 s, above the unchanged 2% phase gate. Browser
frame callbacks confirm 550 ms and 200 ms gaps, with no corresponding JavaScript
timer stall. Steady playback and recovery have no freezes or decoded-frame
drops. This is retained as a delivery failure requiring investigation, not
classified as host interference. Socket limits are restored and owned
containers removed after the series. Stable delivery is not yet established.

The discovery configuration review additionally reproduced valid display names
being rejected or changed when environment expansion encounters YAML quotes or
backslashes. A literal scalar preserves those names; the real configuration
loader regression covers quotes, backslashes, punctuation and UTF-8.

A deterministic concurrency regression also found that an idle pacer awaiting
the FEC packet for its last protected group did not wake for RTX. With no new
media/FEC, a retransmission stayed queued indefinitely. The targeted correction
adds RTX to that wait; the existing scheduling ratios, admission bounds and
repair expiry remain unchanged. The original implementation fails a virtual-
time `synctest` regression, and the complete producer race suite passes with
the correction. The [broader retransmission-priority experiment](./distributor/qualification/evidence/f2d9a28/repair-priority.json)
is rejected after two passes and two failures with unchanged acceptance gates.
Direct capacity has 3.289 s of disruption, including 0.935 s of steady-phase
freezes; source capacity drops six steady frames (1.749%, above the 1% bound).
Both delay/jitter/loss trials pass, but a single randomized run does not establish
an improvement. The experiment remains on its isolated branch; only the narrow
wakeup correction is retained in the main sample.

The [first full direct/symmetric-TURN pair at `4552d30`](./producer/qualification/adaptive-streaming/evidence/4552d30/direct-relay-regression.json)
retains two failed overall verdicts. Both use the same 720p30 producer/browser
images, FlexFEC 5/1 and a zero requested playout-delay hint. This longer profile
steps down to 4 Mbit/s, then adds 120 ms delay, 30 ms jitter and 2% loss; it is
not a repeat of the shorter distributor profile. Direct passes delivery and
controller checks but fails encoder cadence on one 219.774 ms recovery output
gap, above the unchanged 200 ms bound. Host CPU/sampling checks pass, so its
cause remains unassigned. TURN is confirmed on both peers and both candidate
ends, but impaired playback freezes for 9.652 s (29.04%) and fails recovery
time, sustained recovery and receive-throughput recovery. Its encoder ends at
3.1 Mbit/s versus the stable 6.8 Mbit/s reference. Neither result is accepted;
the limits remain unchanged. Socket limits are restored and owned containers
removed. This pair does not isolate the wakeup change or establish a performance
regression from it. Further relay/recovery and source-cadence diagnosis remain.

The [encoder increase-policy trials at `05921e7`](./producer/qualification/adaptive-streaming/evidence/05921e7/encoder-increase-policy.json)
do not justify changing production defaults. Removing the five-second hold and
raising the loss threshold together yields one faster recovery (14.130s versus
34.272s), but fails the unchanged continued-pressure gate: a 100% threshold
cannot supply its required above-threshold observation. Two narrower zero-hold
trials retain the 1% threshold. One encounters substantial unshaped-path loss,
stays at the bitrate floor and never exercises recovery; its loss source is
unresolved. The other recovers in 24.205s but fails impaired playback freezes
(6.491s, 18.96%), host sampling (381ms maximum gap) and encoder cadence
(205.530ms maximum gap). These are retained failures, not evidence of a stable
policy or grounds to loosen thresholds. Temporary UDP buffer settings are
restored and all experiment-owned containers are removed.

## Cross-cutting latency and resource criteria

The optional qualification-only pixel timestamp probe is now maintained in
`producer/qualification/adaptive-streaming/latency`, replacing its local proof
of concept. It stamps I420 pixels immediately before encoding and reads a small
crop at 5 Hz through browser frame callbacks. Reports retain raw observations,
phase distributions, sampling overhead and invalid/incomplete runs. A shared
Linux boot identity, wall/performance clock agreement and marker integrity are
required. Its scope excludes camera exposure, capture/scaling before the stamp
and physical display scanout. Production pipelines and buffering are unchanged.
The 100-test harness suite, artifact sanitization and result/negative-gate tests
pass; the plugin builds on Linux and macOS and survives actual H.264 encode /
decode at 500 and 8000 kbit/s. The first clean-checkout launch rejected an
untracked profile before starting media; the profile is now explicitly committed.
The [five pilot runs at `dd2e37c`](./distributor/qualification/evidence/dd2e37c/latency-pilot.json)
pass: latency-enabled direct, custom-adapter and native MediaMTX, plus direct /
adapter probe-disabled references. Every instrumented phase satisfies clock,
marker and sample-count gates. Stable-phase median / p95 are 58.1 / 99.3 ms
direct, 43.8 / 44.8 ms with the adapter and 40.2 / 41.0 ms natively. These single
observations do not rank the paths: realized source rates differ (8, 7.586 and
5 Mbit/s), as do their control policies. Canvas readback materially increases
browser memory (about 553 MiB peak versus 326 MiB in the direct reference),
which is retained as measurement overhead. Production readers do not use it.
Repeated controlled resource comparisons and impaired-network latency remain
pending; the earlier direct impairment freeze remains an open delivery failure.

The [four initial impaired-network latency trials](./distributor/qualification/evidence/f654989/latency-network.json)
retain two passes (source delay/jitter/loss and direct capacity), one direct
impairment delivery failure (0.674 s transition freezes), and one source-capacity
measurement failure. These use 30 s phases for sufficient marker samples across
capacity transients, not the earlier 15 s profile. The source-capacity timestamp
regresses by 200.213 ms to an earlier checksum-valid marker while RTP/media time
advance. The cause remains unproven; its latency distribution is invalid, not a
publishable delay bound. The collector now retains a complete report when marker
gates fail, allowing the runner to collect teardown/resource/media evidence
before its unchanged latency gate fails. Frame-latency percentiles must be read
alongside freeze durations because callbacks sample newly presented frames.

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
playback still uses no URL credentials.

The optional replay UI now uses separate bounded MP4 clips, a timeline that
preserves gaps, and an explicit return to the existing live WebRTC session.
Source quality is hidden during replay and controls remain below the picture.
Closing replay cancels its media load; unmounting or hiding the page also
cancels index reads. Returning to a visible page restores replay paused;
expiry and unavailable indexes have
explicit states. Browser checks pass in Chromium, Firefox and WebKit for actual MP4
decoding, contiguous clips, stop-at-gap behavior, keyboard seeking, responsive
layouts, expiry, index outage/recovery, visibility cancellation and return to the
same live video/MediaStream without renegotiation. These checks use controlled
recording responses; native MediaMTX storage and playback are covered separately.
The platform's 172 unit checks, TypeScript check and production build pass, as
do the real PostgreSQL/Next.js route/access checks. Fresh desktop/mobile captures
have been presented and the user has accepted the replay presentation in normal
and full-page layouts.

The clean revision `73764aa` passes real provisioned-source recording/replay,
return to the same live session, a full 512 MiB recording tmpfs, and subsequent
MediaMTX outage/direct fallback/recovery. Two readers share one encoder. During
the 20-second storage fault and 307-second recovery, presented cadence remains
30 fps; the largest presentation gap is 66.7ms, with no media/RTP timestamp
regression or stream replacement. A new closed segment decodes within 11.226s
after space is freed; the full index recovers after 307.221s. The private MP4
request releases its server admission slot 44ms after return-to-live.

This exposed a native MediaMTX limit: ENOSPC leaves incomplete segments, and
the index remains unavailable while one is included in its requested window.
Recovery occurs when the window excludes them or retention removes them.
The original immediate-index-recovery run remains a failure; the documented
fault contract now distinguishes a 30-second recorder recovery bound from an
eight-minute index bound (5m retention + 2.5m cleanup interval + margin), with
unchanged live frame-rate/gap gates throughout. No native segment is removed
by the qualification. Earlier harness failures are retained: paused Chromium
media requests do not reliably settle Playwright's `response.finished()`,
and Docker cannot copy a tmpfs through `docker cp`. The harness observes
actual private-fetch release and copies bounded bytes inside the container.
This qualifies live continuity on the tested path, not capture-to-display
latency or recording CPU/memory overhead. Those measurements remain.
Application-level replay now also passes in Firefox 155 on Linux with the same
controlled-media UI checks; the local macOS Firefox ICE fixture failure remains
documented separately below.

MediaMTX 1.21.1 passes native distribution and recording-storage qualification.
The release binary and
container image are pinned/verified; JWT/session/metrics, browser-origin checks,
the bridge race integration suite, real MP4 decoding in Chromium/Firefox/WebKit
and native retention checks pass. Native WHEP now advertises RTX. A deliberate
primary-packet loss proves actual RTX retransmission and delivery to both
readers; the producer negotiates it without a production-code change. Native
source pacing remains fixed and FlexFEC stays on the adaptive adapter path.
The initial capability guard correctly failed on this change; current native
qualification explicitly requires upstream RTX, while downstream remains NACK
and TWCC. Three live native-distribution runs at clean revision `02faff8` pass
with identical images: no decoded-frame loss or freezes, one producer session,
upstream RTX/NACK/TWCC and complete source teardown. Sampled producer CPU peaks
range from 0.349 to 0.373 core and MediaMTX from 0.045 to 0.050 core on this test
host; these are not hardware-independent budgets or capture-latency measures.

The first 1.21.1 recording regression at `02faff8` passes the storage-fault
subtest: 30 fps throughout, maximum presentation gap 66.7ms, a decodable closed
segment after 6.868s, index recovery after 302.484s and private MP4 release 43ms
after return-to-live. Quality selection, direct fallback and distributor recovery
also pass. The overall run nevertheless fails its diagnostics gate on two
Chromium index requests reported as aborted despite 200 headers. The harness
now observes complete parsed index bodies and correlates exact URL/method/time,
as it already does for quality JSON; headers alone, timeouts and unrelated
requests still fail. The failed run is retained.

The fresh full run at clean revision `58a09c1` passes with no unexpected browser
diagnostics. It covers quality selection, two readers sharing one encoder, real
replay/return-to-live, bounded storage saturation, direct fallback and MediaMTX
recovery. During full storage and index recovery, presentation remains near
30 fps with a maximum 66.6ms gap; the final recovered observation is 29.2 fps
with a maximum 216.7ms gap. A new closed segment decodes after 7.923s and the
index recovers after 303.535s. The replay request has already released its
server slot before return-to-live. This does not change the native incomplete-
segment limitation or establish capture-to-display latency/resource overhead.

Provide optional short-retention recording and authenticated playback, keeping
WebRTC as the live path. MediaMTX 1.20 supports recording and HTTP playback;
this does not provide seek on the existing WHEP session. Recordings cover only
periods in which a source was transmitting. Preserve the existing on-demand
source policy. The lot includes bounded storage/retention, separate playback
authorization, clear live/history navigation and tests that disk activity does
not harm live latency. Recording stays disabled by default.

## Visual acceptance

The initial discovered-inventory desktop/mobile presentation was accepted.
The recent-recording timeline and controls were also accepted in normal and
full-page layouts after desktop/mobile captures were presented.
Add same-tab full-page viewing with discreet controls, preserved playback,
keyboard/focus handling and restored scroll position. Use a layout expansion
independent of the native Fullscreen API; verify Chromium, Firefox and WebKit,
including mobile layouts, and present the new view for approval.
The requested refinement places controls below the image in both layouts,
keeps quality left-aligned, uses accessible icon-only expand/exit buttons and
keeps the mobile controls on one line with a consistently spaced select chevron.
These layout changes are implemented locally. Related controls/text use 8px
spacing, with 16px between groups and full-width quality descriptions below the
controls. The same 8px spacing covers wrapped device headers and player error /
retry controls, including full-page error padding. Real-media UI checks pass in
Chromium and WebKit on macOS and Firefox 155 on Linux at desktop, narrow mobile
and landscape sizes, including full-page replay and return to the original live
MediaStream. Updated screenshots have been presented; visual acceptance of the
quality/format/metrics refinements remains separate from accepted replay controls.

The initial macOS Firefox fixture still fails ICE, including a minimal media
pair without the application. Preparing preferences before startup did not fix
the full test. Linux Firefox passes with direct TCP access to the test application;
Playwright's HTTP forwarding proxy restricted UDP candidates and is not used.
The optional remote-browser endpoint and fixed application port make this
qualification reproducible without changing application code or user profiles.
Failed local/proxy runs are retained, not classified as successful or skipped.

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
