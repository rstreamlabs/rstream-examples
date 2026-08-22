# WebRTC Video Platform

This example shows how a third-party Next.js application can integrate `rstream` without asking devices or browser users to install the `rstream` CLI or handle long-lived rstream tokens.

The application owns the device inventory, authentication, device secrets, producer provisioning, viewer authorization, and demo data lifecycle. `rstream` remains the tunnel, TURN, token, and real-time tunnel state layer behind that product API.

This control plane builds the Go code from `../producer` in provisioning mode,
with the embedded viewer omitted from the deployment binary. Capture, encoding,
adaptation, repair, recovery, and metrics remain in that producer; this
application supplies short-lived rstream material and owns the viewer
entrypoint.

The [Next.js platform guide](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream)
walks through this control plane. It follows the [adaptive producer](https://rstream.io/guides/build-device-to-browser-webrtc-streaming-with-rstream);
the third guide, currently in preparation, adds the optional MediaMTX
distribution backend.

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

The producer receives only two product-level values:

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

The dashboard uses `@rstreamlabs/react` to watch tunnel state in real time. The device list is still stored in PostgreSQL, but online/offline state is read from rstream tunnel state.

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

Create the environment file:

```bash
cp .env.example .env
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

MediaMTX 1.20 [refreshes a remote JWKS at most once per hour](https://github.com/bluenviron/mediamtx/blob/v1.20.0/internal/auth/manager.go).
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
profile. The rstream producer accepts MediaMTX 1.20's narrower offer only when
that compatibility profile is enabled; strict producer profiles remain strict.
Native pull negotiates NACK and TWCC, but not the adapter's source-side
RTX/FlexFEC repair or dynamic source resolver.

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

Install dependencies, create the database, and start the app:

```bash
npm install
npm run prisma:migrate
npm run dev
```

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

Set `CRON_SECRET` and `DEMO_CLEANUP_ENABLED="true"` only for disposable demo deployments. Vercel sends the cron secret as a Bearer token in the `Authorization` header when it invokes `/api/cron/cleanup`. The endpoint deletes demo users, accounts, sessions, device records, and verification tokens. It does not touch rstream project configuration.

## Security Shape

- Device secrets are product secrets and are stored hashed.
- rstream application credentials stay on the Next.js server.
- Producer tokens are short-lived and allow only tunnel creation for one device tunnel.
- Producer TURN credentials are fetched from the product API when needed.
- Viewer tokens are short-lived and allow only the WHEP resource required by the selected backend.
- Dashboard watch tokens are short-lived and only list tunnels labelled for the signed-in user.
- The webhook endpoint accepts only signed rstream lifecycle events and only updates devices carrying this sample's `app` and `device` labels.
- Device creation and TURN credential issuance are bounded to keep the public sample from being used as an unmetered relay minting endpoint.
- The local producer viewer can stay enabled for operator workflows, but the product viewer token does not allow access to `/`.
- Unscoped rstream tokens are intentionally not issued by this sample.
- The demo cleanup cron is disabled by default, protected by `CRON_SECRET`, and should only be enabled for disposable demo databases.
