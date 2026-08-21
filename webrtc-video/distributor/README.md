# On-demand WebRTC distribution

This component adds multi-viewer delivery to the video reference without
changing the producer. MediaMTX starts one lightweight Go adapter when the first
viewer requests a device path. The adapter resolves that device through the
Next.js control plane, pulls its strict WHEP source through rstream, repairs the
shared upstream, and publishes one H.264 track back into the same MediaMTX
process. Later viewers share that publisher; the final viewer leaving stops it.

Direct WebRTC remains the default for one-to-one delivery. Distribution is a
backend selected by the platform, not a fork of the capture or player code.
The [distribution guide](https://rstream.io/guides/distribute-webrtc-video-with-mediamtx-and-rstream)
places this backend in the complete producer and Next.js series.

## Delivery profiles

| Profile               | Device uplinks | Producer leg                                      | Viewer leg                     | Use it when                                                         |
| --------------------- | -------------: | ------------------------------------------------- | ------------------------------ | ------------------------------------------------------------------- |
| Direct                | one per viewer | TWCC, NACK/RTX, FlexFEC, bounded pacer            | same end-to-end session        | one viewer needs the shortest path and end-to-end adaptation        |
| MediaMTX native pull  |     one shared | NACK and negotiated TWCC with fixed source pacing | MediaMTX NACK and TWCC         | a static source accepts the reduced MediaMTX 1.20 offer             |
| MediaMTX with adapter |     one shared | TWCC, NACK/RTX, FlexFEC, bounded pacer            | independent MediaMTX NACK/TWCC | a product needs dynamic sources, complete source repair and fan-out |

The native profile is intentionally retained as an interoperability option. It
removes the adapter when its smaller feature set and static source contract are
enough. The producer must opt into the bounded MediaMTX-native offer profile;
strict profiles do not relax their WHEP validation. Draft 04 requires
`rtcp-mux-only` and one common `msid` on the active media sections; MediaMTX
1.20 emits neither in its source offer and its player does not complete a `406`
counter-offer exchange. The opt-in accepts only those two known differences,
continues to require BUNDLE and RTCP multiplexing, and disables RTX, FlexFEC,
and adaptive source encoding for that session. The adapter profile keeps the
strict producer contract and is the reference product path.

MediaMTX exposure is independent of these profiles. A public deployment gives
the browser an HTTPS MediaMTX URL. A private deployment publishes the same
WHEP listener with an authenticated rstream HTTP tunnel. In both cases media
uses ICE over UDP or TURN; the HTTP endpoint carries signaling, not RTP.

### Native pull for one static source

Native pull uses the same producer binary with an explicit compatibility
setting:

```yaml
web:
  whep:
    allowMediaMTXNativeOffer: true
```

Point one MediaMTX path at that producer. `wheps://` selects WHEP over HTTPS;
when the producer tunnel enforces rstream edge authentication, place its
short-lived connect token in `whepBearerToken`.

```yaml
paths:
  camera:
    source: wheps://producer.example/whep
    whepBearerToken: "short-lived-rstream-connect-token"
    sourceOnDemand: true
```

This profile is deliberately static: MediaMTX receives one source URL in its
configuration and pulls it on first demand. It is useful for a small fixed
deployment, but it does not provide the platform's per-device resolver or
automatic direct fallback. The producer fixes pacing for this native source
session because MediaMTX 1.20 does not negotiate the RTX/FlexFEC profile used
by the adaptive adapter leg.

The adapter terminates source repair before publishing a fresh downstream RTP
flow. Source transport-wide sequence numbers never cross into the MediaMTX
leg; MediaMTX and each browser establish their own feedback and retransmission
state. This separation prevents feedback from one viewer from controlling the
device uplink used by every viewer.

The current distributor carries one H.264 rendition. Downstream TWCC describes
each viewer leg, but MediaMTX does not transcode that rendition or propagate a
viewer estimate back to the shared producer encoder. Fan-out is therefore
qualified only for viewers whose path can sustain the selected source profile.
Use direct delivery when one constrained viewer needs end-to-end encoder
adaptation. Heterogeneous audiences need a measured rendition ladder or
external transcoding workers before this boundary can be raised.

## Process and failure model

MediaMTX owns demand. One path creates at most one adapter, regardless of the
number of readers. A source or destination failure ends the current attempt,
closes both peer connections, and releases its HTTP media resources. While the
same demand remains active, the adapter resolves fresh credentials and retries
after a jittered exponential delay bounded between one and 15 seconds. Thirty
seconds of stable forwarding resets that delay. This keeps recovery inside one
MediaMTX-owned process instead of enabling an uncontrolled external restart
loop. Network failures, an offline source, throttling, and server errors are
retryable. Rejected credentials, invalid resolver or WHEP/WHIP contracts, and
local resource invariants stop the process immediately so a broken deployment
cannot hammer the control plane. `SIGINT`/`SIGTERM` interrupts both forwarding
and backoff immediately.

The forwarding pipeline has bounded packet, repair, and worker queues. It
reorders media for at most 300 ms, retries NACK feedback at a bounded cadence,
expires missing packets after one second, and stops instead of accumulating an
unbounded live-stream backlog. PLI and FIR requests cross back to the source;
viewer NACK and TWCC remain local to the MediaMTX hop.

The reference configuration admits at most eight readers on one device path.
That boundary is deliberate: the fan-out qualification drives a decoder-valid
8 Mbit/s H.264 stream through one, four, and eight viewers, rejects a ninth
viewer without interrupting the existing eight, then replaces readers under
churn while retaining one source. Override `MTX_PATHDEFAULTS_MAXREADERS` only
after repeating the same qualification on the target host and network. This is
a per-path guard, not a global instance capacity policy; a multi-tenant pool
still needs a measured aggregate admission boundary.

## Build the combined image

The official MediaMTX image is distroless and cannot execute `runOnDemand`
commands. The supplied image copies the pinned MediaMTX 1.20 binary and the Go
adapter into an unprivileged Alpine runtime with a shell and CA roots. Both
processes still run in one container.

```bash
docker build -t rstream-video-distributor:local .
```

For a repeatable single-host deployment, copy the reviewed example and start
the supplied Compose service:

```bash
cp distributor.env.example distributor.env
# Replace every example origin, host, and secret before continuing.
docker compose up --build -d
docker compose ps
```

The Compose profile runs the same combined image read-only, drops Linux
capabilities, enables `no-new-privileges`, binds WHEP/WHIP and metrics to
loopback, and exposes only the ICE UDP port. It does not introduce a second
server: MediaMTX owns the on-demand adapter as a child process in the same
container. `docker compose down` drains MediaMTX and its active adapter within
the configured ten-second stop window.

The image listens on TCP `8889` for WHEP/WHIP control, UDP `8189` for ICE,
and TCP `9998` for OpenMetrics. Publish the metrics port on loopback or a
private monitoring network; the configuration excludes only that action from
JWT authentication.

## Configure the control plane

Generate the platform and distributor signing keys once. Give each MediaMTX
instance a stable deployment identity:

```bash
cd ../platform
npm run mediamtx:key -- mediamtx-one
```

Set the matching values on the platform and MediaMTX. MediaMTX configuration
keys can be overridden through its `MTX_...` environment convention.

```bash
# Next.js
VIDEO_DISTRIBUTOR=mediamtx
MEDIAMTX_EXPOSURE=public
MEDIAMTX_PUBLIC_URL=https://media.example
MEDIAMTX_TUNNEL_NAME=
MEDIAMTX_JWT_PRIVATE_KEY_BASE64=...
MEDIAMTX_JWT_ADDITIONAL_JWKS='{"keys":[]}'
MEDIAMTX_JWT_ISSUER=rstream-webrtc-video-platform
MEDIAMTX_JWT_AUDIENCE=rstream-mediamtx
MEDIAMTX_SOURCE_RESOLVER_JWKS='{"keys":[...]}'
MEDIAMTX_SOURCE_RESOLVER_ISSUER=rstream-video-distributor
MEDIAMTX_SOURCE_RESOLVER_AUDIENCE=rstream-video-source-resolver

# Combined MediaMTX container
MTX_AUTHJWTJWKS=https://platform.example/api/video/distributor/jwks
MTX_AUTHJWTISSUER=rstream-webrtc-video-platform
MTX_AUTHJWTAUDIENCE=rstream-mediamtx
MTX_WEBRTCALLOWORIGINS=https://platform.example
MTX_WEBRTCADDITIONALHOSTS=media.example
MTX_PATHDEFAULTS_MAXREADERS=8
RSTREAM_SOURCE_RESOLVER_URL=https://platform.example/api/video/distributor/source
RSTREAM_SOURCE_RESOLVER_INSTANCE_ID=mediamtx-one
RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64=...
RSTREAM_SOURCE_RESOLVER_ISSUER=rstream-video-distributor
RSTREAM_SOURCE_RESOLVER_AUDIENCE=rstream-video-source-resolver
RSTREAM_MEDIAMTX_URL=http://127.0.0.1:8889
```

The example above uses a public MediaMTX ingress. To keep the HTTP listener
private, set `MEDIAMTX_EXPOSURE=rstream`, clear `MEDIAMTX_PUBLIC_URL`, publish
`8889/tcp` through an authenticated rstream tunnel, and set
`MEDIAMTX_TUNNEL_NAME` to its name. UDP `8189` must either be reachable through
the advertised host or be complemented by a STUN or TURN entry under
`webrtcICEServers2` in both exposure modes.

The adapter can resolve sources in two ways. A static deployment sets exactly
one `RSTREAM_SOURCE_URL`, plus `RSTREAM_SOURCE_AUTHORIZATION` when the source
uses its own bearer. A product deployment sets `RSTREAM_SOURCE_RESOLVER_URL`
and the distributor identity variables shown above. The resolver maps each
`devices/<uuid>` path to fresh producer, publisher, and TURN credentials. The
two source modes are mutually exclusive so a stale static URL cannot override
product policy.

JWTs bind `read` or `publish` to exactly one `devices/<uuid>` path. Browser
tokens, adapter publisher tokens, producer credentials, and the resolver
identity are separate. Every resolver call carries a fresh Ed25519-signed JWT
with a 20-second lifetime, a cryptographic nonce, the deployment instance, the
exact media path, and the resolution purpose. The private key stays inside one
MediaMTX deployment; the platform stores only public keys and can accept an old
and new key together during rotation. `session` establishes a peer and
therefore includes TURN material; `signaling` refreshes only the source and
destination authorizations used by an existing resource. A signaling refresh
returns an empty ICE-server list and never calls the TURN keyring or consumes
its quota. Both sides reject a missing or unknown purpose instead of silently
falling back to the more expensive operation.

The producer leg keeps edge and application authentication independent. The
short-lived rstream connect token is the single `rstream.token` query value on
the WHEP URL; `Authorization` is reserved for a producer-owned credential and
is empty in this reference deployment. The adapter carries a rotated edge
token onto the existing opaque WHEP resource without changing its origin,
path, or other query parameters. Static deployments use the same contract:
place the rstream token in `RSTREAM_SOURCE_URL` and use
`RSTREAM_SOURCE_AUTHORIZATION` only when the producer itself requires a bearer.

MediaMTX 1.20 validates the JWT when it creates a WHEP or WHIP session. In this
implementation, the returned resource URL then acts as an opaque capability:
PATCH and DELETE are bound to its random session identifier and do not
revalidate a later JWT.
Treat every `Location` value as a secret, keep it on HTTPS, and never place it
in logs or metrics. The rstream HTTP tunnel adds an independent
short-lived edge token to every lifecycle request. JWT expiry prevents a new
session; it does not revoke one that is already carrying media. Explicit
revocation therefore needs to terminate that session rather than merely stop
issuing tokens. This behavior is locked by the real MediaMTX authentication
test. URL-bound authentication is permitted independently by
[WHEP draft 04](https://datatracker.ietf.org/doc/html/draft-ietf-wish-whep-04#section-4.9.1)
for read sessions and by
[RFC 9725](https://www.rfc-editor.org/rfc/rfc9725.html#section-4.7.1) for WHIP
publish sessions; it is not an implicit property of every WHEP or WHIP server.

## Observe the two media legs

The producer exporter describes capture, encoding, TWCC/GCC, pacing, queue
residence, RTX, and FlexFEC on the shared device uplink. MediaMTX exposes path
readiness, reader count, and inbound/outbound bytes:

```text
GET http://127.0.0.1:9998/metrics?type=paths&path=devices/<device-id>
```

Keep device identity in scrape-target labels rather than adding session or
viewer identifiers in application metrics. The adapter's structured shutdown
log distinguishes received recovery packets, repairs delivered before the
reorder window closes, and RTX/FlexFEC packets that arrived too late. The
current on-demand process deliberately does not open a second metrics listener
per device.

## Qualification

```bash
go test ./...
go test -race ./...
go vet ./...
go test -race -tags=integration ./internal/bridge
make qualify-fanout OUT=/tmp/rstream-video-distributor
```

The integration suite starts the real MediaMTX 1.20 binary. It proves that two
viewers create one source WHEP session, injects and repairs a missing H.264 RTP
packet with FlexFEC, then repeats with the first FEC packet suppressed and
requires RTX recovery. Every viewer must receive the complete ordered range.
The source offer and both viewer sessions must also negotiate transport-wide
congestion control, and the source harness must receive TWCC feedback.
The suite also reads the live MediaMTX path metrics, closes the source after the
last viewer, restarts without stale state, and recovers after a rejected source
negotiation. A separate native-pull test locks the capability difference that
makes the two profiles explicit.

The fan-out qualification runs three independent passes with a real
constrained-baseline H.264 GOP at approximately 8 Mbit/s. It requires constant
device ingress, linear MediaMTX egress, identical payload delivery to all
readers, one source lifecycle, bounded process count, CPU and memory, and
source TWCC feedback. At the eight-reader limit it requires a fast rejection
of the ninth reader, uninterrupted delivery to the existing readers, and
twelve successful leave-and-replacement cycles. Any MediaMTX B-frame, packet
loss, source-timeout, or adapter-failure diagnostic fails the run rather than
being hidden behind aggregate byte counters.

The finite browser qualification uses a real rstream context and enables edge
authentication by default:

```bash
RSTREAM_CONTEXT="<staging-context>" \
RSTREAM_DISTRIBUTOR_MODE="mediamtx" \
qualification/end-to-end/run.sh /tmp/rstream-video-result
```

The runner identifies the exact temporary producer tunnel and project before
minting a WHEP-path-scoped connect token. The token remains in process memory
and the WHEP URL; result files record only that edge authentication was active.
Use `RSTREAM_DISTRIBUTOR_MODE=direct` for the one-to-one reference. Setting
`RSTREAM_DISTRIBUTOR_EDGE_AUTH=false` is useful only when isolating local media
behavior and does not satisfy the release authentication gate.

The browser runner can apply capacity, delay, jitter, loss, and an explicit
playout target to the viewer leg. It correlates first-frame timing, decoded
frame rate, freezes, source OpenMetrics, packet repair, traffic-control
counters, recovery, and WHEP resource balance. Direct delivery must adapt the
producer target to a capacity step. The current single-rendition MediaMTX mode
is intentionally rejected when the viewer cannot sustain the source rate;
downstream feedback cannot create a lower rendition or control the shared
producer encoder.

The custom adapter has a separate causal test for its shared source leg. These
variables shape UDP after it leaves the producer and only when its destination
is the adapter:

```bash
RSTREAM_CONTEXT="<staging-context>" \
RSTREAM_DISTRIBUTOR_MODE="mediamtx" \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS="5000" \
qualification/end-to-end/run.sh /tmp/rstream-video-source-capacity
```

Use `RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT` for repair testing; delay,
jitter, and queue controls use the same `SOURCE_` prefix. Source and viewer
impairment cannot be enabled in one run because that would make the observed
reaction causally ambiguous. The result records the selected network
namespace, destination, traffic-control counters, TWCC response, encoder
target, RTX/FlexFEC repair, decoded frame rate, freezes, and recovery.
