# Adaptive streaming qualification — FAIL

Generated at 2026-08-17T02:05:08Z from repository revision `ca8a308907aaa5661954af81d10f04bd8e5f52bb`.

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
| producer-build-started | 0.1 s |
| producer-build-completed | 137.4 s |
| browser-build-started | 0.1 s |
| browser-build-completed | 24.0 s |
| connection-started | 0.0 s |
| producer-container-started | 0.2 s |
| producer-ready | 1.1 s |
| browser-container-started | 0.0 s |
| media-connected | 12.1 s |

Measured service establishment: 13.4 s.

## Phase summary

| Phase | Samples | Connected | Received kbps (median) | Link use | TWCC kbps (median) | Encoder kbps (median) | Decoded fps | Avg QP | Decode ms/frame | Frozen | NACK | RTX packets | FEC packets | Max RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 100.0% | 6823 | n/a | 8000 | 8000 | 30.0 | 25.2 | 2.78 | 0.0% | 0 | 0 | 0 | 139 |
| baseline | 25 | 100.0% | 4794 | n/a | 5800 | 5600 | 30.0 | 25.0 | 2.54 | 0.0% | 0 | 0 | 0 | 139 |
| conditioning | 30 | 100.0% | 6691 | 20.9% | 8000 | 8000 | 30.0 | 24.0 | 2.74 | 0.0% | 0 | 0 | 0 | 140 |
| constrained | 45 | 100.0% | 2834 | 70.9% | 3400 | 3100 | 30.0 | 29.1 | 2.46 | 0.0% | 0 | 0 | 0 | 444 |
| impaired | 35 | 100.0% | 1912 | 47.8% | 2000 | 2000 | 28.0 | 31.4 | 2.00 | 17.0% | 511 | 193 | 0 | 342 |
| recovery | 44 | 100.0% | 4749 | 14.8% | 6200 | 5400 | 30.1 | 26.0 | 2.46 | 0.0% | 0 | 0 | 0 | 306 |

Congestion response: 1.0 s. Recovery response: 20.1 s.

Selected ICE candidate-pair switches: 0. Both peers remain on the required TURN relay path.

Superseded out-of-order estimator callbacks: 0. The producer always applies the estimator's current target rather than the potentially stale callback payload.

## Qualification decision

| Gate | Observed | Required |
| --- | ---: | ---: |
| Shaper activation | 142.9% of baseline before the first capacity step | at least 80% |
| Rate response | 61.3% reduction in 1.0 s | at least 20% within 30 s |
| Rate recovery | 20.1 s to threshold; 100.0% target residency in the best 10 s window; 14.1 s longest uninterrupted interval; received throughput 66.3% of the stable pre-transition rate | target reaches 80% within 35 s, sustains it for 10 s, and median throughput reaches at least 60% of baseline |
| Playback under impairment | 28.0 fps; 17.0% frozen | at least 20 fps; at most 10% frozen |
| Latency under impairment | 342 ms max RTT; 243.0 ms effective playout buffer | at most 600 ms RTT; at most 300 ms phase-average buffer |
| Visual quality under impairment | QP 31.4; 1920x1080 | QP at most 42; 1920x1080 |
| Loss fidelity | qdisc 1.91%; TWCC 5.44% | 2% injected; TWCC within 8 percentage points |
| Packet repair | NACK 511; sender RTX 595; receiver RTX 193 | NACK, sender RTX, and receiver RTX greater than zero |
| Sender queue | 55.0 ms residence; 63.4 ms admitted backlog; 0 overflow drops | at most 375 ms; at most 225 ms; zero overflow |

## Congestion-controller diagnostics

| Phase | Loss target kbps | Delay target kbps | Guard target kbps | Peak report loss | Guard reduce / recover | Controller loss | Browser TWCC loss | Encoder updates | Update failures | Feedback packets | Padding statuses | Malformed feedback |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 4 | 0 | 304 | 0 | 0 |
| baseline | 5846 | 5847 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 4 | 0 | 384 | 0 | 0 |
| conditioning | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 4 | 0 | 464 | 0 | 0 |
| constrained | 3362 | 3367 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 16 | 0 | 702 | 0 | 0 |
| impaired | 1857 | 2412 | 2000 | 15.38% | 1 / 0 | 4.89% | 5.44% | 1 | 0 | 553 | 408 | 0 |
| recovery | 6213 | 6302 | 0 | 5.00% | 0 / 1 | 0.00% | 0.00% | 10 | 0 | 686 | 0 | 0 |

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
| warmup | 51.3 | 51.3 | 0.0 | 59.7 | 60.9 | 19 | 65418 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| baseline | 51.3 | 51.3 | 0.0 | 59.7 | 60.9 | 18 | 58869 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| conditioning | 51.3 | 51.3 | 0.0 | 59.7 | 60.9 | 26 | 68007 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| constrained | 55.0 | 55.0 | 0.0 | 63.4 | 64.7 | 12 | 65608 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| impaired | 55.0 | 55.0 | 52.8 | 63.4 | 64.7 | 5 | 25579 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| recovery | 55.0 | 55.0 | 52.8 | 63.4 | 64.7 | 14 | 66986 | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 | 0 |

### Repair timeliness

FEC is paced immediately after each protected 0-packet media group so it can
arrive before playout. RTX remains at media-frame boundaries because it repairs
an already reported loss and must not delay completion of the current frame.
The split counters below make a late proactive repair distinguishable from an
expired retransmission.

| Phase | Max FEC residence ms | FEC sent | FEC expired | FEC rate-trimmed | Max RTX residence ms | RTX sent | RTX expired | RTX rate-trimmed | RTX duplicates coalesced |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 0.0 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| baseline | 0.0 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| conditioning | 0.0 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| constrained | 0.0 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 |
| impaired | 0.0 | 0 | 0 | 0 | 52.8 | 595 | 0 | 0 | 0 |
| recovery | 0.0 | 0 | 0 | 0 | 52.8 | 0 | 0 | 0 | 0 |

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
| warmup | 78 | 22.7% | 0.0% | 0.0% | 255 ms | 255 ms |
| baseline | 100 | 20.6% | 0.0% | 0.0% | 255 ms | 255 ms |
| conditioning | 118 | 22.1% | 0.0% | 0.0% | 254 ms | 255 ms |
| constrained | 178 | 21.6% | 0.0% | 0.0% | 255 ms | 255 ms |
| impaired | 138 | 19.5% | 0.0% | 0.0% | 254 ms | 255 ms |
| recovery | 177 | 21.4% | 0.0% | 0.0% | 254 ms | 254 ms |

## Receiver host scheduling

Linux aggregate CPU counters are sampled from the receiver's host namespace.
They distinguish media-path latency from time when the hypervisor prevented the
browser host from running. The 250 ms sampling heartbeat also exposes shorter
runtime pauses. The receiver runtime reports 4 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 79 | 22.7% | 0.0% | 0.0% | 254 ms | 254 ms |
| baseline | 100 | 20.6% | 0.0% | 0.0% | 255 ms | 255 ms |
| conditioning | 118 | 22.1% | 0.0% | 0.0% | 254 ms | 254 ms |
| constrained | 178 | 21.6% | 0.0% | 0.0% | 254 ms | 254 ms |
| impaired | 138 | 19.4% | 0.0% | 0.0% | 253 ms | 254 ms |
| recovery | 177 | 21.4% | 0.0% | 0.0% | 254 ms | 254 ms |

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
| warmup | 601 | 36.5 | 38.0 | 0.00% | 0.00% |
| baseline | 761 | 35.9 | 36.7 | 0.00% | 0.00% |
| conditioning | 899 | 36.0 | 37.9 | 0.00% | 0.00% |
| constrained | 1352 | 36.8 | 38.4 | 0.00% | 0.00% |
| impaired | 1049 | 36.0 | 37.1 | 0.00% | 0.00% |
| recovery | 1192 | 36.5 | 38.6 | 0.00% | 0.00% |

## Network-emulation fidelity

The impairment applies to the selected producer-to-TURN transport. Media, TURN permissions, and TURN channel traffic share that physical branch; rstream publication and HTTP signaling remain outside it.

| Interval | Configured random loss | Shaped packets | Total qdisc drops | Total drop ratio | Ending queue |
| --- | ---: | ---: | ---: | ---: | ---: |
| capacity transitions | 0.00% | 10142 | 0 | 0.00% | n/a |
| constrained steady state | 0.00% | 9428 | 0 | 0.00% | 5/256 (2.0%) |
| impaired (incremental) | 2.00% | 10788 | 210 | 1.91% | 58/256 (22.7%) |
| recovery drain | 0.00% | 545 | 0 | 0.00% | 0/256 (0.0%) |

Total qdisc drops include both configured random loss and queue overflow while the congestion controller reacts. Capacity-transition counters are separated from the final steady interval so a bounded reaction transient cannot hide sustained overload, and steady behavior cannot hide a destructive transition.

## Receiver playout latency

The receiver uses a bounded jitter buffer to absorb packet timing variation and
leave time for repair. Both columns come from cumulative WebRTC receiver
counters. The configured minimum hint is 200 ms. Qualification caps the requested target at 250 ms and each phase's average effective buffered delay at 300 ms. The synchronized transport figure retains per-sample values so shorter excursions remain visible.

| Phase | Average buffered delay ms/frame | Average target delay ms/frame |
| --- | ---: | ---: |
| warmup | 188.2 | 186.0 |
| baseline | 187.9 | 186.0 |
| conditioning | 187.4 | 186.0 |
| constrained | 183.0 | 186.3 |
| impaired | 243.0 | 186.9 |
| recovery | 205.3 | 186.3 |

## Receiver-kernel UDP diagnostics

These independent kernel counters distinguish upstream packet loss from a local
browser socket that could not drain its receive buffer. The qualification
browser is sampled inside its isolated Linux network namespace, so the counters
exclude unrelated host and container traffic.

Source: `linux-network-namespace`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 14884 | 379 | 0 | 0 | 0 | 0 |
| baseline | 25 | 14367 | 449 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 24007 | 551 | 0 | 0 | 0 | 0 |
| constrained | 45 | 19838 | 805 | 0 | 0 | 0 | 0 |
| impaired | 35 | 10824 | 1108 | 0 | 0 | 0 | 0 |
| recovery | 45 | 24867 | 832 | 0 | 0 | 0 | 0 |

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
| warmup | 20 | 382 | 14856 | 0 | 0 | 0 | 0 |
| baseline | 25 | 479 | 14424 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 573 | 24047 | 0 | 0 | 0 | 0 |
| constrained | 45 | 872 | 19841 | 0 | 0 | 0 | 0 |
| impaired | 35 | 1149 | 11117 | 0 | 0 | 0 | 0 |
| recovery | 45 | 875 | 24914 | 0 | 0 | 0 | 0 |

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
- FAIL — impaired-link-freezes: each shaped phase spends at most 10% of measured time frozen
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
- PASS — rtx-repair: the receiver observes RTX repair packets while loss is injected without proactive FEC
- PASS — repair-amplification: NACK feedback remains below 10% of received packets during 2% injected loss
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
