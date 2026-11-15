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

## Prerequisites

Use Node.js 24, npm, Git and the installed rstream CLI for the tunnel mode.
Check `node --version` and `npm --version`; this is a self-hosted Node process,
not an edge/serverless runtime. Local verification uses synthetic fixtures and
needs no rstream account. Publication needs a selected project that permits the
HTTP tunnel and chosen access policy.

## Run locally

```bash
npm ci
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
rstream project use <project-endpoint>
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

## Configuration reference

| Variable                                                                 | Default / purpose                                                                                                                                  |
| ------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `RSTREAM_CONFIG`, `RSTREAM_CONTEXT`                                      | Runtime SDK's shared CLI identity/context. Services can instead inject their own Engine/token or certificate settings; see the Node runtime guide. |
| `RSTREAM_TUNNEL_NAME`, `RSTREAM_TUNNEL_HOSTNAME`                         | `nextjs-preview`; optional requested stable public hostname. A tunnel name alone does not reserve a public URL.                                    |
| `RSTREAM_TUNNEL_LABELS`                                                  | Add comma-separated `key=value` discovery metadata to the sample's application/framework labels.                                                   |
| `RSTREAM_TUNNEL_TOKEN_AUTH`, `RSTREAM_TUNNEL_RSTREAM_AUTH`               | False; `1`, `true` or `yes` requests the matching edge policy. Agent authentication is separate.                                                   |
| `RSTREAM_TUNNEL_RECONNECT_INITIAL_MS`, `RSTREAM_TUNNEL_RECONNECT_MAX_MS` | 1000 / 30000 ms; bounded reconnect backoff owned by this application.                                                                              |
| `PORT`                                                                   | 3000; Next's application port configuration, while the tunnel server accepts streams directly.                                                     |
| `NEXT_ALLOWED_DEV_ORIGINS`                                               | Explicit allowed development hosts for HMR through the tunnel.                                                                                     |
| `GITHUB_WEBHOOK_SECRET`                                                  | Verify unchanged webhook request bytes; independent from every rstream credential.                                                                 |

Open the printed URL to see the preview page. A GitHub webhook delivery to its
`/api/webhooks/github` route must use the matching signing secret. An interrupt
stops the supervisor and its current tunnel; production mode requires a prior
Next build. API administration and browser-token issuance remain different SDK
roles from serving this process.
