# Configure the video platform

Return to the [platform overview](../README.md) for the application path. Run commands from `webrtc-video/platform`, unless a step changes directory.

## Setup

Start with a dedicated PostgreSQL database and create `.env.local` from [`.env.example`](../.env.example). Follow the [platform quick start](../README.md#run-the-application) to install, apply the migrations and launch the application. The integration suite uses PostgreSQL 17 in disposable containers.

| Variable                                     | Value for the local application                                                                                                 |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POSTGRES_PRISMA_POOL_URL`                   | Pooled connection URL for application queries. With local PostgreSQL you can use its direct URL for both fields.                |
| `POSTGRES_PRISMA_DIRECT_URL`                 | Direct connection URL for Prisma migrations.                                                                                    |
| `NEXTAUTH_URL`                               | `http://localhost:3000`. Use `http://localhost:3000/api/auth/callback/github` as the GitHub OAuth callback.                     |
| `NEXTAUTH_SECRET`                            | A random application secret. Generate one with `openssl rand -hex 32`.                                                          |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`   | Client credentials for the GitHub OAuth application.                                                                            |
| `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET` | Client ID and hex-encoded secret of the server-side rstream application credential.                                             |
| `RSTREAM_PROJECT_ENDPOINT`                   | Endpoint for the rstream project. Required for engine and TURN discovery.                                                       |
| `RSTREAM_PROJECT_ID`                         | Optional explicit project scope for minted tokens. Must match the resolved project when set. Required for discovered inventory. |
| `RSTREAM_TURN_KEYRING_BASE_URL`              | Optional public HTTPS origin that serves the TURN key when an interactive access gateway protects `RSTREAM_API_URL`.            |
| `RSTREAM_WEBHOOK_SIGNING_SECRET`             | Signing secret for managed tunnel lifecycle webhooks. Set it when configuring [webhook delivery](#rstream-project-setup).       |
| `WATCH_TOKEN_TTL_SECONDS`                    | `120` by default for a dashboard watch token.                                                                                   |
| `DEMO_CLEANUP_ENABLED`                       | `false` for development and organization use.                                                                                   |
| `CRON_SECRET`                                | Random secret for authenticated cleanup jobs if enabled.                                                                        |

The server derives application TURN credentials locally using the public key for the selected realm. It requests the key from `RSTREAM_API_URL` by default, rejects redirects and validates the bounded DER key before use.

### Select personal or organization access

Each GitHub account owns a private inventory by default. Configure `.env.local` to share the inventory across an organization.

| Variable               | Value for shared access   |
| ---------------------- | ------------------------- |
| `DEVICE_ACCESS_MODE`   | `organization`.           |
| `GITHUB_ORGANIZATION`  | GitHub organization slug. |
| `DEMO_CLEANUP_ENABLED` | `false`.                  |

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

Enable discovery for producers that already use rstream project credentials.

| Variable                           | Value for discovery                                                     |
| ---------------------------------- | ----------------------------------------------------------------------- |
| `DEVICE_INVENTORY_MODE`            | `discovered`.                                                           |
| `RSTREAM_PROJECT_ID`               | ID of the project the producers select.                                 |
| `DEVICE_DISCOVERY_HISTORY_ENABLED` | `true` to retain offline devices, or `false` to show live devices only. |

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
cd ../producer
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

Set `VIDEO_DISTRIBUTOR=direct` for browser-to-producer playback. Set `VIDEO_DISTRIBUTOR=mediamtx` to share one device uplink across multiple viewers. Configure the signing and resolver values using the [distributor control-plane guide](../../distributor/docs/001-setup.md#configure-the-control-plane).

| Variable               | Public HTTPS signaling                            | Authenticated rstream signaling |
| ---------------------- | ------------------------------------------------- | ------------------------------- |
| `MEDIAMTX_EXPOSURE`    | `public`                                          | `rstream`                       |
| `MEDIAMTX_PUBLIC_URL`  | MediaMTX origin, such as `https://media.example`. | Empty.                          |
| `MEDIAMTX_TUNNEL_NAME` | Empty.                                            | Published MediaMTX tunnel name. |

| Variable                         | Default | Effect                                                                    |
| -------------------------------- | ------- | ------------------------------------------------------------------------- |
| `MEDIAMTX_ALLOW_DIRECT_FALLBACK` | `true`  | Set `false` to preserve the one-uplink policy during distributor outages. |
| `MEDIAMTX_TOKEN_TTL_SECONDS`     | `300`   | Lifetime of the path-scoped MediaMTX viewer token.                        |

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

MediaMTX 1.21.3 [refreshes a remote JWKS at most once per hour](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/auth/manager.go).
Rotate the access
key in two phases so that every instance learns the next key before it signs a
token. Keep the current private key active, add the next public JWK to
`MEDIAMTX_JWT_ADDITIONAL_JWKS`, deploy, then wait at least one hour or refresh
each instance through a controlled restart. Switch to the next private key and
replace the additional set with the old public JWK. Remove the old public key
only after the longest token lifetime and another complete JWKS refresh window.
Private keys are never placed in the additional set.

The [distributor README](../../distributor/README.md) documents the combined image,
MediaMTX environment, ICE reachability, profile differences, and qualification
gates. Native MediaMTX WHEP pull is retained as an explicit reduced-feature
profile. The rstream producer accepts MediaMTX 1.21.3's narrower offer only when
that compatibility profile is enabled; strict producer profiles remain strict.
Native pull negotiates NACK/RTX and TWCC, but does not provide FlexFEC,
adaptive source encoding or the dynamic source resolver.

### Run the complete local MediaMTX stack

Follow the [distributor quick start](../../distributor/README.md#run-with-the-platform) for the default local setup. The launcher needs Docker, the rstream CLI, platform dependencies and migrations, and the rstream application credentials in `.env.local`. It makes its own CLI context for temporary resources. Keep TCP ports 3000, 8889, 9998 and 9999 and UDP port 8189 free. Recording also requires TCP port 9996.

| Launcher option           | Default | Effect                                                                               |
| ------------------------- | ------- | ------------------------------------------------------------------------------------ |
| `--exposure public`       | Yes     | Gives the browser `http://localhost:8889` for WHEP.                                  |
| `--exposure rstream`      |         | Publishes WHEP through a token-authenticated rstream HTTP tunnel.                    |
| `--next-mode development` | Yes     | Runs the Next.js development server.                                                 |
| `--next-mode production`  |         | Builds and starts Next.js in production mode.                                        |
| `--recording true`        | `false` | Enables temporary MediaMTX recording and the local playback endpoint.                |
| `--state-file PATH`       | Unset.  | Writes launcher state for local automation. Use an output path outside the checkout. |

To use an authenticated distributor tunnel, run `npm run mediamtx:local -- --exposure rstream` instead of the default command. The producer publishes its own WHEP endpoint through rstream in either mode. The container reaches local JWKS and source resolution through a temporary callback tunnel. `Ctrl-C` stops the application, container and temporary tunnels. A deployed platform uses its normal HTTPS origin for callbacks.

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

For a production-style run after the database migrations, use `npm run build` and `npm run start`. Use `npm run prisma:migrate` when changing the database schema and creating a development migration.

## Run a Producer

Build and launch the device using the [platform quick start](../README.md#run-the-application). The provisioning profile uses H.264 with a 2–8 Mbit/s adaptive encoder, bounded pacing, NACK/RTX and one FlexFEC repair packet per five media packets. It admits one source session, owned by either the browser or the MediaMTX adapter. The producer receives a scoped tunnel token through the platform and serves the WHEP and control endpoints used by the dashboard.
