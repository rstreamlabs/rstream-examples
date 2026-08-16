# Adaptive streaming qualification — PASS

Generated at 2026-08-16T13:30:43Z from repository revision `6706cfdf830ded88a718bc35c6b4a94a0a69e089`.

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
| producer-build-completed | 1.5 s |
| browser-build-started | 0.1 s |
| browser-build-completed | 0.7 s |
| connection-started | 0.0 s |
| producer-container-started | 0.2 s |
| producer-ready | 0.1 s |
| browser-container-started | 0.0 s |
| media-connected | 6.1 s |

Measured service establishment: 6.5 s.

## Phase summary

| Phase | Samples | Connected | Received kbps (median) | Link use | TWCC kbps (median) | Encoder kbps (median) | Decoded fps | Avg QP | Decode ms/frame | Frozen | NACK | RTX packets | FEC packets | Max RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 100.0% | 10388 | n/a | 8000 | 7500 | 30.0 | 24.1 | 1.95 | 0.0% | 0 | 0 | 8644 | 0 |
| baseline | 25 | 100.0% | 10561 | n/a | 8000 | 7500 | 30.0 | 23.8 | 1.94 | 0.0% | 0 | 0 | 11470 | 0 |
| constrained | 45 | 100.0% | 3073 | 76.8% | 2200 | 2000 | 29.9 | 29.2 | 1.48 | 0.0% | 2 | 3 | 10258 | 135 |
| impaired | 36 | 100.0% | 2974 | 74.4% | 2150 | 2000 | 30.0 | 29.7 | 1.42 | 0.0% | 227 | 218 | 7474 | 206 |
| recovery | 45 | 100.0% | 7423 | n/a | 8000 | 5500 | 30.1 | 26.4 | 1.81 | 0.0% | 0 | 0 | 15176 | 172 |

Congestion response: 1.0 s. Recovery response: 8.1 s.

Selected ICE candidate-pair switches: 0. The direct-path address selector remains active across port changes.

Superseded out-of-order estimator callbacks: 2. The producer always applies the estimator's current target rather than the potentially stale callback payload.

## Congestion-controller diagnostics

| Phase | Loss target kbps | Delay target kbps | Controller loss | Browser TWCC loss | Encoder updates | Update failures | Feedback packets | Padding statuses | Malformed feedback |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 8000 | 8000 | 0.00% | 0.00% | 4 | 0 | 305 | 0 | 0 |
| baseline | 8000 | 8000 | 0.00% | 0.00% | 0 | 0 | 384 | 0 | 0 |
| constrained | 2177 | 2200 | 0.00% | 0.00% | 20 | 0 | 703 | 0 | 0 |
| impaired | 2150 | 2189 | 2.33% | 2.14% | 2 | 0 | 557 | 281 | 0 |
| recovery | 8000 | 8000 | 0.00% | 0.00% | 12 | 0 | 727 | 0 | 0 |

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
| warmup | 59.1 | 57.5 | 59.1 | 47.3 | 64.9 | 39 | 62636 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| baseline | 63.2 | 62.6 | 63.2 | 50.5 | 67.6 | 30 | 63970 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| constrained | 129.2 | 129.2 | 99.9 | 105.1 | 120.8 | 56 | 62179 | 0 | 0 | 63 | 0 | 0 | 0 | 0 | 0 | 0 |
| impaired | 129.2 | 129.2 | 101.4 | 105.1 | 120.8 | 32 | 22788 | 0 | 0 | 37 | 0 | 0 | 0 | 0 | 0 | 0 |
| recovery | 129.2 | 129.2 | 101.4 | 105.1 | 120.8 | 46 | 58331 | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 | 0 |

### Repair timeliness

FEC is paced immediately after each protected 4-packet media group so it can
arrive before playout. RTX remains at media-frame boundaries because it repairs
an already reported loss and must not delay completion of the current frame.
The split counters below make a late proactive repair distinguishable from an
expired retransmission.

| Phase | Max FEC residence ms | FEC sent | FEC expired | FEC rate-trimmed | Max RTX residence ms | RTX sent | RTX expired | RTX rate-trimmed |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 59.1 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 |
| baseline | 63.2 | 0 | 0 | 0 | 0.0 | 0 | 0 | 0 |
| constrained | 99.9 | 0 | 0 | 63 | 24.3 | 3 | 0 | 0 |
| impaired | 101.4 | 0 | 0 | 37 | 87.2 | 229 | 0 | 0 |
| recovery | 101.4 | 0 | 0 | 0 | 87.2 | 0 | 0 | 0 |

## Producer host scheduling

Linux aggregate CPU counters are sampled from the producer's host namespace.
They expose time withheld by the hypervisor separately from work performed by
the application. A run with sustained steal time cannot establish a transport
performance result because the source itself was not scheduled predictably.
The 250 ms sampling heartbeat also exposes shorter pauses that aggregate CPU
counters cannot attribute.
The producer runtime reports 10 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 79 | 9.2% | 0.0% | 0.0% | 258 ms | 258 ms |
| baseline | 101 | 9.1% | 0.0% | 0.0% | 255 ms | 258 ms |
| constrained | 178 | 8.5% | 0.0% | 0.0% | 255 ms | 256 ms |
| impaired | 142 | 8.5% | 0.0% | 0.0% | 255 ms | 258 ms |
| recovery | 180 | 8.9% | 0.0% | 0.0% | 257 ms | 257 ms |

## Receiver host scheduling

Linux aggregate CPU counters are sampled from the receiver's host namespace.
They distinguish media-path latency from time when the hypervisor prevented the
browser host from running. The 250 ms sampling heartbeat also exposes shorter
runtime pauses. The receiver runtime reports 10 logical CPUs.

Source: `linux-proc-stat`.

| Phase | Samples | Median active CPU | p95 steal | Maximum steal | p99 sampler gap | Maximum sampler gap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 80 | 9.2% | 0.0% | 0.0% | 258 ms | 258 ms |
| baseline | 100 | 9.0% | 0.0% | 0.0% | 255 ms | 255 ms |
| constrained | 178 | 8.5% | 0.0% | 0.0% | 257 ms | 260 ms |
| impaired | 141 | 8.4% | 0.0% | 0.0% | 257 ms | 258 ms |
| recovery | 179 | 8.9% | 0.0% | 0.0% | 257 ms | 259 ms |

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
| warmup | 609 | 40.0 | 41.2 | 0.00% | 0.00% |
| baseline | 765 | 41.4 | 43.0 | 0.00% | 0.00% |
| constrained | 1352 | 39.1 | 42.7 | 0.00% | 0.00% |
| impaired | 1077 | 39.0 | 40.9 | 0.00% | 0.00% |
| recovery | 1351 | 40.6 | 44.4 | 0.00% | 0.00% |

## Network-emulation fidelity

The direct impairment applies to every outbound UDP packet on the isolated producer-to-browser address. It follows legitimate ICE candidate-port switches while excluding host and unrelated container traffic.

| Interval | Configured random loss | Shaped packets | Total qdisc drops | Total drop ratio | Ending queue |
| --- | ---: | ---: | ---: | ---: | ---: |
| capacity transitions | 0.00% | 11667 | 0 | 0.00% | n/a |
| constrained steady state | 0.00% | 19880 | 0 | 0.00% | 93/256 (36.3%) |
| impaired (incremental) | 2.00% | 22497 | 477 | 2.08% | 118/256 (46.1%) |
| recovery drain | 0.00% | 760 | 0 | 0.00% | 0/256 (0.0%) |

Total qdisc drops include both configured random loss and queue overflow while the congestion controller reacts. Capacity-transition counters are separated from the final steady interval so a bounded reaction transient cannot hide sustained overload, and steady behavior cannot hide a destructive transition.

## Receiver playout latency

The receiver uses a bounded jitter buffer to absorb packet timing variation and
leave time for repair. Both columns come from cumulative WebRTC receiver
counters. The configured minimum hint is 200 ms. Qualification caps the requested target at 250 ms and the effective buffered delay at 300 ms.

| Phase | Average buffered delay ms/frame | Average target delay ms/frame |
| --- | ---: | ---: |
| warmup | 197.6 | 188.0 |
| baseline | 203.6 | 188.0 |
| constrained | 189.8 | 188.0 |
| impaired | 157.9 | 188.0 |
| recovery | 206.5 | 188.0 |

## Receiver-kernel UDP diagnostics

These independent kernel counters distinguish upstream packet loss from a local
browser socket that could not drain its receive buffer. The qualification
browser is sampled inside its isolated Linux network namespace, so the counters
exclude unrelated host and container traffic.

Source: `linux-network-namespace`.

| Phase | Samples | UDP received | UDP sent | Input errors | No-socket drops | Receive-buffer drops | Send-buffer drops |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warmup | 20 | 27037 | 461 | 0 | 0 | 0 | 0 |
| baseline | 25 | 35726 | 571 | 0 | 0 | 0 | 0 |
| constrained | 44 | 30873 | 1018 | 0 | 0 | 0 | 0 |
| impaired | 36 | 23141 | 1052 | 0 | 0 | 0 | 0 |
| recovery | 46 | 47196 | 1066 | 0 | 0 | 0 | 0 |

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
| warmup | 20 | 458 | 27049 | 0 | 0 | 0 | 0 |
| baseline | 25 | 571 | 35761 | 0 | 0 | 0 | 0 |
| constrained | 45 | 1030 | 32215 | 0 | 0 | 0 | 0 |
| impaired | 36 | 1050 | 23676 | 0 | 0 | 0 | 0 |
| recovery | 46 | 1055 | 47083 | 0 | 0 | 0 | 0 |

## Acceptance criteria

- PASS — phase-sample-coverage: every measured phase has at least 15 samples
- PASS — producer-host-scheduler: producer host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — receiver-host-scheduler: receiver host CPU evidence covers every phase, its p95 hypervisor steal time stays at or below 5%, and its 250 ms sampler never stalls for more than 350 ms
- PASS — playout-target-latency-budget: receiver jitter-buffer target evidence covers every phase and remains at or below 250 ms
- PASS — playout-effective-latency-budget: receiver effective buffered delay evidence covers every phase and remains at or below 300 ms
- PASS — ice-path: every sample uses the direct Docker bridge path without TURN
- PASS — session-continuity: peer connection and playback remain healthy for at least 98% of samples
- PASS — baseline-throughput: baseline median receive throughput is at least 1 Mbps
- PASS — healthy-link-quality-ceiling: baseline median encoder target reaches the 8000 kbps adaptive ceiling within its 10% control hysteresis
- PASS — congestion-response: constrained median encoder target falls by at least 20% from baseline
- PASS — response-time: encoder target reacts to the constrained link within 30 seconds
- PASS — continued-pressure: the encoder does not increase its target after additional loss starts
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
- PASS — recovery-time: encoder target rises by at least 20% within 35 seconds of link recovery
- PASS — throughput-recovery: median receive throughput rises by at least 15% after recovery
- PASS — loss-feedback: network impairment produces NACK feedback
- PASS — rtx-repair: the receiver observes RTX repair packets while loss is injected
- PASS — repair-amplification: NACK feedback remains below 10% of received packets during 2% injected loss
- PASS — flexfec-negotiation: the browser and producer negotiate the FlexFEC-03 protection stream
- PASS — flexfec-repair: the receiver observes FlexFEC packets while loss is injected
- PASS — flexfec-configuration: runtime telemetry matches the FlexFEC protection recorded by the manifest
- PASS — flexfec-shared-pacing-envelope: the sender shares its real-time pacing headroom with proactive repair instead of multiplying both budgets
- PASS — decoded-video: the browser keeps decoding frames throughout the scenario
- PASS — bounded-pacer-capacity: the sender never overflows its packet queue, keeps actual packet residence within 375 ms, and admits no new media beyond 225 ms
- PASS — pacer-recovery-keyframes: every complete-frame admission drop requests a recovery key frame and encounters no encoder rejection
- PASS — rtcp-keyframe-feedback: receiver PLI feedback reaches the producer's encoder instead of being discarded
- PASS — rtcp-feedback-integrity: the producer parses every compound RTCP feedback datagram
- PASS — adaptive-reconfiguration-integrity: the encoder accepts every rate-limited adaptive bitrate reconfiguration
- PASS — twcc-feedback-integrity: TWCC feedback is present and every reported status is parsed without malformed packets
- PASS — selective-media-shaping: the selective traffic-control branch handles the measured media flow
- PASS — capacity-profile-configuration: the capacity phase has no random-loss injector configured
- PASS — loss-profile-configuration: the impaired phase configures exactly 2% random packet loss
- PASS — traffic-control-drop-budget: capacity-step transients stay below 15%, and steady capacity plus random-loss phases stay below 5% drops
- PASS — traffic-control-queue-headroom: the shaped queue ends each steady interval below 75% occupancy, preventing a larger limit from hiding sustained bufferbloat
- PASS — traffic-control-recovery-drain: the healthy recovery profile carries media, adds no drops, and drains every packet before traffic-control teardown
- PASS — twcc-loss-fidelity: browser TWCC loss stays within eight percentage points of shaped-link drops, detecting transport-sequence accounting regressions
- PASS — receiver-udp-observability: receiver-kernel UDP counters cover every measured phase
- PASS — receiver-kernel-capacity: the receiver kernel drops no UDP datagram because its socket buffer is full
- PASS — producer-udp-observability: producer-kernel UDP counters cover every measured phase
- PASS — producer-kernel-capacity: the producer kernel has no UDP receive overflow or send rejection that is not bounded by its independently measured qdisc drops

The checked-in summary is evidence for one pinned run, not a universal performance guarantee. Re-run `./run.sh` on the target architecture before using the result as an acceptance decision.
