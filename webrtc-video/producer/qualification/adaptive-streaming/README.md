# Adaptive streaming qualification

This qualification pack measures the adaptive H.264 sender under changing
bandwidth, latency, and packet loss. It compares a managed rstream TURN path
with an isolated direct reference, then moves the active producer between two
interfaces to qualify Trickle ICE and ICE restart on the recovered session.

## What the harness measures

Each run starts the exact producer source in an ephemeral Linux container and
connects the same pinned headless Chromium image in its own container. Direct
and relay therefore differ by network path, not browser version, host operating
system, or socket namespace. The harness records browser WebRTC statistics once
per second together with the sender's TWCC/GCC estimate, encoder target, and
bounded-pacer state. Frame rate alone can hide heavily compressed output, so
the qualification runtime also captures the pinned x264 encoder's per-frame
quantization parameter from timestamped diagnostics. Lower average QP is
better; the full relay profile must remain below the absolute quality ceiling
and close to the identical direct reference. Chromium separately proves the
decoded resolution, frame rate, freeze time, and decode cost. These signals are
complementary: QP measures compression pressure, while receiver statistics
measure transport and playback continuity.

The harness also samples UDP counters once per second inside both isolated
Linux network namespaces: the producer container and the receiver browser. A
local receive-buffer drop or a send rejection outside a shaped phase
invalidates the run because local pressure, rather than the tested network, may
have discarded a datagram. Linux can report a `netem` rejection in both the
qdisc drop counter and UDP `SndbufErrors`. During each shaped phase, the analyzer
therefore accepts only the subset bounded by that interval's independently
measured qdisc drops; any excess still fails. Keeping both kernel
boundaries beside WebRTC sequence loss and sender qdisc counters makes an
unexpected burst attributable instead of merely calling it “the network”.

Both media containers record aggregate Linux CPU counters from their runtime
hosts. The report separates useful execution from hypervisor steal time for
every phase. A run whose p95 steal time exceeds 5% on either side fails: an
encoder or browser that was not scheduled predictably cannot qualify the media
pipeline or transport, regardless of its average frame rate. A 250 ms heartbeat
also records shorter runtime pauses that aggregate CPU counters cannot expose,
including pauses of a local container VM.

The impairment schedule runs as one process inside the producer network
namespace. It first holds a 32 Mbit/s profile with no added delay, jitter, or
random loss long enough for the controller to settle after qdisc activation.
The final ten seconds before the first capacity step define the causal
reference for that run. Every target in that window must remain within 20% of
its median, which is the smallest reduction accepted as a congestion response,
and the final sample must remain within 10%. This admits a bounded GCC transient
without allowing pre-existing target movement to masquerade as a response to
the capacity step. Congestion reduction and recovery are measured from that
bounded reference, while the earlier unshaped
baseline remains the healthy-link quality reference. A real relay may therefore
settle below an initial access-link peak without turning that peak into an
artificial recovery requirement.
The capacity experiment then changes only the rate while the path steps through
16, 12, 8, and 4 Mbit/s. Additional delay, jitter, and loss begin in a separate
phase.

Recovery follows the same discipline. It first removes loss, delay, and jitter
while capacity remains at 4 Mbit/s, then raises capacity to 32 Mbit/s. The
measured response time starts at that second event.
After the recovery measurement, a zero-delay, zero-loss drain profile with
capacity 20 times the encoder ceiling empties the qdisc before teardown.
Deleting a populated qdisc would create an artificial loss burst and
misclassify teardown as transport recovery.
Docker API latency remains outside the measured phase durations, including
when the producer runs on a separate daemon. An interruption removes the qdisc
before the container is cleaned up.

Media already accepted for packetization drains at its admission rate after a
target decrease, while newly encoded access units immediately use GCC's current
budget. New access units that exceed the 225 ms admission envelope are dropped
whole before packetization. Recovery resumes on a key frame once the queue can
contain its most recently observed size plus 25% headroom.
The report separately exposes encoder requests, complete frame drops, the
key-frame reserve, actual packet residence time, and prospective sustained-rate
backlog. These signals make the latency bound, sequence continuity, and egress
rate directly verifiable.

The link moves through six measured phases:

| Phase        | Media path                                                 |
| ------------ | ---------------------------------------------------------- |
| warmup       | unshaped reference                                         |
| baseline     | unshaped quality ceiling                                   |
| conditioning | 45 s at 32 Mbit/s; no delay, jitter, or random loss        |
| constrained  | 16, 12, 8, then 4 Mbit/s; no delay, jitter, or random loss |
| impaired     | 4 Mbit/s; 120 ms delay; 30 ms jitter; 2% random loss       |
| recovery     | 4 then 32 Mbit/s; no delay, jitter, or random loss         |

For a direct run, the Linux traffic-control filter applies to outbound UDP on
the isolated producer-to-browser address. It deliberately does not pin the
initial destination port: ICE may switch to another valid host candidate when
the first pair is congested, and a port-pinned rule would then silently stop
shaping the session. The private Docker network contains only this producer and
browser, so the address selector still excludes unrelated traffic while
covering media, RTCP, DTLS, and ICE consent on every candidate pair. A relay run
forces both peers through one managed TURN/UDP path and shapes the stable
producer-to-TURN server address and port. The selected producer relay
candidate supplies the address after ICE stabilizes; resolving the TURN name a
second time could select a different backend behind a load-balanced DNS name.
The scheduler also stops after its first capacity step if the selective branch
has not carried a packet. Media, TURN permissions, and TURN
channel traffic necessarily share that physical branch; the producer's HTTP
tunnel, signaling, rstream control connection, and unrelated traffic still
bypass it. The manifest records the selector, including whether the destination
port is matched, and interval filter counters prove that packets kept traversing
it after every phase change.

The `netem` queue defaults to 256 packets. At the reference 4 Mbit/s profile,
this covers the 120 ms propagation-delay bandwidth product, its 30 ms jitter
envelope, and one bounded sender-reaction interval. The former 128-packet limit
sat close to full under normal FlexFEC/RTX bursts and manufactured tail loss far
above the configured 2%. A much larger queue could instead hide congestion
behind seconds of bufferbloat. Override the default only for an explicit queue
sensitivity experiment with `RSTREAM_QUALIFICATION_QUEUE_LIMIT_PACKETS`; the
manifest records the effective value. The report retains actual qdisc and
filter counters so the configured profile is verified rather than assumed.
Each interval records counters immediately after the qdisc change and again at
its end. The analyzer therefore remains correct whether Linux preserves or
resets counters on `tc qdisc change`, and computes loss as
`drops / (forwarded packets + drops)`. The transition to recovery passes only
when its drain profile carries media, adds no qdisc drops, and ends with an
empty queue.

## Paths and protection profiles

The harness supports two path kinds:

- `direct` uses an isolated Docker bridge, no rstream tunnel, and no TURN.
- `relay` publishes the producer through rstream and forces both browser and
  producer through managed TURN/UDP. This stricter, symmetric path avoids
  relying on a host-specific NAT shortcut. Both selected paths are required to
  remain stable before measurement starts and are verified on every sample.

It also supports two protection profiles:

- `nack-rtx` uses reactive NACK feedback and RTX retransmissions.
- `nack-rtx-flexfec` adds proactive FlexFEC-03 protection. Its default is one
  repair packet per five media packets; ratio overrides support explicit stress
  comparisons.

### Symmetric relay qualification

A one-sided relay depends on the other peer exposing a reachable candidate. The
containerized producer exposes private host candidates, so the release profile
relays both peers through one explicit TURN/UDP credential. This produces one
stable transport branch, avoids idle fallback allocations, and identifies the
exact flow being impaired. Transport fallback has its own qualification path;
the video comparison measures one selected route rather than an opportunistic
ICE outcome.

FlexFEC is not free capacity. GCC controls the complete paced wire budget, and
the producer derives the encoder's media share after reserving the configured
repair ratio. With the reference `1/5` profile, a 1.2 Mbit/s wire budget
provides 1 Mbit/s to media before RTP, UDP, IP, and occasional RTX overhead. At
the qualified 4 Mbit/s wire point the theoretical media share is about 3.33
Mbit/s; the 2 Mbit/s encoder floor leaves measured room for packetization,
reactive repair, and transient overshoot. Chromium does not acknowledge the
FlexFEC stream through TWCC, so those packets remain outside GCC's loss and
received-rate calculations while remaining inside its capacity budget.

The sender uses one real-time envelope for media bursts and repair. Its
sustained target is GCC's complete wire budget; the token bucket permits short
bursts at 1.5 times that rate without changing the long-term allowance. Every
recorded sample carries the media target, wire target, and pacing envelope, and
the qualification fails if their relationship diverges from the configured
protection ratio.

Pion's delay and loss estimators remain the primary congestion controller. A
bounded feedback-loss guard closes one coordination gap between them: two
consecutive valid TWCC reports above 10% loss reduce the current media target
immediately, even if the delay estimator has not emitted a new callback. The
guard ignores reports with fewer than 20 statuses and isolated spikes. After a
second of reports below 2% loss, its cap rises in 5% steps every 200 ms until
Pion's target takes over again. This prevents a transient queue from becoming a
loss/RTX/FlexFEC amplification loop without replacing GCC's normal bandwidth
estimate. The phase report exposes the guard target, peak reported loss, and
every reduction and recovery.

Encoder hysteresis cannot spend the same headroom twice. Startup validation
therefore derives the largest safe decrease threshold from the selected repair
ratio and the shared pacing envelope. The qualified profiles use immediate
decreases; the `2/4` stress ratio consumes the complete 1.5× allowance, while
the default `1/5` ratio retains margin for packetization and reactive repair.

Pion distributes protected media packets across independent XOR groups when a
profile uses several repair packets. In the `2/4` stress profile, each repair
covers one two-packet group; the pair does not recover any arbitrary two losses
in the complete four-packet window. This topology is recorded with the ratio so
the stress result cannot be misread as a generic two-loss guarantee.

Packet repair and playback buffering solve different parts of continuity.
NACK/RTX and FlexFEC recover missing media; the receiver jitter buffer absorbs
arrival-time variation and gives repair packets a bounded window to arrive.
The runner can set Chromium's minimum receiver hint with
`RSTREAM_QUALIFICATION_PLAYOUT_DELAY_HINT_SECONDS`. It records both the
configured hint and the receiver's effective target delay. The target must stay
below 250 ms, so a smooth result cannot be obtained by hiding several seconds
of latency in the player. The qualification profile requests 200 ms and still
requires the measured target to remain below 250 ms.

## Run one scenario

Requirements are Docker with `NET_ADMIN` support, Node.js, jq, and an
authenticated rstream CLI context. Go builds the context-preparation helper by
default. A qualification host without Go can use the same audited helper as a
prebuilt binary through `RSTREAM_QUALIFICATION_PREPARE_CONTEXT_BINARY`. The
runner requires an explicit non-production context, making the environment
under test unambiguous.

A remote controller can instead receive the four files generated by the helper
and set `RSTREAM_QUALIFICATION_PREPARED_RUNTIME_DIRECTORY`. In that mode, the
runner needs neither Go nor the rstream CLI. It validates the prepared input,
copies it into its own private runtime, and removes that copy on exit. The
caller remains responsible for removing the source directory after the run.

```bash
RSTREAM_CONTEXT=your-context \
  ./qualification/adaptive-streaming/run.sh
```

Select the path, protection, context, and output explicitly when needed:

```bash
RSTREAM_QUALIFICATION_PATH=relay \
RSTREAM_QUALIFICATION_PROTECTION=nack-rtx-flexfec \
RSTREAM_CONTEXT=my-context \
  ./qualification/adaptive-streaming/run.sh \
  ./qualification/adaptive-streaming/.artifacts/my-run
```

The direct reference uses the same media, codec, adaptation, protection, and
browser image. Only tunnel publication and TURN are disabled. Its Chromium
viewer runs on the same isolated Docker bridge, which prevents the host route
or an external service from silently changing the comparison.

The direct reference must reach the configured 8 Mbit/s media ceiling on its
healthy phase. A relay run crosses the qualification host's real access link
and managed TURN, so its unshaped ceiling is not assumed. It must instead keep
the available path full enough to preserve the declared quality floor, remain
stable under added loss, and satisfy the direct-versus-relay bounds in the
matrix. This keeps a limited uplink visible without attributing it to rstream
or converting it into an artificial release failure.

Release evidence can place the relay producer on a separate Docker host while
the browser stays on the machine running the harness:

```bash
docker context create video-producer \
  --docker host=ssh://qualification-host

RSTREAM_QUALIFICATION_PATH=relay \
RSTREAM_QUALIFICATION_PROTECTION=nack-rtx-flexfec \
RSTREAM_QUALIFICATION_PRODUCER_DOCKER_CONTEXT=video-producer \
  ./qualification/adaptive-streaming/run.sh \
  ./qualification/adaptive-streaming/.artifacts/split-host-relay
```

This topology removes competition between the encoder and the instrumented
Chromium receiver, and exercises two independent network legs through TURN.
The same runner still owns the phase schedule, qdisc, socket counters,
artifacts, and cleanup. It records both Docker runtimes in the manifest and
copies only the ephemeral producer configuration to a private remote volume.
Automation that already exposes a Docker endpoint can use
`RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST` instead of a context. The two
selectors are mutually exclusive. Direct qualification remains single-daemon
because its isolated bridge is the reference path.

The runner gives the producer its selected CLI credential through an ephemeral
mode-`0600` file. Interruptions remove that file, the containers, the private
Docker network, and the private runtime directory. Retained manifests capture
the topology and tool versions required to reproduce the result.

## Qualify Trickle ICE mobility

The relay harness can move the producer to a second isolated interface while
the session is playing. This scenario forces the rstream upstream to QUIC, so a
successful publication proves the signaling transport in use. The producer
keeps that original QUIC client while the browser preserves its WHEP resource,
performs an in-place ICE restart, and lets WebRTC select the replacement TURN
path.

```bash
RSTREAM_CONTEXT=your-context \
RSTREAM_QUALIFICATION_PATH=relay \
RSTREAM_QUALIFICATION_PROTECTION=nack-rtx-flexfec \
RSTREAM_QUALIFICATION_MOBILITY=producer \
  ./qualification/adaptive-streaming/run.sh \
  ./qualification/adaptive-streaming/.artifacts/relay-mobility
```

The mobility verdict requires one producer address change, a newly trickled
candidate, a selected candidate-pair switch, one WebRTC peer connection, one
WHEP resource with no intervening DELETE or failed request, one successful
restart PATCH, and playback recovery within 15 seconds. The manifest records
both interfaces and the switch point. Mobility is explicit and relay-only, so
the normal release matrix keeps one stable path per run.

## Run the release matrix

The release-oriented command runs both paths and both protection profiles, then
generates a single comparison report:

```bash
RSTREAM_QUALIFICATION_REPETITIONS=3 \
  ./qualification/adaptive-streaming/run-matrix.sh
```

Three repetitions produce twelve runs. Individual NACK/RTX diagnostic runs may
fail under the loss/latency phase; the matrix still completes so that the
proactive profile can be compared against the same baseline. The final command
fails unless:

- every expected run produced a summary from the same clean producer tree;
- every full direct run forces a congestion response, while every relay run
  receives valid TWCC feedback and does not increase its encoder target under
  additional pressure; a relay whose stable pre-transition target already fits
  the shaper budget is not required to manufacture a 20% target change;
- every full-profile direct and relay run passes its per-run criteria;
- relay median output remains at least 20 fps and frozen time at most 10%;
- every full-profile run exposes valid timestamped x264 QP telemetry and relay
  stays at or below QP 42;
- relay remains within 5 fps, six QP points, and five frozen-time percentage
  points of direct;
- proactive repair materially improves a degraded NACK/RTX relay baseline, or
  does not materially regress an already healthy baseline.

When a separate producer Docker daemon is configured, the matrix uses it for
relay runs and keeps direct references on the browser daemon. This preserves
the isolated direct bridge while removing producer/browser CPU contention from
the end-to-end TURN evidence.

`comparison.md` is the human report and `comparison.json` is the automation
verdict. The three `comparison-*.svg` figures separate frame cadence,
quantization, and frozen time so each visual answers one question.

The matrix uses one 4 Mbit/s wire bottleneck for the default `1/5` profile and
the explicit `2/4` stress comparison. The encoder has a 2 Mbit/s media floor:
lower targets kept frames flowing but drove x264 to QP 49–51, which is not
acceptable evidence of healthy video. Four Mbit/s leaves the default profile
room for packetization and RTX while still forcing a measurable downshift, and
it gives the stress ratio a deliberately tighter repair budget. This is the
qualified test point, not a universal minimum for every camera or link.
Production H.264 examples use the same 2 Mbit/s quality floor. Override the
test capacity with `RSTREAM_QUALIFICATION_CAPACITY_KBPS`; the manifest records
it and the matrix rejects runs that do not use one identical profile.

## Read one result

Start with the qualification-decision table in `summary.md`. It places the
observed value beside each release threshold. Three focused figures show the
rate response, decoded frame rate, and NACK/RTX activity. The comparison matrix
adds one frozen-time view for the relay protection profiles. Detailed counters
remain in `metrics.csv` and `summary.json`.

The capacity experiment keeps delay, jitter, and random loss at zero, so its
rate response has one controlled cause. The following impairment phase holds
capacity at 4 Mbit/s while adding delay, jitter, and loss. Its results qualify
continuity and repair, independently of the capacity downshift.

The setup timeline deliberately separates Docker builds from service
establishment. Its connection duration starts immediately before the producer
container, then covers publication, browser startup, signaling, ICE, and the
selected media path. This prevents a cold image build from being misreported as
rstream or TURN connection latency.

The acceptance checks reject, among other cases:

- the wrong ICE path or a disconnected session;
- insufficient phase samples or decoded-frame progress;
- missing host scheduling evidence, sustained hypervisor steal time, or a
  runtime pause longer than 350 ms between 250 ms heartbeat samples;
- excessive frozen time or low decoded frame rate;
- missing jitter-buffer evidence, a requested target above 250 ms, or a phase
  whose average effective buffered delay exceeds 300 ms; per-sample values
  remain visible in the transport figure;
- missing quality telemetry, excessive H.264 quantization, or a resolution
  change hidden behind a healthy frame count;
- encoder cadence with a p99 gap above 50 ms, any gap above 200 ms, or more
  than 1% late or catch-up intervals in a measured phase;
- an encoder that does not react/recover when the constrained budget requires
  adaptation;
- a negotiated repair mechanism that sends no repair packets under loss;
- an unbounded/overloaded sender queue;
- missing or malformed TWCC feedback, or loss accounting that diverges sharply
  from the independently captured traffic-control counters;
- a traffic-control rule that stops handling the selected WebRTC transport,
  including after an ICE candidate-pair switch;
- a configured or observed qdisc loss profile outside its budget.

The controller's `Average loss` is an internal exponentially weighted feedback
signal, not the physical packet-loss percentage. It can temporarily be much
higher than the injected 2% when a feedback window reports clustered missing
transport sequence numbers. Use the separately reported qdisc counters and
receiver packet/NACK counters to reason about physical loss.

The raw investigation set is `samples.jsonl`, `receiver-udp.jsonl`,
`producer-udp.jsonl`, `setup-timeline.jsonl`, `metrics.csv`, `browser.json`, `manifest.json`,
qdisc/filter JSON, and producer/browser logs. One run proves one pinned tree on
one recorded machine; release claims use the repeated matrix and should be
regenerated on each target architecture.

A relay run also measures the qualification host's real access link. An ICE
outage, severe unshaped packet loss, excessive loaded latency, or a baseline
that cannot sustain the profile's minimum wire budget invalidates that run.
The failure remains useful diagnostic evidence, but it is not converted into a
release claim. Repeat the scenario from a stable qualification host and compare
the direct reference, the unshaped relay baseline, and the shaped intervals.

For a targeted Pion subsystem investigation, pass a comma-separated debug scope
without changing the reference configuration, for example
`RSTREAM_QUALIFICATION_PION_LOG_DEBUG=turnc`. Do not enable all Pion scopes in a
normal release matrix: the resulting packet-level log is intentionally noisy.
Relay diagnostics can additionally isolate one credential URL with
`RSTREAM_QUALIFICATION_TURN_TRANSPORT=udp|tcp|dtls|tls`. The release matrix uses
one explicit UDP credential on both peers so every run measures the same active
allocation rather than creating unused fallback allocations. Set
`RSTREAM_QUALIFICATION_PRODUCER_TURN_POLICY=auto` only for candidate-selection
diagnostics; `relay` is the release default and `disabled` is reserved for the
isolated direct reference.
