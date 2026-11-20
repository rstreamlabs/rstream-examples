# Private LLM mesh web app

This directory owns the Next.js chat UI and server-side agent runtime. Follow the [example quickstart](../README.md#1-start-the-chat-app) to pair it with a worker. Run the commands here from `private-llm-mesh/web` using Node.js 24 and npm 11. The checked-in lockfile uses npm 11.19.1 and includes dependency overrides.

## Configuration

Copy `.env.example` to `.env.local`. The browser receives a short-lived read-only watch token. The application credentials and the worker connect tokens stay on the server.

| Variable                                                           | Purpose                                                                                                                                                                                                            |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET`                       | Hosted Application key (APP). Grant the project resolution, inventory, and stream permissions listed in the [quickstart](../README.md#1-start-the-chat-app). The SDK signs short-lived application tokens locally. |
| `RSTREAM_PROJECT_ENDPOINT` or `RSTREAM_PROJECT_ID`                 | Set at least one to scope the app to the hosted project. The Dashboard's eight-character endpoint is the usual choice for local setup.                                                                             |
| `RSTREAM_ENGINE`, `RSTREAM_API_URL`                                | Optional Engine and Control plane overrides for the same project. The API URL also determines the rstream MCP endpoint.                                                                                            |
| `CONNECT_TOKEN_TTL_SECONDS`                                        | Worker connect and probe grant lifetime, 300 seconds by default. Allow for all model calls in a multi-step turn.                                                                                                   |
| `WATCH_TOKEN_TTL_SECONDS`                                          | Read-only browser watch grant lifetime, 120 seconds by default.                                                                                                                                                    |
| `NEXTAUTH_SECRET`, `NEXTAUTH_URL`                                  | Session signing secret and canonical application URL. Generate a secret with `openssl rand -hex 32`.                                                                                                               |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`                         | GitHub OAuth app credentials for sign-in.                                                                                                                                                                          |
| `ALLOWED_EMAILS`, `ALLOWED_EMAIL_DOMAINS`, `ALLOWED_GITHUB_LOGINS` | Comma-separated GitHub allowlists. Any match permits sign-in. An empty allowlist disables the GitHub provider.                                                                                                     |
| `AUTH_DISABLED`                                                    | Defaults to false. Set to `true` only for a loopback-only quickstart or a controlled local test. Worker tunnel authentication remains in force.                                                                    |
| `TAVILY_API_KEY`                                                   | Optional key for reliable web search and fetch. Without a key, these tools use rate-limited keyless access.                                                                                                        |

## GitHub sign-in and deployment

For any deployment that serves other users, remove `AUTH_DISABLED` and create a GitHub OAuth app. Set its callback URL to `<NEXTAUTH_URL>/api/auth/callback/github`, add its client ID and secret, and configure at least one allowlist above. Use an HTTPS application URL for a deployed app. The sign-in allowlist controls access to the shared worker pool and the server-side tools.

Deploy the Next.js app with `private-llm-mesh/web` as the project root. On Vercel, use Node.js 24, the lockfile-based install command `npx --yes npm@11.19.1 ci`, and the same server environment values. The worker may run on a different network or machine as long as it registers in the same project. The browser keeps chat history while the page is open.

## Worker discovery and routing

The app lists online tunnels labeled `role=llm` and `app=private-llm-mesh`. The pool panel uses `@rstreamlabs/react/hooks` for real-time presence. The server reads model IDs and round-trip time from `GET /v1/models` and, when available, capacity and load from `GET /healthz`. These discovery probes run concurrently. Both worker variants publish token-gated HTTPS tunnels, expose the model catalog at `GET /v1/models`, and accept OpenAI-compatible `POST /v1/chat/completions`.

Automatic routing considers workers that serve the requested model. It prefers the lowest normalized load (active and waiting work divided by parallel capacity), then lower probe round-trip time, with a random choice when both are equal. A worker that omits the load endpoint uses a neutral score. Process-local reservations count work immediately, before the two-second discovery cache refreshes. Each worker owns its admission limits, so the app needs no shared routing database.

The app checks `/v1/models` again before dispatch. Automatic routing can try another eligible worker if this check fails. A pinned turn fails if its worker cannot serve it. After dispatch the app avoids replaying the turn because a tool may already have run.

For each turn, the server mints a short-lived token scoped to the selected tunnel and the exact `/v1/chat/completions` path. A separate probe grant covers `/v1/models` and `/healthz` on the labeled pool. The browser receives only the token for reading worker presence.

The agent executes tools on the server. Web search and fetch send their query or target URL to Tavily. When the hosted rstream MCP is available, the app discovers its WebTTY tools and requires approval for tools marked destructive. Tool selection depends on the model's chat template and tool-calling support.

## Verification

```bash
npx --yes npm@11.19.1 ci
npm run verify
```

`npm run verify` runs lint, type checking, unit tests, the production build, and the dependency audit. The build and fixture tests can run without an rstream account. To verify a live turn, start a worker and follow the [browser check](../README.md#3-verify-a-turn). The [qualification guide](../qualification/README.md) covers multiple workers and boundaries for controlled failure tests.
