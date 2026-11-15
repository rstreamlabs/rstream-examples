# Build and configure the distributor

Return to the [distributor overview](../README.md) for the application path. Run commands from `webrtc-video/distributor`, unless a step changes directory.

## Build the combined image

The official MediaMTX image is distroless and cannot execute `runOnDemand`
commands. The supplied image builds MediaMTX 1.21.3 from checksum-pinned sources
with the bounded H.264 patch described below, then copies it and the Go adapter
into an unprivileged Alpine runtime with a shell and CA roots. Both processes
still run in one container.

```bash
make image
```

### H.264 access-unit budget

An H.264 image can contain many independently encoded slices. The upstream
50-NAL-unit limit rejects valid high-slice sources, even when the complete image
is small. The sample runtime raises that count to **1,024 NAL units per access
unit** and retains the upstream **8 MiB byte limit**. This applies uniformly to
all sources; it does not select devices, resolutions or encoder brands. The
maximum slice-header table is 24 KiB on a 64-bit host, excluding bounded Go
slice allocation overhead. Media payloads remain subject to the byte limit.

[`mediamtx-build/sources.json`](../mediamtx-build/sources.json) pins MediaMTX,
mediacommon and gortsplib module checksums and source revisions. The builder
verifies module archive contents, applies one production constant change in a
fresh temporary directory, and refuses a changed patch context. It does not
modify the Go module cache. The generated binary identifies itself as
`v1.21.3-rstream.1`; its adjacent `.build.json` records the source checksums,
budgets and binary SHA-256. The container retains this manifest at
`/usr/local/share/mediamtx.build.json` and the three upstream license notices at
`/usr/local/share/licenses/mediamtx/LICENSES`.

Run `make mediamtx-test` with Python 3, Go and FFmpeg installed. This runs the
upstream H.264 codec and RTP decoder tests under the race detector, tests single
NAL, STAP-A and FU-A input at and above the count/byte bounds, and verifies
recovery after rejection. A real 64-slice video then passes through MediaMTX
RTSP delivery and fMP4 recording. All received and recorded images must decode
to the original frame hashes. The recording fixture includes a trailing GOP beyond its 120 qualification
frames: fMP4 needs the next timestamp, and recorder shutdown can discard queued
lookahead data. All 120 qualification frames and every recorded tail frame must
match; missing or altered frames within that interval fail the test.
The regular distributor integration suite separately checks real WebRTC
fan-out, recovery and lifecycle against the same patched runtime.

When upgrading MediaMTX, review all three pins and the patch, rerun these tests
and the platform authorization/recording checks, and remove the patch when the
upstream budget covers these cases. Never silently fall back to the unpatched
image. Record the selected runtime version with each new test run.

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

MediaMTX 1.21.3 validates the JWT when it creates a WHEP or WHIP session. In this
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
