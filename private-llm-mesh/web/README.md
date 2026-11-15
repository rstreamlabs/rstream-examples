# private-llm-mesh web app

Next.js chat UI and server-side agent runtime for `private-llm-mesh`.

The app authenticates with rstream application credentials, watches the worker
pool from the browser, mints scoped server-side worker tokens for chat turns, and
serves the UI that lets users add workers and chat with their private models.

## Prerequisites

Use Node.js 24 and npm 11 (validated with 11.19.1) with the checked-in lockfile.
The lockfile uses dependency overrides that npm 10 does not resolve consistently. This is a server-side Next.js
application plus a browser UI; only the backend receives application credentials.
Workers have separate native prerequisites in [their guide](../worker/README.md).

## Setup

Create `web/.env.local`:

```bash
RSTREAM_CLIENT_ID=...
RSTREAM_CLIENT_SECRET=...
RSTREAM_PROJECT_ENDPOINT=...
NEXTAUTH_SECRET=...
NEXTAUTH_URL=http://localhost:3000
AUTH_DISABLED=true
```

For GitHub SSO, set `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, and at least one
allowlist variable: `ALLOWED_EMAILS`, `ALLOWED_EMAIL_DOMAINS`, or
`ALLOWED_GITHUB_LOGINS`.

## Commands

```bash
npx --yes npm@11.19.1 ci
npm run dev
npm run test
npm run verify
```

`npm run dev` starts the local app on `http://localhost:3000`. `npm run test`
runs lint, type checking and unit tests. `npm run verify` runs test, build, and dependency audit.
These checks use local fixtures and need no configured rstream account; the live
worker pool needs the project and application identity described above.

## Identity and configuration

| Variables                                                       | Role                                                                                          |
| --------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET`                    | Required backend application identity; never send it to workers or the browser.               |
| `RSTREAM_PROJECT_ENDPOINT` / `RSTREAM_PROJECT_ID`               | Identify the authorized project; configure at least one. Endpoint is the usual managed setup. |
| `RSTREAM_ENGINE`, `RSTREAM_API_URL`                             | Optional explicit Engine or Control plane override for that same project.                     |
| `CONNECT_TOKEN_TTL_SECONDS`, `WATCH_TOKEN_TTL_SECONDS`          | 300 / 120 seconds; separate worker-call and read-only browser-watch grants.                   |
| `NEXTAUTH_SECRET`, `NEXTAUTH_URL`                               | Product session signing and application origin.                                               |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, allowlist variables | Product user sign-in. Without an allowlist GitHub sign-in fails closed.                       |
| `AUTH_DISABLED`                                                 | False; explicit local quickstart only. It does not remove worker edge authentication.         |
| `TAVILY_API_KEY`                                                | Optional product tool credential; independent of rstream and model inference.                 |

After starting a worker in the same project, the pool panel shows its model.
Choose it and submit a message to observe the streamed response and worker
attribution. No worker appears until a real worker has registered; installing
this UI alone does not create model capacity. Stop web and worker processes
separately. The [root application guide](../README.md) covers routing, failure
boundaries and the two worker shapes.
