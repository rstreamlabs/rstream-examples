# Homelab Grafana and Prometheus with rstream

This example runs a small Grafana and Prometheus monitoring stack and publishes
Grafana through an outbound rstream tunnel. Prometheus stays off the public
tunnel and remains reachable from the Docker network and the local host.
Grafana is the browser-facing entrypoint.

The sample is intentionally close to a real homelab setup:

- Grafana is provisioned with a Prometheus datasource and a starter dashboard.
- Prometheus scrapes itself and Grafana metrics.
- Grafana binds to `127.0.0.1` for local access.
- rstream discovers the Grafana tunnel from Docker labels.
- Labels identify the service as `env=homelab`, `stack=monitoring`, and
  `service=grafana`.

## Prepare a private local configuration

Use Docker with Compose, Node.js 24 and the Compose v2 CLI. On a Linux host using the local Docker socket, run from this directory:

```sh
node scripts/configure.mjs
make check
```

On a Linux host using the local Docker socket, the setup writes an ignored `.env` with mode 0600, a random Grafana password,
non-root watcher UID/GID and the Docker socket's local group ID. Rerunning it
preserves the existing file and password. If the socket is provided elsewhere,
or when using Docker Desktop, pass its container-visible group ID explicitly:
`node scripts/configure.mjs --docker-gid <gid>`. On Linux, check it with
`stat -c %g /var/run/docker.sock`. Docker Desktop uses a Linux VM: verify the socket GID in that daemon namespace (commonly 0) and pass it explicitly; the macOS host socket group is not authoritative. Choose this explicit command instead of the no-argument setup above on Docker Desktop.
Keep `.env` private and inspect it locally to copy the Grafana login password.
The configuration refuses to start without a password; there is no shared
predictable fallback. The variable initializes a new Grafana database, not a
password rotation of an existing volume. Rotate an existing account through
Grafana's administrative workflow.

`make check` validates configuration and private-file behavior without contacting
Docker's daemon. The optional `make verify` starts an isolated smoke Compose
project; use it only on a Docker host intended for testing.

## Run the stack

```sh
make start
```

Open Grafana at `http://127.0.0.1:13000` and sign in as `admin` with the password
retained in `.env`. Prometheus is local at `http://127.0.0.1:19090`.
Compose reads the same `.env` on later commands, so restarting or publishing the
stack does not silently generate another password.

## Publish Grafana from Docker labels

The Compose file carries rstream tunnel labels on the `grafana` service. Run the
rstream reconciler as a Compose service:

```bash
# Edit these values in the private .env, keeping mode 0600:
# RSTREAM_ENGINE=<engine-host>:443
# RSTREAM_AUTHENTICATION_TOKEN=<project-scoped-agent-token>
make run
```

`make run` is an alias for the full Docker profile with the rstream reconciler;
`make start` runs only Grafana and Prometheus for local checks.

This keeps tunnel configuration next to the container that owns the browser
surface. It scales cleanly when a homelab host runs several services and a
single rstream agent should reconcile them from Docker labels.

The watcher runs as `RSTREAM_UID:RSTREAM_GID` (1000:1000 by default), with
`DOCKER_GID` added for socket access. Override those numeric IDs in `.env` when
the host requires a different dedicated service account. The container uses an
empty explicit config path under `/tmp` and does not depend on a home directory
for that numeric UID; its Engine identity comes from the configured environment. The socket bind is
mounted `:ro`, but that does **not** make the Docker API read-only: membership in
its group grants powerful host control. Use a deliberately restricted socket
proxy or host policy when the watcher must have less authority. This Compose
example does not implement that proxy or claim host isolation.

For local testing against an engine running on the Docker host, also set
`RSTREAM_ENGINE_HOSTNAME` to the hostname inside `RSTREAM_ENGINE`. The Compose
file maps that hostname to the Docker host through `host-gateway` so TLS keeps
the expected server name while the container reaches the local engine.

For a stable browser URL, set the tunnel hostname label and Grafana root URL
before starting the stack:

```bash
# Retain the existing password; add these values to .env:
# RSTREAM_GRAFANA_HOSTNAME=<your-stable-domain>
# GRAFANA_ROOT_URL=https://<your-stable-domain>/
make run
```

Recreate the Grafana container after changing `GRAFANA_ROOT_URL`. Keep all terminals on the same local `.env`; no foreground tunnel process or second terminal needs a separately generated secret.

## Access policy

Grafana keeps its own login enabled and anonymous access disabled. For team
browser access, enable rstream Auth at the edge when the selected rstream
project supports it:

Add `RSTREAM_HTTP_AUTH_RSTREAM=true` to the private `.env`, then run
`make run`. The existing Engine identity and Grafana password remain in that
same local configuration.

Token authentication is a better fit for API endpoints and automation than for
a browser application like Grafana, because browsers will not attach a bearer
token to every Grafana asset and API request automatically.

## Clean up

```bash
make clean
```

## Configuration reference

| Variable | Default / role |
| --- | --- |
| `GRAFANA_ADMIN_PASSWORD`, `GRAFANA_ADMIN_USER` | Required retained bootstrap password; `admin` user. Changing the file does not rotate an existing database account. |
| `RSTREAM_ENGINE`, `RSTREAM_AUTHENTICATION_TOKEN` | Project-scoped agent authority and identity for the container, separate from Grafana login. |
| `RSTREAM_UID`, `RSTREAM_GID`, `DOCKER_GID` | Non-root process identity and separately granted socket group; not a read-only Docker permission model. |
| `RSTREAM_GRAFANA_HOSTNAME`, `GRAFANA_ROOT_URL` | Optional requested stable tunnel host and matching external Grafana origin. |
| `RSTREAM_HTTP_AUTH_RSTREAM` | False; request project-supported browser authentication at the edge. |
| `GRAFANA_PORT`, `PROMETHEUS_PORT` | Local loopback ports 13000 / 19090. The wait helper loads the same `.env`. |
| `RSTREAM_SITE_LABEL` | `home`; discovery metadata for this stack. |
| `RSTREAM_IMAGE`, `GRAFANA_IMAGE`, `PROMETHEUS_IMAGE` | Image overrides. Preserve and qualify reviewed versions for a managed deployment. |

`make clean` removes the sample's Grafana/Prometheus volumes. It leaves the
private `.env` in place so a routine cleanup does not silently replace the login
secret; remove it deliberately when retiring the sample.
