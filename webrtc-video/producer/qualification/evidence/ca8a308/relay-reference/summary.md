# Adaptive streaming qualification — PASS

Generated at 2026-08-17T01:24:38Z from repository revision `ca8a308907aaa5661954af81d10f04bd8e5f52bb`.

![Adaptive bitrate response](./adaptive-bitrate.svg)

The media controller starts at 5000 kbps and operates from 2000 through 8000 kbps. Its 10% hysteresis keeps a healthy-link target stable once it is close to the configured ceiling.

## Qualification setup timeline

Build time is reported separately from service establishment. The
`connection-started` to `media-connected` interval covers producer startup,
rstream publication, browser startup, signaling, ICE, and first selected media
path; it does not include Docker image builds.

| Milestone | Time since previous milestone |
| --- | ---: |
| runtime-prepared | n/a |
| producer-build-started | 0.0 s |
| producer-build-completed | 126.7 s |
| browser-build-started | 0.1 s |
| browser-build-completed | 20.7 s |
| connection-started | 0.0 s |
| producer-container-started | 0.2 s |
| producer-ready | 1.1 s |
| browser-container-started | 0.0 s |
| media-connected | 12.1 s |

Measured service establishment: 13.4 s.

## Phase summary

| Phase | Samples | Connected | Received kbps (median) | Link use | TWCC kbps (median) | Encoder kbps (median) | Decoded fps | Avg QP | Decode ms/frame | Frozen | NACK | RTX packets | FEC packets | Max RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 100.0% | 7856 | n/a | 8000 | 7300 | 30.0 | 23.9 | 2.30 | 0.0% | 0 | 0 | 2907 | 108 |
| baseline | 25 | 100.0% | 7862 | n/a | 8000 | 7300 | 30.0 | 23.7 | 2.30 | 0.0% | 0 | 0 | 3693 | 108 |
| conditioning | 30 | 100.0% | 7742 | 24.2% | 8000 | 7300 | 30.0 | 24.2 | 2.27 | 0.0% | 0 | 0 | 4461 | 113 |
| constrained | 44 | 100.0% | 2516 | 62.9% | 2000 | 2000 | 30.0 | 30.2 | 2.10 | 4.6% | 246 | 236 | 3472 | 362 |
| impaired | 35 | 100.0% | 2465 | 61.6% | 2000 | 2000 | 29.5 | 31.3 | 2.33 | 2.1% | 196 | 180 | 1947 | 313 |
| recovery | 45 | 100.0% | 5953 | 18.6% | 5900 | 5700 | 30.1 | 25.9 | 2.27 | 0.0% | 0 | 0 | 4908 | 248 |

Congestion response: 0.0 s. Recovery response: 18.1 s.

Selected ICE candidate-pair switches: 0. Both peers remain on the required TURN relay path.

Superseded out-of-order estimator callbacks: 0. The producer always applies the estimator's current target rather than the potentially stale callback payload.

## Qualification decision

| Gate | Observed | Required |
| --- | ---: | ---: |
| Shaper activation | 100.0% of baseline before the first capacity step | at least 80% |
| Rate response | 72.6% reduction in 0.0 s | at least 20% within 30 s |
| Rate recovery | 18.1 s to threshold; 100.0% target residency in the best 10 s window; 11.1 s longest uninterrupted interval; received throughput 76.4% of the stable pre-transition rate | target reaches 80% within 35 s, sustains it for 10 s, and median throughput reaches at least 60% of baseline |
| Playback under impairment | 29.5 fps; 2.1% frozen | at least 20 fps; at most 10% frozen |
| Latency under impairment | 313 ms max RTT; 185.3 ms effective playout buffer | at most 600 ms RTT; at most 300 ms phase-average buffer |
| Visual quality under impairment | QP 31.3; 1920x1080 | QP at most 42; 1920x1080 |
| Loss fidelity | qdisc 2.19%; TWCC 2.36% | 2% injected; TWCC within 8 percentage points |
| Packet repair | NACK 196; sender RTX 211; receiver RTX 180; FlexFEC 1947 | NACK, sender RTX, and FlexFEC greater than zero |
| Sender queue | 224.3 ms residence; 49.5 ms admitted backlog; 0 overflow drops | at most 375 ms; at most 225 ms; zero overflow |

## Congestion-controller diagnostics

| Phase | Loss target kbps | Delay target kbps | Guard target kbps | Peak report loss | Guard reduce / recover | Controller loss | Browser TWCC loss | Encoder updates | Update failures | Feedback packets | Padding statuses | Malformed feedback |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 3 | 0 | 304 | 0 | 0 |
| baseline | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 0 | 0 | 384 | 0 | 0 |
| conditioning | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 0 | 0 | 464 | 0 | 0 |
| constrained | 2024 | 2315 | 2366 | 31.25% | 8 / 1 | 0.00% | 2.89% | 16 | 0 | 679 | 38 | 0 |
| impaired | 2236 | 2239 | 2000 | 8.70% | 0 / 0 | 2.16% | 2.36% | 2 | 0 | 536 | 221 | 0 |
| recovery | 5891 | 5895 | 0 | 0.00% | 0 / 2 | 0.00% | 0.06% | 8 | 0 | 720 | 10 | 0 |

## Real-time sender queue

The sender drops complete encoded access units before RTP packetization when
its sustained-rate backlog would exceed 225 ms. It schedules a recovery key
frame once the bounded queue has room and resumes on that frame, avoiding
partial-frame corruption, multi-second GOP waits, and bufferbloat. Requests caused by one
congestion event or by repeated receiver PLI/FIR feedback are coalesced to one
encoder request per 250 ms, preventing recovery key frames from becoming their
own burst-amplification loop. A rejected recovery request or packetized RTP
rejection remains a hard failure. A sudden estimator decrease is applied to the
encoder and new media immediately. A frame that was already accepted and
packetized drains at its admission rate: slowing or deleting part of that RTP
frame would create sequence holes and multi-second latency. This bounded
transition is exposed by the actual-residence and scheduled-backlog columns.
Queued FEC and RTX are purged on a rate decrease rather than consuming the new
budget for stale repair. A recovery
key-frame request is deferred until the queue has room for the most recently
observed key-frame size plus 25% headroom, avoiding a request that would produce
another key frame only to discard it. Admission accounts for every queued
primary and FEC service interval, plus the single RTX packet that scheduling may
place before each queued frame. Repair packets older than 225 ms are expired
rather than consuming bandwidth after their media window. Expiration is
reported separately from queue overflow. High-water values are cumulative
process counters, so a later phase retains an earlier peak unless it establishes
a new one; the packet count is sampled within each phase.

| Phase | Any packet residence ms | Primary residence ms | Repair residence ms | Admitted backlog ms | Scheduled backlog ms | Maximum sampled packets | Key-frame reserve bytes | Complete frames dropped | Expired repair packets | Rate-trimmed repair packets | Encoder requests | Coalesced requests | Receiver PLI/FIR | Malformed RTCP | Request failures | RTP queue overflows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 41.0 | 41.0 | 37.4 | 40.4 | 49.9 | 35 | 67236 | 0 | 0 | 4 | 0 | 0 | 0 | 0 | 0 | 0 |
| baseline | 41.0 | 41.0 | 37.4 | 40.4 | 49.9 | 30 | 64017 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| conditioning | 41.0 | 41.0 | 37.4 | 40.4 | 49.9 | 28 | 64042 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| constrained | 224.3 | 54.2 | 224.3 | 49.5 | 150.0 | 76 | 64042 | 0 | 36 | 380 | 1 | 0 | 0 | 0 | 0 | 0 |
| impaired | 224.3 | 54.2 | 224.3 | 49.5 | 150.0 | 8 | 22138 | 0 | 0 | 27 | 0 | 0 | 0 | 0 | 0 | 0 |
| recovery | 224.3 | 54.2 | 224.3 | 49.5 | 150.0 | 12 | 66256 | 0 | 0 | 9 | 1 | 0 | 0 | 0 | 0 | 0 |

### Repair timeliness

FEC is paced immediately after each protected 5-packet media group so it can
arrive before playout. RTX remains at media-frame boundaries because it repairs
an already reported loss and must not delay completion of the current frame.
The split counters below make a late proactive repair distinguishable from an
expired retransmission.

| Phase | Max FEC residence ms | FEC sent | FEC expired | FEC rate-trimmed | Max RTX residence ms | RTX sent | RTX expired | RTX rate-trimmed | RTX duplicates coalesced |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 37.4 | 2894 | 0 | 4 | 0.0 | 0 | 0 | 0 | 0 |
| baseline | 37.4 | 3677 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| conditioning | 37.4 | 4440 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| constrained | 55.3 | 3518 | 0 | 17 | 224.3 | 594 | 36 | 363 | 347 |
| impaired | 55.3 | 1992 | 0 | 27 | 224.3 | 211 | 0 | 0 | 0 |
| recovery | 55.3 | 4931 | 0 | 9 | 224.3 | 13 | 0 | 0 | 0 |

## Producer host scheduling

Linux aggregate CPU counters are sampled from the producer's host namespace.
They expose time withheld by the hypervisor separately from work performed by
the application. A run with sustained steal time cannot establish a transport
performance result because the source itself was not scheduled predictably.
The 250 ms sampling heartbeat also exposes shorter pauses that aggregate CPU
counters cannot attribute.
The producer runtime reports 4 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 79 | 25.0% | 0.0% | 0.0% | 254 ms | 254 ms |
| baseline | 99 | 25.0% | 0.0% | 0.0% | 255 ms | 255 ms |
| conditioning | 119 | 26.0% | 0.0% | 0.0% | 254 ms | 255 ms |
| constrained | 178 | 24.8% | 0.0% | 0.0% | 254 ms | 255 ms |
| impaired | 138 | 23.0% | 0.0% | 0.0% | 254 ms | 254 ms |
| recovery | 178 | 24.0% | 0.0% | 0.0% | 254 ms | 254 ms |

## Receiver host scheduling

Linux aggregate CPU counters are sampled from the receiver's host namespace.
They distinguish media-path latency from time when the hypervisor prevented the
browser host from running. The 250 ms sampling heartbeat also exposes shorter
runtime pauses. The receiver runtime reports 4 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 79 | 25.0% | 0.0% | 0.0% | 253 ms | 253 ms |
| baseline | 99 | 24.8% | 0.0% | 0.0% | 254 ms | 254 ms |
| conditioning | 119 | 26.0% | 0.0% | 0.0% | 254 ms | 255 ms |
| constrained | 177 | 24.8% | 0.0% | 0.0% | 254 ms | 254 ms |
| impaired | 139 | 23.2% | 0.0% | 0.0% | 254 ms | 254 ms |
| recovery | 177 | 24.0% | 0.0% | 0.0% | 254 ms | 254 ms |

## Encoder cadence and observer effect

The performance result is meaningful only if its source produces frames on
time. Per-frame x264 evidence is written to container-local tmpfs and copied
only after the pipeline stops, not streamed through Docker's logging path; this
prevents detailed diagnostics or host I/O from blocking the encoder being
measured. At 30 fps, a late interval exceeds
50 ms and a catch-up burst is shorter than 16.7 ms. Qualification requires a
p99 gap no higher than 50 ms, no individual gap above 200 ms, and no more
than 1% late or catch-up intervals in any measured phase. The p99 and maximum
columns keep isolated scheduler jitter visible rather than misclassifying it as
network loss or hiding it behind an aggregate frame rate.

| Phase | Measured intervals | p99 frame gap ms | Maximum frame gap ms | Late intervals | Catch-up bursts |
| --- | ---: | ---: | ---: | ---: | ---: |
| warmup | 601 | 37.2 | 40.3 | 0.00% | 0.00% |
| baseline | 757 | 36.7 | 38.5 | 0.00% | 0.00% |
| conditioning | 903 | 36.4 | 40.8 | 0.00% | 0.00% |
| constrained | 1349 | 36.7 | 39.5 | 0.00% | 0.00% |
| impaired | 1049 | 36.5 | 37.6 | 0.00% | 0.00% |
| recovery | 1189 | 36.9 | 40.1 | 0.00% | 0.00% |

## Network-emulation fidelity

The impairment applies to the selected producer-to-TURN transport. Media, TURN permissions, and TURN channel traffic share that physical branch; rstream publication and HTTP signaling remain outside it.

| Interval | Configured random loss | Shaped packets | Total qdisc drops | Total drop ratio | Ending queue |
| --- | ---: | ---: | ---: | ---: | ---: |
| capacity transitions | 0.00% | 13422 | 370 | 2.68% | n/a |
| constrained steady state | 0.00% | 9056 | 0 | 0.00% | 5/256 (2.0%) |
| impaired (incremental) | 2.00% | 12391 | 277 | 2.19% | 58/256 (22.7%) |
| recovery drain | 0.00% | 700 | 0 | 0.00% | 0/256 (0.0%) |

Total qdisc drops include both configured random loss and queue overflow while the congestion controller reacts. Capacity-transition counters are separated from the final steady interval so a bounded reaction transient cannot hide sustained overload, and steady behavior cannot hide a destructive transition.

## Receiver playout latency

The receiver uses a bounded jitter buffer to absorb packet timing variation and
leave time for repair. Both columns come from cumulative WebRTC receiver
counters. The configured minimum hint is 200 ms. Qualification caps the requested target at 250 ms and each phase's average effective buffered delay at 300 ms. The synchronized transport figure retains per-sample values so shorter excursions remain visible.

| Phase | Average buffered delay ms/frame | Average target delay ms/frame |
| --- | ---: | ---: |
| warmup | 189.0 | 187.0 |
| baseline | 188.2 | 187.0 |
| conditioning | 188.9 | 187.0 |
| constrained | 219.0 | 186.6 |
| impaired | 185.3 | 186.0 |
| recovery | 208.0 | 186.8 |

## Receiver-kernel UDP diagnostics

These independent kernel counters distinguish upstream packet loss from a local
browser socket that could not drain its receive buffer. The qualification
browser is sampled inside its isolated Linux network namespace, so the counters
exclude unrelated host and container traffic.

Source: `linux-network-namespace`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 18316 | 470 | 0 | 0 | 0 | 0 |
| baseline | 25 | 23062 | 572 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 27613 | 697 | 0 | 0 | 0 | 0 |
| constrained | 45 | 22683 | 1269 | 0 | 0 | 0 | 0 |
| impaired | 35 | 12393 | 995 | 0 | 0 | 0 | 0 |
| recovery | 45 | 29892 | 1042 | 0 | 0 | 0 | 0 |

## Producer-kernel UDP diagnostics

These counters come from the producer container's isolated Linux network
namespace. Together with the receiver table they show whether a missing RTP
sequence was dropped by a local socket or disappeared between two healthy
kernel boundaries. Linux may account a `netem` rejection in both the qdisc
drop counter and UDP `SndbufErrors`; therefore send-buffer errors are accepted
only in shaped phases and only up to each interval's independently measured
qdisc drops. Any receive overflow, unshaped-phase send rejection, or excess over
those bounds fails the run.

Source: `linux-docker-network-namespace`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 478 | 18279 | 0 | 0 | 0 | 0 |
| baseline | 25 | 602 | 23126 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 724 | 27634 | 0 | 0 | 0 | 0 |
| constrained | 45 | 1335 | 22730 | 0 | 0 | 0 | 370 |
| impaired | 35 | 1036 | 12783 | 0 | 0 | 0 | 0 |
| recovery | 45 | 1088 | 29903 | 0 | 0 | 0 | 0 |

## Acceptance criteria

- PASS — network-condition-timeline: every configured traffic-control transition is timestamped in chronological order on the metrics collector clock
- PASS — phase-sample-coverage: every measured phase has at least 15 samples
- PASS — producer-host-scheduler: producer host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — receiver-host-scheduler: receiver host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — playout-target-latency-budget: receiver jitter-buffer target evidence covers every phase and remains at or below 250 ms
- PASS — playout-effective-latency-budget: receiver effective buffered delay evidence covers every phase and its phase average remains at or below 300 ms
- PASS — ice-path: every sample uses a TURN relay candidate
- PASS — session-continuity: peer connection and playback remain healthy for at least 98% of samples
- PASS — baseline-throughput: baseline median receive throughput is at least 1 Mbps
- PASS — capacity-experiment-settled: at least 80% of samples and the final sample in the pre-transition window stay within 10% of that window's median encoder target
- PASS — congestion-response: constrained median encoder target falls by at least 20% from the stable pre-transition target
- PASS — response-time: encoder target reacts to the constrained link within 30 seconds
- PASS — continued-pressure: the encoder does not increase its target after measured loss exceeds the configured recovery threshold
- PASS — constrained-link-efficiency: median video payload uses at least 55% of constrained link capacity
- PASS — impaired-target-efficiency: median received video remains at least 85% of the encoder target while loss is injected
- PASS — decoder-activity: decoded-frame progress is visible in at least 95% of sample intervals
- PASS — healthy-link-freezes: baseline and recovery spend at most 2% of measured time frozen
- PASS — impaired-link-freezes: each shaped phase spends at most 10% of measured time frozen
- PASS — interactive-latency-budget: maximum RTT stays below 350 ms on unshaped links and 600 ms under the 120 ms one-way impairment
- PASS — decoded-frame-rate: decoded output stays above 25 fps on healthy links and 20 fps while shaped
- PASS — encoder-quality-telemetry: the pinned x264 encoder reports valid per-frame H.264 quantization data in every phase
- PASS — encoder-cadence: the encoded source keeps p99 frame gaps at or below 50 ms, every gap at or below 200 ms, and late or catch-up intervals at or below 1% from baseline through recovery
- PASS — impaired-compression-quality: impaired-link sender average H.264 QP stays at or below 42
- PASS — decoded-resolution: decoded video remains 1920x1080 in every phase
- PASS — recovery-time: encoder target returns to at least 80% of its stable pre-transition target within 35 seconds of the capacity-restoration step
- PASS — sustained-recovery: the encoder stays at or above 80% of its stable pre-transition target for at least 80% of a continuous 10-second window after capacity is restored
- PASS — throughput-recovery: recovery median receive throughput returns to at least 60% of the stable pre-transition receive rate
- PASS — loss-feedback: network impairment produces NACK feedback
- PASS — rtx-sender-pacing: the sender records paced RTX packets while loss is injected
- PASS — repair-amplification: NACK feedback remains below 10% of received packets during 2% injected loss
- PASS — flexfec-negotiation: the browser and producer negotiate the FlexFEC-03 protection stream
- PASS — flexfec-repair: the receiver observes FlexFEC packets while loss is injected
- PASS — flexfec-sender-pacing: the sender records paced FlexFEC packets while loss is injected
- PASS — flexfec-configuration: runtime telemetry matches the FlexFEC protection recorded by the manifest
- PASS — flexfec-burst-headroom: the protected wire rate retains the sender's real-time burst headroom
- PASS — decoded-video: the browser keeps decoding frames throughout the scenario
- PASS — bounded-pacer-capacity: the sender never overflows its packet queue, keeps actual packet residence within 375 ms, and admits no new media beyond 225 ms
- PASS — pacer-recovery-keyframes: every complete-frame admission drop requests a recovery key frame and encounters no encoder rejection
- PASS — rtcp-keyframe-feedback: receiver PLI feedback reaches the producer's encoder instead of being discarded
- PASS — rtcp-feedback-integrity: the producer parses every compound RTCP feedback datagram
- PASS — adaptive-reconfiguration-integrity: the encoder accepts every rate-limited adaptive bitrate reconfiguration
- PASS — twcc-feedback-integrity: TWCC feedback is present and every reported status is parsed without malformed packets
- PASS — loss-guard-response: persistent TWCC loss above 10% immediately reduces the sender target without waiting for a delay-estimator callback
- PASS — selective-media-shaping: the selective traffic-control branch handles the measured media flow
- PASS — capacity-profile-configuration: the capacity phase has no random-loss injector configured
- PASS — loss-profile-configuration: the impaired phase configures exactly 2% random packet loss
- PASS — traffic-control-drop-budget: capacity-step transients stay below 15%, and steady capacity plus random-loss phases stay below 5% drops
- PASS — traffic-control-queue-headroom: the shaped queue ends each steady interval below 75% occupancy, preventing a larger limit from hiding sustained bufferbloat
- PASS — traffic-control-recovery-drain: the healthy recovery profile carries media, adds no drops, and leaves at most one short RTP burst queued before traffic-control teardown
- PASS — twcc-loss-fidelity: browser TWCC loss stays within eight percentage points of shaped-link drops, detecting transport-sequence accounting regressions
- PASS — receiver-udp-observability: receiver-kernel UDP counters cover every measured phase
- PASS — receiver-kernel-capacity: the receiver kernel drops no UDP datagram because its socket buffer is full
- PASS — producer-udp-observability: producer-kernel UDP counters cover every measured phase
- PASS — producer-kernel-capacity: the producer kernel has no UDP receive overflow or send rejection outside the independently measured qdisc envelope, allowing at most two datagrams at an asynchronous phase boundary

The checked-in summary is evidence for one pinned run, not a universal performance guarantee. Re-run `./run.sh` on the target architecture before using the result as an acceptance decision.
