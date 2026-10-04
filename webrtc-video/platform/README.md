# WebRTC Video Platform

In its default managed mode, this example shows how a third-party Next.js application can integrate `rstream` without asking devices or browser users to install the `rstream` CLI or handle long-lived rstream tokens.

The application owns the device inventory, authentication, device secrets, producer provisioning, viewer authorization, and demo data lifecycle. `rstream` remains the tunnel, TURN, token, and real-time tunnel state layer behind that product API.

This control plane builds the Go code from `../producer` in provisioning mode,
with the embedded viewer omitted from the deployment binary. Capture, encoding,
adaptation, repair, recovery, and metrics remain in that producer; this
application supplies short-lived rstream material and owns the viewer
entrypoint.

The [Next.js platform guide](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream)
walks through this control plane. It follows the [adaptive producer](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream);
the [MediaMTX guide](https://rstream.io/guides/distribute-webrtc-video-with-mediamtx-and-rstream) adds the optional distribution backend.

## One media core across the video series

The [standalone producer](../producer/) remains the media application throughout
the video series. This Next.js code adds the product boundary around it;
capture, encoding, congestion control, repair, recovery, and OpenMetrics stay
together in the same Go source tree and device process.

The series keeps those responsibilities stable across three delivery paths:

1. one producer and one browser for the standalone and diagnostic path;
2. this Next.js control plane around the same one-to-one media session;
3. a MediaMTX distribution adapter that sends one device upstream to several
   viewers while preserving direct WebRTC as an option.

The browser consumes one WHEP contract in both modes. `VIDEO_DISTRIBUTOR`
selects `direct` or `mediamtx`; the viewer component, producer binary, device
identity, and product authorization flow stay shared.

The platform's `mediamtx` backend uses the on-demand adapter. Native MediaMTX
pull remains a separate static deployment profile documented in the
[distributor README](../distributor/); it is not a third platform backend.

Distribution and exposure are separate choices. Distribution decides whether
the browser reads from the producer or from MediaMTX. Exposure decides whether
the MediaMTX WHEP endpoint has its own public HTTPS origin or is published by
an authenticated rstream tunnel. Neither choice changes the producer, the
viewer contract, or the path-scoped MediaMTX authorization.

In the default managed inventory, the producer receives only two product-level values:

```bash
API_URL=http://localhost:3000
DEVICE_SECRET=dev_...
```

It calls `POST /api/devices/tunnel` with that secret. The Next.js API validates the device, creates a short-lived rstream token that can only create the expected HTTP tunnel, and returns the tunnel configuration.

Whenever the producer needs TURN credentials, it calls `POST /api/devices/turn` with the same device secret. TURN issuance is intentionally separate from tunnel provisioning so the producer can refresh credentials on demand.

Browser viewers never receive the producer secret. When a viewer session is needed, the frontend calls `POST /api/devices/:id/viewer`. The API creates TURN credentials and a short-lived token that can only reach the selected producer's WHEP resource. The same response shape selects either the direct producer or MediaMTX backend.

The WHEP URL carries the short-lived rstream edge token as its single
`rstream.token` query value. `Authorization` remains available to the service
behind the tunnel: it is empty for the producer and contains the path-scoped
MediaMTX JWT for distributed viewers. The player uses the same response shape
for both paths and keeps the two trust boundaries separate during credential
refresh.

The dashboard uses `@rstreamlabs/react` to watch tunnel state in real time. With managed inventory, registered devices are stored in PostgreSQL; online/offline state is read from rstream tunnel state.

The app also exposes `POST /api/rstream/webhook`. rstream signs lifecycle events for this endpoint, the app verifies them with the JavaScript SDK, and tunnel lifecycle events update the device presence timestamps from the labels attached to the short-lived producer token. `tunnel.created` records when the device came online, and `tunnel.deleted` records when it was last seen before going offline.

Producer OpenMetrics stay on the device boundary. Enable the private metrics
listener in `../producer/config.provisioning.h264.yaml` and let a collector on
the device or edge host scrape it; the Next.js application does not proxy the
metrics endpoint. The producer README documents the complete series and useful
queries.

Direct delivery has one congestion domain from producer to browser. The
adapter profile has two: producer to adapter, then MediaMTX to each browser.
The producer adapts the shared upstream from feedback generated by the adapter;
viewer feedback terminates at MediaMTX and cannot make one constrained viewer
lower the shared encoder target. Producer OpenMetrics describe the first leg,
while MediaMTX and browser telemetry describe the second.

## Stack

- Next.js App Router
- NextAuth with GitHub OAuth only
- Prisma with PostgreSQL for the reference setup
- `@rstreamlabs/tunnels` for the configured Engine client, tunnel inventory, TURN credentials, fine-grained auth tokens, and signed webhook verification
- `@rstreamlabs/rstream` for shared SDK contracts and schemas used by the app
- `@rstreamlabs/react` for real-time tunnel state in the dashboard
- Tailwind CSS with small shadcn-style UI primitives

## Setup

Use Node.js 24 with npm for the platform's verified development and validation
workflow, and have a PostgreSQL database available. The integration suite uses
PostgreSQL 17 in disposable containers.

Create the environment file:

```bash
cp .env.example .env.local
```

Fill the product values:

```bash
POSTGRES_PRISMA_POOL_URL="postgresql://postgres:postgres@localhost:5432/webrtc_video_platform?schema=public"
POSTGRES_PRISMA_DIRECT_URL="postgresql://postgres:postgres@localhost:5432/webrtc_video_platform?schema=public"
NEXTAUTH_URL="http://localhost:3000"
NEXTAUTH_SECRET="replace-with-a-random-secret"
GITHUB_CLIENT_ID="github-oauth-client-id"
GITHUB_CLIENT_SECRET="github-oauth-client-secret"
CRON_SECRET="replace-with-a-random-secret"
DEMO_CLEANUP_ENABLED="false"
```

Use the pooled PostgreSQL URL for `POSTGRES_PRISMA_POOL_URL`. Use the direct, non-pooled PostgreSQL URL for `POSTGRES_PRISMA_DIRECT_URL`; Prisma uses it for migrations.

Fill the rstream application credentials and target tunnels project:

```bash
RSTREAM_CLIENT_ID="rstream-app-client-id"
RSTREAM_CLIENT_SECRET="hex-encoded-rstream-app-client-secret"
RSTREAM_PROJECT_ENDPOINT="rstream-project-endpoint"
RSTREAM_PROJECT_ID=""
RSTREAM_TURN_KEYRING_BASE_URL=""
RSTREAM_WEBHOOK_SIGNING_SECRET="whsec_..."
WATCH_TOKEN_TTL_SECONDS="120"
```

The sample resolves the engine from `RSTREAM_PROJECT_ENDPOINT`. `RSTREAM_PROJECT_ID` is optional when an endpoint is configured; when present, it is used by the SDK as the default project scope for short-lived tunnel tokens. Application TURN credentials are derived locally from the public key published for the selected TURN realm. Leave `RSTREAM_TURN_KEYRING_BASE_URL` empty when that key is served by `RSTREAM_API_URL`; set it to a separate public HTTPS origin when an interactive access gateway protects the control-plane origin. The keyring request refuses redirects and validates the bounded DER key before using it.

### Select personal or organization access

`DEVICE_ACCESS_MODE=user` is the default. Each GitHub account owns a private
inventory. For an internal installation, use:

```bash
DEVICE_ACCESS_MODE="organization"
GITHUB_ORGANIZATION="your-github-organization"
DEMO_CLEANUP_ENABLED="false"
```

Register a separate GitHub OAuth application with the internal deployment's
`NEXTAUTH_URL/api/auth/callback/github` callback. Organization mode requests
`read:org`: members must authorize that scope, and an organization administrator
must approve the OAuth application if OAuth application restrictions are enabled.
An invitation alone does not grant access: GitHub must report active membership.
Existing users must sign out and authorize again after enabling this mode.

Every protected operation rechecks membership through a bounded verifier, with
at most 60 seconds of positive caching. Failed refreshes never extend cached
membership. Previously issued short-lived credentials remain valid until their
own expiry; an already established media session is not forcibly disconnected
when membership changes. This is admission control, not immediate revocation of
active media. Stop the affected source/session when immediate eviction is needed.

With `DEVICE_INVENTORY_MODE=managed` (the default), all active members see,
create, delete, and control the same devices. Inventory,
provisioning, viewer authorization, and watch labels use GitHub's stable numeric
organization ID. `createdById` records who created a device; deleting that account
does not delete the shared device. There is no separate administrator role.

Apply `npm run prisma:deploy` before starting the upgraded application. The
migration keeps existing personal devices private and adds a database constraint
requiring exactly one owner. Changing the environment switches the visible
inventory; it does not copy or reassign devices. Restart the application after
configuration changes. Device limits are serialized in PostgreSQL per owner;
Application database connections and pool acquisition time out after five seconds;
PostgreSQL statements have a five-second server limit with a six-second client
deadline. Idle transactions terminate after ten seconds. Migration connections
use the separate direct URL and do not inherit these application limits.
Request-rate quotas are bounded per process, so deployments with several replicas
should also apply their own shared ingress rate limits.

### Select managed or discovered inventory

| Access         | Inventory    | Device registration                                                                       |
| -------------- | ------------ | ----------------------------------------------------------------------------------------- |
| `user`         | `managed`    | Each account creates devices and receives a provisioning secret.                          |
| `organization` | `managed`    | Members share provisioned devices.                                                        |
| `organization` | `discovered` | Existing project credentials publish labeled video tunnels; devices appear automatically. |

For producers already using the rstream CLI, add:

```bash
DEVICE_INVENTORY_MODE="discovered"
RSTREAM_PROJECT_ID="your-project-id"
DEVICE_DISCOVERY_HISTORY_ENABLED="true"
```

Keep `RSTREAM_PROJECT_ENDPOINT` configured for engine/TURN resolution; its
resolved project must match the explicit ID. Personal access with discovery is
rejected. Database accounts and sessions remain in use. With history enabled,
the platform remembers stable UUIDs, names and first/last observed presence in a
separate project-scoped table. Setting history to `false` lists only connected
devices and neither reads nor writes that table; it does not erase existing
history. Neither setting changes the managed inventory. Restart after changing
configuration and apply migrations before starting the application.

The discovery contract is a published HTTP tunnel with token authentication,
with these labels:

```yaml
labels:
  app: webrtc-video-platform
  inventory: discovered
  device: 85a6703e-04de-42b6-93ac-c3b70c4cab51
  device-name: Front camera
```

Generate a different lowercase UUID once for each device and retain it across
restarts. `device-name` is optional, accepts up to 80 UTF-8 bytes without control
characters and may change without changing identity. Without it, the UI uses
`Device <UUID prefix>`. Concurrent tunnels with the same UUID are rejected;
unrelated HTTP/WebTTY tunnels are ignored. Labels describe devices inside the
configured project; restrict who can publish tunnels in that project.

On a producer whose CLI context already selects that project and credentials:

```bash
cd webrtc-video/producer
make build-no-web
export VIDEO_DEVICE_ID="85a6703e-04de-42b6-93ac-c3b70c4cab51" # replace once per device
export VIDEO_DEVICE_NAME="Front camera"
./webrtc-video-producer -config ./config.discovery.h264.yaml
```

This configuration includes optional bitrate presets and a test-pattern pipeline.
Replace the pipeline for real capture. It uses local rstream credentials for the
tunnel and TURN; no `API_URL`, `DEVICE_SECRET` or prior device registration is
needed. The platform still issues narrowly scoped viewer/control credentials.
Direct and adaptive MediaMTX delivery use the same discovery contract.

The dashboard refreshes discovery five seconds after each completed request and
aborts pending work when unmounted. It shows a distinct unavailable state if the
engine cannot be queried. History records the last successful observation, not
an exact disconnect time; signed provisioning webhooks do not update discovery
history. Every playback/control admission resolves the current project tunnel,
so an old database row cannot authorize an offline or replacement endpoint.
Creation, provisioning and deletion actions are disabled in discovery mode.

The sample bounds discovery to 100 live labeled tunnels and 1000 remembered
UUIDs per project; exceeding a limit produces an explicit error instead of a
partial inventory. Archive obsolete history administratively if required.
Concurrent database updates preserve the newest observation and have bounded
lock/query waits.

### Select the distribution backend

Direct playback is the default and needs no MediaMTX configuration.

```bash
VIDEO_DISTRIBUTOR="direct"
```

Select MediaMTX when several viewers should share one device uplink.

```bash
VIDEO_DISTRIBUTOR="mediamtx"
MEDIAMTX_SOURCE_RESOLVER_JWKS='{"keys":[...]}'
MEDIAMTX_SOURCE_RESOLVER_ISSUER="rstream-video-distributor"
MEDIAMTX_SOURCE_RESOLVER_AUDIENCE="rstream-video-source-resolver"
MEDIAMTX_JWT_PRIVATE_KEY_BASE64="..."
MEDIAMTX_JWT_ADDITIONAL_JWKS='{"keys":[]}'
MEDIAMTX_JWT_ISSUER="rstream-webrtc-video-platform"
MEDIAMTX_JWT_AUDIENCE="rstream-mediamtx"
MEDIAMTX_TOKEN_TTL_SECONDS="300"
# Disable direct fallback to preserve one shared device uplink during outages.
MEDIAMTX_ALLOW_DIRECT_FALLBACK="false"
```

Choose exactly one MediaMTX exposure. Use a public endpoint when MediaMTX
already has an HTTPS ingress:

```bash
MEDIAMTX_EXPOSURE="public"
MEDIAMTX_PUBLIC_URL="https://media.example"
MEDIAMTX_TUNNEL_NAME=""
```

Use an rstream endpoint when the MediaMTX HTTP listener has no public ingress:

```bash
MEDIAMTX_EXPOSURE="rstream"
MEDIAMTX_PUBLIC_URL=""
MEDIAMTX_TUNNEL_NAME="webrtc-video-mediamtx"
```

`MEDIAMTX_PUBLIC_URL` may use plain HTTP only on a loopback address for local
development. It cannot contain credentials, a query, or a fragment. The public
mode still requires the path-scoped MediaMTX bearer; the rstream mode adds an
independent, short-lived edge token. UDP ICE reachability is configured on
MediaMTX in both cases and is not carried by the HTTP tunnel.

Run `npm run mediamtx:key -- mediamtx-one` once to generate the asymmetric
signing material for MediaMTX access and the named distributor identity.
The platform publishes the public key at `/api/video/distributor/jwks` and
keeps the private key server-side. The source resolver at
`/api/video/distributor/source` verifies a separate, short-lived Ed25519 request
signed by the named distributor instance. It returns producer WHEP,
distributor WHIP, and TURN material only for a known device with an online
tunnel.

MediaMTX 1.21.1 [refreshes a remote JWKS at most once per hour](https://github.com/bluenviron/mediamtx/blob/v1.21.1/internal/auth/manager.go).
Rotate the access
key in two phases so that every instance learns the next key before it signs a
token. Keep the current private key active, add the next public JWK to
`MEDIAMTX_JWT_ADDITIONAL_JWKS`, deploy, then wait at least one hour or refresh
each instance through a controlled restart. Switch to the next private key and
replace the additional set with the old public JWK. Remove the old public key
only after the longest token lifetime and another complete JWKS refresh window.
Private keys are never placed in the additional set.

The [distributor README](../distributor/) documents the combined image,
MediaMTX environment, ICE reachability, profile differences, and qualification
gates. Native MediaMTX WHEP pull is retained as an explicit reduced-feature
profile. The rstream producer accepts MediaMTX 1.21.1's narrower offer only when
that compatibility profile is enabled; strict producer profiles remain strict.
Native pull negotiates NACK/RTX and TWCC, but does not provide FlexFEC,
adaptive source encoding or the dynamic source resolver.

### Run the complete local MediaMTX stack

The local launcher builds the combined MediaMTX image, starts Next.js, and
tears everything down on `Ctrl-C`. It creates one temporary rstream tunnel so
the container can reach the local platform control plane. Its default exposes
MediaMTX directly on loopback:

```bash
npm run mediamtx:local
```

Exercise the same media pipeline with MediaMTX HTTP control published through
rstream:

```bash
npm run mediamtx:local -- --exposure rstream
```

Open `http://localhost:3000`, create a device, and run the producer command
shown by the dashboard. In the default mode the browser connects to
`http://localhost:8889`; in rstream mode it connects to the protected MediaMTX
tunnel. The producer itself still publishes its device WHEP endpoint through
rstream in both cases. The temporary platform callback lets the container
reach local JWKS and source-resolution routes; a deployed platform uses its
normal HTTPS origin instead.

### Edge authentication qualification

Set `RSTREAM_EDGE_AUTH_EXPECTED_ENGINE` to the exact engine selected by the
sample credentials, then run:

```bash
npm run test:rstream-edge-auth
```

The live check creates a temporary token-protected tunnel and exercises a
complete POST, PATCH, expiry, credential-renewal, and DELETE lifecycle. It
verifies that rstream authenticates every edge request while the producer
continues to receive its own application Bearer token. The expected-engine
guard is evaluated before the check creates any remote resource.

An operator can qualify a deployed context directly:

```bash
RSTREAM_EDGE_AUTH_CONTEXT="<context>" \
RSTREAM_EDGE_AUTH_PROJECT_ID="<project-id>" \
RSTREAM_EDGE_AUTH_EXPECTED_ENGINE="<engine-host:port>" \
npm run test:rstream-edge-auth:context
```

This canary verifies complete WHEP-like lifecycles both with and without an
application bearer, plus path scope, reserved-query sanitization, and
malformed-token rejection. It validates the context, project, and exact engine
before creating its temporary tunnel. The check above additionally qualifies
real expiry and renewal.

### rstream project setup

Use a dedicated rstream project for this sample. Create an application token scoped to that project and store its client id and secret in the Next.js environment.

The app token is used server-side only. It creates short-lived producer tokens, viewer tokens, TURN credentials, and dashboard watch tokens. Devices and browsers should never receive the application client secret. Dashboard watch tokens are minted on demand because browser watch streams send them as `rstream.token` query values to the engine streaming endpoint; they use explicit read-only watch permissions plus list-only tunnel resources filtered to the signed-in user's devices.

Create a webhook destination for the same project:

| Field            | Value                                                           |
| ---------------- | --------------------------------------------------------------- |
| Destination type | Webhook endpoint                                                |
| Endpoint URL     | `https://your-platform.example.com/api/rstream/webhook`         |
| Events           | `tunnel.created`, `tunnel.deleted`                              |
| Signing secret   | Copy the generated secret into `RSTREAM_WEBHOOK_SIGNING_SECRET` |

For local development, expose the Next.js app with any HTTPS tunnel and use the public `/api/rstream/webhook` URL as the endpoint URL. The route verifies the raw request body against `rstream-signature` before parsing the event.

You can also drive the local receiver directly from the CLI while developing:

```bash
rstream events \
  --webhook \
  --webhook-secret "$RSTREAM_WEBHOOK_SIGNING_SECRET" \
  --events tunnel.created,tunnel.deleted \
  --tunnel-filter 'labels.app=webrtc-video-platform' \
  --forward-to http://localhost:3000/api/rstream/webhook
```

Passing the same `RSTREAM_WEBHOOK_SIGNING_SECRET` to the CLI and the Next.js app
keeps local signatures deterministic. When no `--webhook-secret` is passed, the
CLI prints an ephemeral `whsec_...` value that can be used for a single receiver
session. This mirrors the webhook request body and signed headers, but it does
not create delivery history or retry after the CLI exits.

### rstream resource requirements

The sample always mints short-lived tokens with tunnel resources. Producer tokens can only create the expected tunnel for one device, direct viewer tokens can only connect to that device's `/whep` resource, distributor tokens are bound to one MediaMTX device path, and dashboard watch tokens can only list the sample tunnels for the signed-in user.

Install dependencies, apply the checked-in database migrations, and start the app:

```bash
npm ci
npm run prisma:deploy
npm run dev
```

`npm run dev` generates the Prisma client before starting Next.js. Use
`npm run prisma:migrate` when deliberately changing the database schema and
creating a new development migration.

Open `http://localhost:3000`, sign in with GitHub, create a device, and copy the generated device secret.

For a production-style local run:

```bash
npm run build
npm run start
```

## Run a Producer

From the device-side example:

```bash
cd ../producer
make build-provisioning
API_URL=http://localhost:3000 \
DEVICE_SECRET=dev_... \
./webrtc-video-producer -config ./config.provisioning.h264.yaml
```

The producer asks this application for provisioning, creates its rstream tunnel with the returned short-lived token, and serves only the API surface required by the product viewer when `web.viewer.enabled` is `false`. `make build-provisioning` builds that no-viewer binary without requiring Node.js or npm on the producer machine.

The provisioning profile uses the same adaptive 2–8 Mbit/s H.264 sender,
bounded pacer, NACK/RTX, and one-per-five FlexFEC protection qualified by the
standalone producer. It admits one source session: the selected direct browser
or the MediaMTX adapter owns that feedback loop, never both at once.

## MediaMTX distribution metrics

With `VIDEO_DISTRIBUTOR=mediamtx`, set `MEDIAMTX_METRICS_URL` to the private
MediaMTX metrics endpoint reachable from the Next.js server, for example
`http://127.0.0.1:9998/metrics` when both run on the same host. Leave it empty to
disable this feature. `npm run mediamtx:local` configures the local endpoint
automatically. Keep port 9998 on loopback or a private network; do not expose it
through the public WHEP tunnel. The administrative API remains disabled.

The normal player shows source readiness, total readers and the rates derived
from MediaMTX path inbound/outbound byte counters. Outbound is the total across
readers; these values are not encoder targets or a measurement of all network
protocol overhead. Full-page viewing keeps only its compact controls.

`GET /api/devices/<device-id>/metrics` authorizes every request before consulting
the server cache. It returns 204 when disabled, 503 when unavailable, and never
returns upstream URLs or raw metric labels. Initial rates are unknown; an
observed counter reset, readiness change, failed scrape or gap over 15 seconds
starts a new baseline. Rate samples are process-local, so a serverless cold start
or another application replica also needs two observations.

The browser polls every five seconds while the normal player is visible, with
no overlapping requests. Server scrapes coalesce per device, including failures,
for two seconds. A process retains at most 128 device observations and admits at
most eight concurrent scrapes with 64 observers each. Each scrape has a
three-second deadline and a 32 KiB response bound. Cancelling one observer does
not interrupt another; cancelling the last observer stops the upstream request.
No metrics request starts the video source or adds a producer connection.

## Optional recent recordings

Recording is disabled by default. Recent recording and playback can be tested
with the local MediaMTX stack:

```bash
npm run mediamtx:local -- --recording true
```

This explicitly enables MediaMTX recording and its private playback server,
binds port 9996 to loopback and mounts a temporary 512 MiB recording volume.
Recordings disappear when the container is removed. The producer still starts
on demand; requesting an index or a recorded clip does not start a live source.
When the recording API is available, a history button appears beside the
full-page control. It opens the most recent available recording. The timeline
marks recorded spans and gaps; selecting a gap or an expired segment shows an
explicit unavailable state. **Live** returns to the existing WebRTC session.
The source-quality controls apply to live capture and are hidden during replay.
Controls stay below the picture in both normal and full-page views.

Replay uses a separate MP4 video element and bounded clips, not a seek on the
WebRTC stream. The live connection stays established so returning to live does
not reconnect the source. Contiguous clips load in sequence; gaps stop replay
instead of silently jumping forward. Clip changes can introduce a short pause;
this is not a seamless DVR. Hiding the page cancels the replay media load and
index polling; returning restores the position paused, subject to retention.
The dashboard exposes these controls while the device is online. Live
resource/latency qualification remains pending.

For a separate deployment, configure MediaMTX as described in the
[distributor recording section](../distributor/README.md#optional-recent-recordings).
Set `MEDIAMTX_PLAYBACK_URL` to its private HTTP(S) playback base URL and
`MEDIAMTX_RECORDING_WINDOW_SECONDS` to the recent window to expose (300 seconds
by default, 30–600 supported). An empty URL disables the API. The recent window
restricts access; it does not configure disk retention or reserve storage.

`GET /api/devices/<device-id>/recordings` returns the available time spans, or
204 when disabled. `GET /api/devices/<device-id>/recordings/playback?start=<RFC3339>&duration=<seconds>`
streams a standard MP4 clip of at most 30 seconds within that window. Every
request checks device access. Discovered devices must still be present in the
authorized live inventory; a stale database observation alone does not grant
recording access. Separate spans can indicate a gap or codec/format change and
must not be assumed to concatenate. Deletion between index and playback can
return 404; an unavailable recording service returns 503.

Next.js supplies a short-lived, path-scoped `playback` JWT to MediaMTX. No JWT,
upstream URL or recording filename reaches the browser. Media responses stream
with backpressure, a 30-second request deadline and a 64 MiB bound. Disconnects
cancel upstream reads, including an unconsumed body. MediaMTX's MP4 endpoint
does not support byte ranges; a Range request receives the bounded whole clip
with status 200 and `Accept-Ranges: none`. No media body is cached by Next.js.
Each application process admits four concurrent clips and four index reads;
index requests coalesce for two seconds, with at most 128 cached devices and
32 waiters per read. Capacity must account for the number of application
processes and for replay traffic passing through Next.js.

The native MediaMTX integration check requires `mediamtx` 1.21.1, FFmpeg,
FFprobe and the Playwright Chromium/Firefox/WebKit runtimes:

```bash
npm run test:mediamtx-recording
```

This checks recording authorization, idle-source behavior, actual H.264
recording and MP4 decoding, and deletion after a short test retention period.
It does not replace the live latency/resource and replay-UI qualification.

## Optional source quality

The player's **Full page** button expands the video inside the current tab,
keeping the same WebRTC session. Controls sit below the image in both layouts;
the compact full-page toolbar retains the quality selector when presets are
available. The expand/exit controls use icons with accessible labels and tooltips.
Use **Exit full page** or Escape to restore the
dashboard and its scroll position. This layout works without the browser's
native Fullscreen API; browser tabs and address bars remain browser-controlled.

Use `../producer/config.provisioning.quality.h264.yaml` to advertise Low
(1 Mbit/s), Medium (4 Mbit/s), High (10 Mbit/s), and Auto. Presets are bitrate
ceilings; congestion control can reduce the encoder target below the selected
ceiling. Auto restores the configured adaptive range. Resolution and frame rate
stay defined by the device pipeline. RTP/RTCP, retransmission, and FEC overhead
are additional traffic beyond the encoded-video ceiling.

The player discovers modes from `GET /api/devices/:id/quality`. Its selector is
hidden for unconfigured devices and updates other viewers within five seconds.
A change affects every viewer of the device, including MediaMTX readers. The
platform checks ownership and membership, then forwards a bounded request through
a token restricted to `/api/quality`. That token stays on the server. Browser
viewer and adapter source tokens remain restricted to WHEP.

Selections include an opaque version. A competing change returns `409` and is
refreshed before another selection; it cannot silently overwrite a newer choice.
The producer reports selected mode and applied encoder target separately. The
selection survives idle/reconnect cycles in the running producer and resets to
`quality.default` after process restart. See the [producer configuration](../producer/README.md#optional-source-quality-presets)
for validation and the HTTP contract.

## Public and internal deployment profiles

Keep the public Vercel demo on `DEVICE_ACCESS_MODE=user`,
`DEVICE_INVENTORY_MODE=managed`,
`VIDEO_DISTRIBUTOR=direct`, and its existing database. For the internal deployment,
use organization mode, a separate database/OAuth application/secrets, and
`VIDEO_DISTRIBUTOR=mediamtx` with `MEDIAMTX_ALLOW_DIRECT_FALLBACK=false`.
Use the adaptive adapter configuration from `../distributor`, plus the optional
quality producer profile where needed. The two deployments have independent
inventories and credentials. Server provisioning and DNS/TLS are separate
operations; these settings do not deploy infrastructure.

## Demo Deployment

The hosted demo is intended to run at:

```text
https://webrtc-video-platform.demo.rstream.io
```

You can either run this app yourself or use that demo as the product backend. In both cases, the producer only needs the platform URL and the device secret generated by the dashboard.

```bash
make build-provisioning
API_URL=https://webrtc-video-platform.demo.rstream.io \
DEVICE_SECRET=dev_... \
./webrtc-video-producer -config ./config.provisioning.h264.yaml
```

For public demos, `vercel.json` registers a weekly cleanup job:

```json
{
  "crons": [
    {
      "path": "/api/cron/cleanup",
      "schedule": "0 3 * * 0"
    }
  ]
}
```

Set `CRON_SECRET` and `DEMO_CLEANUP_ENABLED="true"` only for disposable demo deployments. Vercel sends the cron secret as a Bearer token in the `Authorization` header when it invokes `/api/cron/cleanup`. Organization mode rejects this cleanup setting. In personal mode the endpoint deletes demo users, accounts, sessions, device records, and verification tokens. It does not touch rstream project configuration.

## Security Shape

- Device secrets are product secrets and are stored hashed.
- rstream application credentials stay on the Next.js server.
- Producer tokens are short-lived and allow only tunnel creation for one device tunnel.
- Producer TURN credentials are fetched from the product API when needed.
- Viewer tokens are short-lived and allow only the WHEP resource required by the selected backend.
- Dashboard watch tokens are short-lived and only list tunnels labelled for the current user or configured organization.
- The webhook endpoint accepts only signed rstream lifecycle events and only updates devices carrying this sample's `app` and `device` labels.
- Device creation and TURN credential issuance are bounded to keep the public sample from being used as an unmetered relay minting endpoint.
- The local producer viewer can stay enabled for operator workflows, but the product viewer token does not allow access to `/`.
- Unscoped rstream tokens are intentionally not issued by this sample.
- The demo cleanup cron is disabled by default, protected by `CRON_SECRET`, and should only be enabled for disposable demo databases.

## Validate changes

```bash
npm ci
npm run verify
npm run test:access
```

The access suite owns disposable PostgreSQL containers, applies both upgrade and
fresh migrations, and exercises the production Next.js routes. It seeds test
sessions and substitutes GitHub/engine HTTP responses; it does not certify a real
organization's OAuth approval or SSO policy. It covers cross-account access,
shared inventory, owner lock timeouts, nonmembers, watch-token scope, and the
server-side prohibition on direct fallback.

To check discovery against a real rstream project, configure the application
credentials and project endpoint in `.env.local`, build the application, then run:

```bash
docker build --file ../producer/qualification/adaptive-streaming/Dockerfile \
  --tag rstream-video-discovery:qualification ..
RSTREAM_DISCOVERY_PRODUCER_IMAGE=rstream-video-discovery:qualification \
  npm run test:discovery-live -- /path/to/empty/discovery-evidence
```

This finite check creates an isolated database and a temporary, token-protected
producer tunnel in that project, using a private CLI context and a short-lived
token limited to that tunnel name. It checks discovery without enrollment,
shared source-quality controls, stale-selection rejection, stable identity after
reconnection/rename, offline history and live-only inventory. It cleans up its
containers and private context. Only GitHub membership responses are substituted;
rstream APIs, tunnel publication and producer HTTP controls are real. This check
does not exercise video playback or an organization's actual OAuth/SSO policy.
Optionally set `RSTREAM_DISCOVERY_BROWSER` to a Chrome/Chromium executable to
include direct playback from the discovered producer, decoded 720p frame cadence
and encoder shutdown after the viewer closes. Browser runs also retain
navigation-to-first-presentation and authorization-to-first-presentation timings,
with the authorization response and WHEP/peer milestones. A one-shot video frame
callback must observe exactly the first presented frame; missing evidence or a
retried startup fails the measurement. These timings cover an authenticated
dashboard opening with an already running producer and warm membership/inventory
caches, not OAuth sign-in or device process startup. The expected display time is
a browser compositor estimate, not a physical display measurement.
To exercise discovery with the
adaptive MediaMTX adapter, also set `RSTREAM_DISCOVERY_DISTRIBUTOR=mediamtx`.
That variant uses the documented local stack helper (including its production
build and temporary HTTPS callback tunnel), so its local ports must be free.
It opens separate browser sessions for two organization members, checks one
encoder for two MediaMTX readers, synchronizes a source-quality change, denies
a nonmember, preserves playback when the first member leaves and stops the
encoder after the last leaves. Both persistent-history and live-only inventory
checks still run. GitHub membership remains a fixture. Neither variant forces
a relay path. First prepare adequate Linux UDP socket limits
as described in the [distribution prerequisites](../distributor/README.md#technical-qualification).
Abrupt browser closure can use the producer's bounded ICE recovery grace period
before it stops the encoder; the result records this elapsed time.

To additionally exercise full-page viewing in Chromium, Firefox and WebKit:

```bash
npx playwright-core install firefox webkit
RSTREAM_DISCOVERY_BROWSER=/path/to/chrome \
RSTREAM_FULL_PAGE_BROWSERS=1 \
RSTREAM_METRICS_BROWSER=1 \
RSTREAM_RECORDING_BROWSER=1 \
RSTREAM_UI_CAPTURE_DIRECTORY=/path/to/ui-evidence \
  node scripts/access-routes.integration.mjs
```

This browser check uses a local synthetic WebRTC source and the real application
UI. It checks session continuity, keyboard/focus handling, scroll restoration,
portrait/landscape layout and source disappearance. `RSTREAM_METRICS_BROWSER=1`
also checks distribution indicators and recovery after a metrics outage. It does not replace network
qualification or testing on physical mobile devices.
`RSTREAM_RECORDING_BROWSER=1` additionally requires FFmpeg and checks real MP4
decoding in Chromium/Firefox/WebKit, replay navigation, gaps, expiry, index outages,
visibility cancellation and return to the same live session. The recording
index and media service are controlled fixtures in this UI check; the native
MediaMTX recording test above covers actual recording and retention.
The disposable Firefox profile permits loopback ICE, selects the local loopback
interface and disables address obfuscation for this same-machine fixture. User
browser profiles are untouched. A failed connection is reported as a failed
qualification; successful layout checks alone do not establish media playback.
For a separate Linux Firefox test host, set `RSTREAM_FIREFOX_WS_ENDPOINT` to
an owned Playwright browser endpoint with the same Playwright version as the
platform lockfile. `RSTREAM_ACCESS_TEST_PORT` optionally fixes the application's
loopback port, so a TCP port forward can give that browser access to the test
application at the same URL. Keep the browser endpoint and forwarded port
private. This uses real WebRTC media without a browser HTTP proxy, which can
restrict Firefox's UDP candidates. The default remains local browsers and
automatically allocated application ports.

With the rstream credentials in `.env.local`, Docker, and Chrome installed, run
the complete distribution and quality paths:

```bash
npm run qualify:distribution -- /path/to/empty/evidence-directory
RSTREAM_QUALIFICATION_QUALITY=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/quality-evidence public
MEDIAMTX_ALLOW_DIRECT_FALLBACK=false RSTREAM_QUALIFICATION_QUALITY=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/required-media-evidence public
RSTREAM_QUALIFICATION_QUALITY=1 RSTREAM_QUALIFICATION_SOURCE_FORMATS=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/source-format-evidence public
RSTREAM_QUALIFICATION_QUALITY=1 RSTREAM_QUALIFICATION_RECORDING=1 \
  ./qualification/end-to-end/run.sh /path/to/empty/recording-evidence public
```

The quality run changes all four modes through the actual device tunnel while
two browser readers share one encoder, measures actual encoded throughput at
MediaMTX, rejects a stale version, and exercises
distributor failure/recovery. The required-MediaMTX variant also checks that
explicit direct authorization is refused. Use a committed, clean checkout for
publishable evidence. Each runner cleans up the resources it creates.

The source-format variant uses the optional H.264 profile example and repeats
360p15, 540p24 and 720p30 selections through MediaMTX and direct fallback. It
checks observed encoder state, actual presented dimensions, frame cadence,
transition gaps, media/RTP timestamp continuity and preservation of the video
element and MediaStream. It writes per-transport transition observations,
including partial observations on failure. Set `RSTREAM_UI_CAPTURE_DIRECTORY`
to retain desktop/mobile screenshots of normal and full-page viewing. This
manual-profile check does not qualify automatic format adaptation under network
impairment or measure capture-to-display latency or CPU savings.

The recording variant enables the local helper's bounded temporary recording
volume. It checks real replay through the platform, two readers sharing one
encoder, and live-session preservation. It deliberately fills only that owned
512 MiB tmpfs, requires an actual recorder `ENOSPC` failure, checks presented
frame cadence before/during/after the fault, then releases the filler and waits
for new recorded segments. It requires `ffprobe` to decode a newly closed segment
within 30 seconds after freeing space. MediaMTX can leave incomplete files that
make its entire playback index unavailable: the test separately requires index
recovery within eight minutes (five-minute retention, 2.5-minute cleaner interval
and polling margin), while keeping the same live frame-rate and gap gates
throughout. No recorded file is deleted to accelerate recovery. Partial
observations and both recovery times are retained in `recording.json`.
This fault test does not measure capture-to-display latency or compare recording
overhead against a recording-disabled baseline.
