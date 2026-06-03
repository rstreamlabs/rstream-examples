# WebRTC Video Platform

This example shows how a third-party Next.js application can integrate `rstream` without asking devices or browser users to install the `rstream` CLI or handle long-lived rstream tokens.

The application owns the device inventory, authentication, device secrets, producer provisioning, viewer authorization, and demo data lifecycle. `rstream` remains the tunnel, TURN, token, and real-time tunnel state layer behind that product API.

This is the platform counterpart to `../webrtc-video-streaming`. The producer code is the same reference WebRTC streamer, but it runs in provisioning mode and receives all rstream material from this product backend.

If you want a guided walkthrough of the architecture and the JavaScript SDK integration, see the associated guide: [Build a Next.js WebRTC Video Platform with rstream](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream).

## Architecture

The producer receives only two product-level values:

```bash
API_URL=http://localhost:3000
DEVICE_SECRET=dev_...
```

It calls `POST /api/devices/tunnel` with that secret. The Next.js API validates the device, creates a short-lived rstream token that can only create the expected HTTP tunnel, and returns the tunnel configuration.

Whenever the producer needs TURN credentials, it calls `POST /api/devices/turn` with the same device secret. TURN issuance is intentionally separate from tunnel provisioning so the producer can refresh credentials on demand.

Browser viewers never receive the producer secret. When a signed viewer URL is needed, the frontend calls `POST /api/devices/:id/viewer`. The API creates TURN credentials itself, then creates a short-lived token that can only connect to the selected tunnel on `/ws`.

The dashboard uses `@rstreamlabs/react` to watch tunnel state in real time. The device list is still stored in PostgreSQL, but online/offline state is read from rstream tunnel state.

The app also exposes `POST /api/rstream/webhook`. rstream signs lifecycle events for this endpoint, the app verifies them with the JavaScript SDK, and tunnel lifecycle events update the device presence timestamps from the labels attached to the short-lived producer token. `tunnel.created` records when the device came online, and `tunnel.deleted` records when it was last seen before going offline.

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
RSTREAM_WEBHOOK_SIGNING_SECRET="whsec_..."
```

The sample resolves the engine from `RSTREAM_PROJECT_ENDPOINT`. `RSTREAM_PROJECT_ID` is optional when an endpoint is configured; when present, it is used by the SDK as the default project scope for short-lived tunnel tokens.

### rstream Project Setup

Use a dedicated rstream project for this sample. Create an application token scoped to that project and store its client id and secret in the Next.js environment.

The app token is used server-side only. It creates short-lived producer tokens, viewer tokens, TURN credentials, and dashboard watch tokens. Devices and browsers should never receive the application client secret. Dashboard watch tokens are minted on demand because browser watch streams send them as `rstream.token` query values to the engine streaming endpoint.

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

### rstream Resource Requirements

The sample always mints short-lived tokens with tunnel resources. Producer tokens can only create the expected tunnel for one device, viewer tokens can only connect to the selected online tunnel on `/ws`, and dashboard watch tokens can only list the sample tunnels for the signed-in user.

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
cd ../webrtc-video-streaming
make build-provisioning
API_URL=http://localhost:3000 \
DEVICE_SECRET=dev_... \
./webrtc-video-streaming -config ./config.provisioning.h264.yaml
```

The producer asks this application for provisioning, creates its rstream tunnel with the returned short-lived token, and serves only the API surface required by the product viewer when `web.viewer.enabled` is `false`. `make build-provisioning` builds that no-viewer binary without requiring Node.js or npm on the producer machine.

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
./webrtc-video-streaming -config ./config.provisioning.h264.yaml
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
- Viewer tokens are short-lived and allow only tunnel connection to `/ws`.
- Dashboard watch tokens are short-lived and only list tunnels labelled for the signed-in user.
- The webhook endpoint accepts only signed rstream lifecycle events and only updates devices carrying this sample's `app` and `device` labels.
- Device creation and TURN credential issuance are bounded to keep the public sample from being used as an unmetered relay minting endpoint.
- The local producer viewer can stay enabled for operator workflows, but the product viewer token does not allow access to `/`.
- Unscoped rstream tokens are intentionally not issued by this sample.
- The demo cleanup cron is disabled by default, protected by `CRON_SECRET`, and should only be enabled for disposable demo databases.
