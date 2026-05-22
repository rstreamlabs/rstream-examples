# Next.js rstream preview

This example is a Next.js App Router application with a signed webhook endpoint
and an advanced rstream SDK tunnel mode.

Use it as the SDK reference for serving a self-hosted Next.js process directly
through rstream. `npm run dev` remains the normal `next dev` command.
`npm run dev:tunnel` starts a small custom Node HTTP server, lets Next.js handle
requests and upgrades, and serves that server directly through rstream with
`@rstreamlabs/runtime`.

## What it demonstrates

- a standard Next.js App Router application
- a signed GitHub webhook endpoint that can be pointed at the tunnel URL
- `@rstreamlabs/runtime` creating a published HTTP tunnel from a Next.js server
- direct `tunnel.serve(server)` forwarding without a localhost proxy hop
- reconnect handling for transient tunnel disconnects
- stable-domain, token-auth, rstream-auth, and labels through environment
  variables
- an additive setup that keeps the normal Next.js commands intact

## Run locally

```bash
npm install
npm run verify
npm run dev
```

`npm run dev` runs `next dev`. The product guide also shows the CLI
`rstream forward` plus `concurrently` setup for existing applications that
should keep their current Next.js server process.

## Run through rstream

Select a local rstream context first:

```bash
rstream login
rstream project use <project-endpoint> --default
```

Then start Next.js with a tunnel:

```bash
GITHUB_WEBHOOK_SECRET="$(openssl rand -hex 24)" \
RSTREAM_TUNNEL_NAME=nextjs-preview \
RSTREAM_TUNNEL_LABELS=service=nextjs,env=dev \
npm run dev:tunnel
```

Use the printed rstream URL as the webhook target:

```text
https://<published-rstream-host>/api/webhooks/github
```

`npm run dev:tunnel` uses the SDK-based server in
`src/server/rstream-next-server.ts`. The process prepares Next.js, creates the
tunnel, passes normal HTTP requests and upgrade requests to Next.js, and
reconnects with exponential backoff if the tunnel session ends unexpectedly.

## Stable domains and edge auth

Use a stable domain when a webhook provider, OAuth app, mobile app, or teammate
should keep the same URL across restarts:

```bash
NEXT_ALLOWED_DEV_ORIGINS=<your-stable-domain> \
RSTREAM_TUNNEL_HOSTNAME=<your-stable-domain> \
npm run dev:tunnel
```

`NEXT_ALLOWED_DEV_ORIGINS` is only needed for browser previews in development.
It lets Next.js serve dev-only resources such as HMR through the stable tunnel
host. Webhook and OAuth callback tests do not depend on it.

For machine-facing endpoints, enable token authentication:

```bash
RSTREAM_TUNNEL_TOKEN_AUTH=1 npm run dev:tunnel
```

For browser-facing internal previews, enable rstream Auth when your project
supports it:

```bash
RSTREAM_TUNNEL_RSTREAM_AUTH=1 npm run dev:tunnel
```

Agent authentication and public tunnel authentication are separate. The rstream
CLI authenticates to the engine using the selected local context or environment
variables. `RSTREAM_TUNNEL_TOKEN_AUTH` and `RSTREAM_TUNNEL_RSTREAM_AUTH` protect
users reaching the published URL.

## Deployment notes

`npm run start:tunnel` runs the same SDK-based server with
`NODE_ENV=production`. Run `npm run build` first for a self-hosted Node process.

If a deployed Next.js product needs to list tunnels, issue tokens, or supervise
remote services through rstream APIs, use `@rstreamlabs/rstream` for hosted
Control plane calls and `@rstreamlabs/tunnels` for Engine API calls from that
product backend. This sample focuses on serving the Next.js app itself through a
data-plane tunnel.
