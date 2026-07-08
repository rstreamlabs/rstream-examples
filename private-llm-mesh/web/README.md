# private-llm-mesh web app

Next.js chat UI and server-side agent runtime for `private-llm-mesh`.

The app authenticates with rstream application credentials, watches the worker
pool from the browser, mints scoped server-side worker tokens for chat turns, and
serves the UI that lets users add workers and chat with their private models.

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
npm install
npm run dev
npm run test
npm run verify
```

`npm run dev` starts the local app on `http://localhost:3000`. `npm run test`
runs lint and typecheck. `npm run verify` runs test, build, and dependency audit.
