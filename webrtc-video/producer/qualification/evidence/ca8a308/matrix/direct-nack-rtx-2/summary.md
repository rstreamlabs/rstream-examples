# Adaptive streaming qualification — FAIL

Generated at 2026-08-17T01:18:29Z from repository revision `ca8a308907aaa5661954af81d10f04bd8e5f52bb`.

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
| producer-build-completed | 131.1 s |
| browser-build-started | 0.1 s |
| browser-build-completed | 25.6 s |
| connection-started | 0.0 s |
| producer-container-started | 0.7 s |
| producer-ready | 0.1 s |
| browser-container-started | 0.0 s |
| media-connected | 7.1 s |

Measured service establishment: 7.9 s.

## Phase summary

| Phase | Samples | Connected | Received kbps (median) | Link use | TWCC kbps (median) | Encoder kbps (median) | Decoded fps | Avg QP | Decode ms/frame | Frozen | NACK | RTX packets | FEC packets | Max RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 100.0% | 7146 | n/a | 8000 | 8000 | 30.0 | 23.6 | 3.11 | 0.0% | 0 | 0 | 0 | 1 |
| baseline | 25 | 100.0% | 7030 | n/a | 8000 | 8000 | 30.0 | 23.1 | 3.12 | 0.0% | 0 | 0 | 0 | 1 |
| conditioning | 30 | 100.0% | 7102 | 22.2% | 8000 | 8000 | 30.0 | 23.3 | 3.11 | 0.0% | 0 | 0 | 0 | 4 |
| constrained | 45 | 100.0% | 2075 | 51.9% | 2200 | 2000 | 27.9 | 28.7 | 2.69 | 6.7% | 383 | 320 | 0 | 552 |
| impaired | 35 | 100.0% | 1888 | 47.2% | 2600 | 2000 | 29.2 | 31.3 | 2.41 | 2.5% | 394 | 208 | 0 | 232 |
| recovery | 45 | 100.0% | 6851 | 21.4% | 8000 | 8000 | 30.0 | 25.3 | 2.74 | 0.0% | 0 | 0 | 0 | 163 |

Congestion response: 6.0 s. Recovery response: 12.1 s.

Selected ICE candidate-pair switches: 0. The direct-path address selector remains active across port changes.

Superseded out-of-order estimator callbacks: 0. The producer always applies the estimator's current target rather than the potentially stale callback payload.

## Qualification decision

| Gate | Observed | Required |
| --- | ---: | ---: |
| Healthy-link quality | 100.0% of baseline at or above 7200 kbps | at least 50% of baseline |
| Shaper activation | 100.0% of baseline before the first capacity step | at least 80% |
| Rate response | 75.0% reduction in 6.0 s | at least 20% within 30 s |
| Rate recovery | 12.1 s to threshold; 100.0% target residency in the best 10 s window; 27.2 s longest uninterrupted interval; received throughput 96.5% of the stable pre-transition rate | target reaches 80% within 35 s, sustains it for 10 s, and median throughput reaches at least 60% of baseline |
| Playback under impairment | 29.2 fps; 2.5% frozen | at least 20 fps; at most 10% frozen |
| Latency under impairment | 232 ms max RTT; 186.8 ms effective playout buffer | at most 600 ms RTT; at most 300 ms phase-average buffer |
| Visual quality under impairment | QP 31.3; 1920x1080 | QP at most 42; 1920x1080 |
| Loss fidelity | qdisc 2.08%; TWCC 3.97% | 2% injected; TWCC within 8 percentage points |
| Packet repair | NACK 394; sender RTX 425; receiver RTX 208 | NACK, sender RTX, and receiver RTX greater than zero |
| Sender queue | 225.0 ms residence; 59.4 ms admitted backlog; 0 overflow drops | at most 375 ms; at most 225 ms; zero overflow |

## Congestion-controller diagnostics

| Phase | Loss target kbps | Delay target kbps | Guard target kbps | Peak report loss | Guard reduce / recover | Controller loss | Browser TWCC loss | Encoder updates | Update failures | Feedback packets | Padding statuses | Malformed feedback |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 3 | 0 | 304 | 0 | 0 |
| baseline | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 0 | 0 | 384 | 0 | 0 |
| conditioning | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 0 | 0 | 464 | 0 | 0 |
| constrained | 2259 | 3808 | 2000 | 50.00% | 5 / 1 | 0.00% | 3.10% | 14 | 0 | 693 | 68 | 0 |
| impaired | 2633 | 2861 | 0 | 16.67% | 0 / 0 | 4.19% | 3.97% | 1 | 0 | 547 | 347 | 0 |
| recovery | 8000 | 8000 | 0 | 0.00% | 0 / 0 | 0.00% | 0.00% | 5 | 0 | 704 | 0 | 0 |

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
| warmup | 36.3 | 36.3 | 0.0 | 45.4 | 46.3 | 21 | 68126 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| baseline | 37.1 | 37.1 | 0.0 | 45.5 | 46.3 | 27 | 68225 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| conditioning | 37.3 | 37.3 | 0.0 | 45.5 | 46.4 | 26 | 68262 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| constrained | 225.0 | 51.3 | 225.0 | 59.4 | 1064.8 | 319 | 65584 | 0 | 1745 | 767 | 1 | 0 | 0 | 0 | 0 | 0 |
| impaired | 225.0 | 51.3 | 225.0 | 59.4 | 1064.8 | 6 | 25630 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| recovery | 225.0 | 51.3 | 225.0 | 59.4 | 1064.8 | 13 | 67935 | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 | 0 |

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
| constrained | 0.0 | 0 | 0 | 0 | 225.0 | 575 | 1745 | 767 | 3760 |
| impaired | 0.0 | 0 | 0 | 0 | 225.0 | 425 | 0 | 0 | 0 |
| recovery | 0.0 | 0 | 0 | 0 | 225.0 | 0 | 0 | 0 | 0 |

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
| warmup | 79 | 24.2% | 0.0% | 0.0% | 254 ms | 254 ms |
| baseline | 100 | 24.2% | 0.0% | 0.0% | 255 ms | 255 ms |
| conditioning | 118 | 26.0% | 0.0% | 0.0% | 255 ms | 255 ms |
| constrained | 178 | 24.1% | 0.0% | 0.0% | 255 ms | 255 ms |
| impaired | 137 | 22.0% | 0.0% | 0.0% | 255 ms | 256 ms |
| recovery | 178 | 25.5% | 0.0% | 0.0% | 255 ms | 256 ms |

## Receiver host scheduling

Linux aggregate CPU counters are sampled from the receiver's host namespace.
They distinguish media-path latency from time when the hypervisor prevented the
browser host from running. The 250 ms sampling heartbeat also exposes shorter
runtime pauses. The receiver runtime reports 4 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 79 | 24.8% | 0.0% | 0.0% | 255 ms | 255 ms |
| baseline | 99 | 24.8% | 0.0% | 0.0% | 254 ms | 254 ms |
| conditioning | 118 | 26.0% | 0.0% | 0.0% | 254 ms | 255 ms |
| constrained | 178 | 23.5% | 0.0% | 0.0% | 254 ms | 254 ms |
| impaired | 138 | 22.0% | 0.0% | 0.0% | 254 ms | 254 ms |
| recovery | 177 | 25.5% | 0.0% | 0.0% | 254 ms | 254 ms |

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
| warmup | 602 | 36.4 | 40.5 | 0.00% | 0.00% |
| baseline | 762 | 36.9 | 42.1 | 0.00% | 0.00% |
| conditioning | 899 | 36.7 | 43.4 | 0.00% | 0.00% |
| constrained | 1353 | 36.0 | 41.4 | 0.00% | 0.00% |
| impaired | 1048 | 36.0 | 43.7 | 0.00% | 0.00% |
| recovery | 1351 | 36.1 | 47.6 | 0.00% | 0.00% |

## Network-emulation fidelity

The direct impairment applies to every outbound UDP packet on the isolated producer-to-browser address. It follows legitimate ICE candidate-port switches while excluding host and unrelated container traffic.

| Interval | Configured random loss | Shaped packets | Total qdisc drops | Total drop ratio | Ending queue |
| --- | ---: | ---: | ---: | ---: | ---: |
| capacity transitions | 0.00% | 12767 | 639 | 4.77% | n/a |
| constrained steady state | 0.00% | 7635 | 0 | 0.00% | 0/256 (0.0%) |
| impaired (incremental) | 2.00% | 10498 | 223 | 2.08% | 58/256 (22.7%) |
| recovery drain | 0.00% | 825 | 0 | 0.00% | 0/256 (0.0%) |

Total qdisc drops include both configured random loss and queue overflow while the congestion controller reacts. Capacity-transition counters are separated from the final steady interval so a bounded reaction transient cannot hide sustained overload, and steady behavior cannot hide a destructive transition.

## Receiver playout latency

The receiver uses a bounded jitter buffer to absorb packet timing variation and
leave time for repair. Both columns come from cumulative WebRTC receiver
counters. The configured minimum hint is 200 ms. Qualification caps the requested target at 250 ms and each phase's average effective buffered delay at 300 ms. The synchronized transport figure retains per-sample values so shorter excursions remain visible.

| Phase | Average buffered delay ms/frame | Average target delay ms/frame |
| --- | ---: | ---: |
| warmup | 187.3 | 185.0 |
| baseline | 188.9 | 185.5 |
| conditioning | 189.2 | 185.6 |
| constrained | 188.3 | 186.0 |
| impaired | 186.8 | 186.1 |
| recovery | 203.0 | 186.3 |

## Receiver-kernel UDP diagnostics

These independent kernel counters distinguish upstream packet loss from a local
browser socket that could not drain its receive buffer. The qualification
browser is sampled inside its isolated Linux network namespace, so the counters
exclude unrelated host and container traffic.

Source: `linux-network-namespace`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 15940 | 359 | 0 | 0 | 0 | 0 |
| baseline | 25 | 21109 | 453 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 25420 | 537 | 0 | 0 | 0 | 0 |
| constrained | 45 | 20653 | 1204 | 0 | 1 | 0 | 0 |
| impaired | 35 | 10532 | 986 | 0 | 0 | 0 | 0 |
| recovery | 45 | 29393 | 819 | 0 | 0 | 0 | 0 |

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
| warmup | 20 | 356 | 15937 | 0 | 0 | 0 | 0 |
| baseline | 25 | 448 | 21101 | 0 | 0 | 0 | 0 |
| conditioning | 30 | 536 | 25423 | 0 | 0 | 0 | 0 |
| constrained | 45 | 1186 | 20596 | 0 | 0 | 0 | 639 |
| impaired | 35 | 986 | 10808 | 0 | 0 | 0 | 0 |
| recovery | 45 | 807 | 29350 | 0 | 0 | 0 | 0 |

## Acceptance criteria

- PASS — network-condition-timeline: every configured traffic-control transition is timestamped in chronological order on the metrics collector clock
- PASS — phase-sample-coverage: every measured phase has at least 15 samples
- PASS — producer-host-scheduler: producer host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — receiver-host-scheduler: receiver host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — playout-target-latency-budget: receiver jitter-buffer target evidence covers every phase and remains at or below 250 ms
- PASS — playout-effective-latency-budget: receiver effective buffered delay evidence covers every phase and its phase average remains at or below 300 ms
- PASS — ice-path: every sample uses the direct Docker bridge path without TURN
- PASS — session-continuity: peer connection and playback remain healthy for at least 98% of samples
- PASS — baseline-throughput: baseline median receive throughput is at least 1 Mbps
- PASS — capacity-experiment-settled: at least 80% of samples and the final sample in the pre-transition window stay within 10% of that window's median encoder target
- PASS — healthy-link-quality-ceiling: the encoder spends at least half of the healthy baseline within 10% of its 8000 kbps adaptive ceiling
- PASS — congestion-response: constrained median encoder target falls by at least 20% from the stable pre-transition target
- PASS — response-time: encoder target reacts to the constrained link within 30 seconds
- PASS — continued-pressure: the encoder does not increase its target after measured loss exceeds the configured recovery threshold
- FAIL — constrained-link-efficiency: median video payload uses at least 55% of constrained link capacity
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
