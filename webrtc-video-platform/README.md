# WebRTC Video Platform

This example shows how a third-party Next.js application can integrate `rstream` without asking devices or browser users to install the `rstream` CLI or handle long-lived rstream tokens.

The application owns the device inventory, authentication, device secrets, producer provisioning, viewer authorization, and demo data lifecycle. `rstream` remains the tunnel, TURN, token, and real-time tunnel state layer behind that product API.

This is the platform counterpart to `../webrtc-video-streaming`. The producer code is the same reference WebRTC streamer, but it runs in provisioning mode and receives all rstream material from this product backend.

If you want a guided walkthrough of the architecture and the `@rstreamlabs/tunnels` integration, see the associated guide: [Build a Next.js WebRTC Video Platform with rstream](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream).

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

## Stack

- Next.js App Router
- NextAuth with GitHub OAuth only
- Prisma with PostgreSQL for the reference setup
- `@rstreamlabs/tunnels` for the configured rstream client, tunnel inventory, TURN credentials, and fine-grained auth tokens
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
```

Use the pooled PostgreSQL URL for `POSTGRES_PRISMA_POOL_URL`. Use the direct, non-pooled PostgreSQL URL for `POSTGRES_PRISMA_DIRECT_URL`; Prisma uses it for migrations.

Fill the rstream application credentials and target tunnels project:

```bash
RSTREAM_CLIENT_ID="rstream-app-client-id"
RSTREAM_CLIENT_SECRET="hex-encoded-rstream-app-client-secret"
RSTREAM_PROJECT_ENDPOINT="rstream-project-endpoint"
RSTREAM_FINE_GRAINED_GRANTS="true"
```

The sample resolves the project from its endpoint. `RSTREAM_API_URL` and `RSTREAM_ENGINE` are intentionally left out of `.env.example`; they are only useful for custom rstream deployments.

### rstream Project Setup

Use a dedicated rstream project for this sample. Create an application token scoped to that project and store its client id and secret in the Next.js environment.

The app token is used server-side only. It creates short-lived producer tokens, viewer tokens, TURN credentials, and dashboard watch tokens. Devices and browsers should never receive the application client secret.

TODO: Add screenshots for the project creation flow.

TODO: Add screenshots for the app token creation flow and project scope selection.

### rstream Plan Compatibility

The reference design uses fine-grained tunnel grants:

```bash
RSTREAM_FINE_GRAINED_GRANTS="true"
```

That is the production path. Producer tokens can only create the expected tunnel for one device, and viewer tokens can only connect to the selected online tunnel on `/ws`.

For basic or free accounts that do not include fine-grained tunnel grants, set:

```bash
RSTREAM_FINE_GRAINED_GRANTS="false"
```

The app still issues short-lived producer, viewer, and watch tokens, but those tokens are not restricted by rstream grant filters. Device and user checks still happen in this Next.js app, and tokens still expire quickly, but rstream no longer enforces user-level, device-level, tunnel-level, or path-level segregation at the edge. Treat that mode as a compatibility/demo mode, not as the recommended multi-tenant production design.

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
API_URL=http://localhost:3000 \
DEVICE_SECRET=dev_... \
./webrtc-video-streaming -config ./config.provisioning.h264.yaml
```

The producer asks this application for provisioning, creates its rstream tunnel with the returned short-lived token, and serves only the API surface required by the product viewer when `web.viewer.enabled` is `false`.

## Demo Deployment

The hosted demo is intended to run at:

```text
https://webrtc-video-streaming.demo.rstream.io
```

You can either run this app yourself or use that demo as the product backend. In both cases, the producer only needs the platform URL and the device secret generated by the dashboard.

```bash
API_URL=https://webrtc-video-streaming.demo.rstream.io \
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

Set `CRON_SECRET` in Vercel. Vercel sends it as a Bearer token in the `Authorization` header when it invokes `/api/cron/cleanup`. The endpoint deletes demo users, accounts, sessions, device records, and verification tokens. It does not touch rstream project configuration.

## Security Shape

- Device secrets are product secrets and are stored hashed.
- rstream application credentials stay on the Next.js server.
- Producer tokens are short-lived and grant only tunnel creation for one device tunnel.
- Producer TURN credentials are fetched from the product API when needed.
- Viewer tokens are short-lived and grant only tunnel connection to `/ws`.
- Dashboard watch tokens are short-lived and, with fine-grained grants enabled, only list tunnels labelled for the signed-in user.
- The local producer viewer can stay enabled for operator workflows, but the product viewer token does not grant access to `/`.
- When `RSTREAM_FINE_GRAINED_GRANTS=false`, producer, viewer, and watch tokens are still short-lived, but rstream does not receive user, device, tunnel, or path filters. Keep that mode for local evaluation.
- The demo cleanup cron is protected by `CRON_SECRET` and should only be enabled for disposable demo databases.
