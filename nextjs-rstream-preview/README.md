# Preview a Next.js application and receive a webhook

Run the App Router app on loopback, then open a tunnel from its Node
process. The webhook inbox verifies signed requests and shows deliveries
on the home page.

## Start on loopback

Use Node.js 24 and npm. From this directory, install the dependencies and
start Next.js.

```sh
npm ci
npm run dev
```

Open `http://127.0.0.1:3000`. Expect the preview and webhook inbox.
Stop the development server with Ctrl+C. To verify signed delivery without
starting the app manually, run `npm run smoke:local`. It starts a loopback
fixture, checks the inbox and stops the fixture.

## Serve the application through rstream

Use a self-hosted Node process and a project that permits published HTTP.
Install the [rstream CLI](https://rstream.io/docs/getting-started/installation),
then select the endpoint shown by the project list. Use disposable content
in this preview, since its inbox displays delivery details. Set project
access controls before exposing sensitive data.

```sh
rstream login
rstream project list
rstream project use my-project-endpoint
export GITHUB_WEBHOOK_SECRET="$(openssl rand -hex 24)"
npm run dev:tunnel
```

Open the `Public URL` printed by the server. Configure a GitHub webhook at
`/api/webhooks/github` on that origin. Choose `application/json` and enter
the same signing secret. Expect a signed delivery to appear in the inbox.
For a GitHub delivery, use a disposable preview whose edge policy allows the
callback. If the project requires an additional caller credential, use the
local smoke test or a relay that can present that credential.

The SDK passes requests and upgrades to Next.js and supervises the tunnel
using bounded retry. Ctrl+C stops the running server. Remove the temporary GitHub webhook after
testing. For an application
that keeps its existing HTTP listener, use [CLI forwarding](https://rstream.io/guides/expose-a-local-nextjs-app-with-rstream#add-a-cli-tunnel-sidecar-to-an-existing-app).

## Choose stable access

Use `RSTREAM_TUNNEL_HOSTNAME` for a hostname already assigned to the
project. Include that hostname in `NEXT_ALLOWED_DEV_ORIGINS` when
browsers need Next.js development assets through it. For a self-hosted
production process, run `npm run build` and then `npm run start:tunnel`
with the same configuration.

| Variable                                                                 | Purpose                                                                            |
| ------------------------------------------------------------------------ | ---------------------------------------------------------------------------------- |
| `RSTREAM_CONFIG`, `RSTREAM_CONTEXT`                                      | Select the runtime's CLI-compatible configuration.                                 |
| `RSTREAM_TUNNEL_NAME`                                                    | Tunnel name. Defaults to `nextjs-preview`.                                         |
| `RSTREAM_TUNNEL_HOSTNAME`                                                | Request a stable hostname configured for the project.                              |
| `RSTREAM_TUNNEL_LABELS`                                                  | Comma-separated `key=value` labels.                                                |
| `RSTREAM_TUNNEL_TOKEN_AUTH`, `RSTREAM_TUNNEL_RSTREAM_AUTH`               | Request the corresponding caller authentication policy using `1`, `true` or `yes`. |
| `RSTREAM_TUNNEL_RECONNECT_INITIAL_MS`, `RSTREAM_TUNNEL_RECONNECT_MAX_MS` | Initial and maximum retry delay. Defaults to 1000 and 30000 ms.                    |
| `PORT`                                                                   | Next.js server port setting. Defaults to 3000.                                     |
| `NEXT_ALLOWED_DEV_ORIGINS`                                               | Comma-separated hostnames allowed to request Next.js development assets.           |
| `GITHUB_WEBHOOK_SECRET`                                                  | Shared signing secret used by the webhook receiver.                                |
| `WEBHOOK_DATA_DIR`                                                       | Directory where the sample stores webhook deliveries. Defaults to `.data`.         |

`npm run verify` runs the type check, local webhook smoke test, production
build and dependency audit.
