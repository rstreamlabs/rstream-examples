# Publish a homelab Grafana dashboard

Run Grafana and Prometheus in Docker Compose. Grafana comes with a Prometheus
datasource and the **Homelab Monitoring** dashboard. When you enable the
rstream service, it reads Grafana's Docker labels and publishes the dashboard
through an outbound HTTPS tunnel. Prometheus stays on the Compose network and
the host's loopback interface.

## Start and verify the stack locally

Use Docker with the `docker compose` command, Node.js 24 or newer, and Make.
Enter the sample from the root of the examples checkout.

```sh
cd homelab-rstream
```

On Linux with the default local Docker socket:

```sh
node scripts/configure.mjs
```

On Docker Desktop, read the socket's group ID from inside the daemon's Linux
environment. The group of the macOS host socket may differ.

```sh
docker_gid="$(docker run --rm -v /var/run/docker.sock:/run/daemon.sock:ro \
  alpine:3.24 stat -c '%g' /run/daemon.sock)"
node scripts/configure.mjs --docker-gid "$docker_gid"
```

The helper creates a gitignored `.env` with mode `0600` and a randomly generated
Grafana admin password. It reuses an existing private file on later runs.
Inspect it locally to retrieve the password. Grafana uses these admin settings
when initializing a new database. Rotate an existing account in Grafana.

```sh
make check
make start
```

`make check` validates configuration without a running Docker daemon.
`make start` waits for Grafana's database and for the `grafana` and
`prometheus` jobs to appear in Prometheus. Sign in at
`http://127.0.0.1:13000` as `admin` and open **Homelab Monitoring**. Query
`up` in Prometheus at `http://127.0.0.1:19090` to inspect both scrape jobs.

For an automated provisioning check, run `make verify` after configuration.
It checks the datasource, dashboard, and scrape jobs, then removes its separate
test stack.

## Publish Grafana through rstream

Choose the deployment that owns the endpoint.

| Deployment | What to provide |
| --- | --- |
| Hosted or managed rstream | An engine address and a project-scoped agent token allowed to create the published HTTP tunnel. The project must permit the chosen browser access policy. |
| Direct Community Edition engine | The direct engine address and an agent JWT issued for that engine. Configure the engine's TLS certificates and published DNS first. |

Set these values in the same private `.env` used for the local run.

```dotenv
RSTREAM_ENGINE=<engine-host>:443
RSTREAM_AUTHENTICATION_TOKEN=<agent-token>
```

If the engine runs on the Docker host, set `RSTREAM_ENGINE_HOSTNAME` to the
hostname in `RSTREAM_ENGINE`. Compose maps that name to the host gateway so
the agent can keep using the engine's TLS hostname.

```sh
make run
docker compose logs -f rstream
```

When the watcher creates the Grafana tunnel, open the HTTPS URL printed in its
logs. The `rstream.tunnel.grafana.forward` label points to Grafana's container
port `3000`, so changing the host's loopback port does not change tunnel
routing. The watcher publishes only services with the corresponding labels.
See [Docker labels](https://rstream.io/docs/tunnels/declarative-tunnels/docker-labels)
and the [self-hosted engine guide](https://rstream.io/docs/tunnels/self-hosted).

## Choose the browser URL and access policy

For a durable URL, select a hostname your project or engine can serve. Add
these settings to `.env`, then run `make run` again. Compose recreates Grafana
with the updated root URL and adds the hostname label from
`compose.hostname.yaml`.

```dotenv
COMPOSE_PATH_SEPARATOR=:
COMPOSE_FILE=compose.yaml:compose.hostname.yaml
RSTREAM_GRAFANA_HOSTNAME=<stable-hostname>
GRAFANA_ROOT_URL=https://<stable-hostname>/
```

Grafana enforces its own login and disables anonymous access and sign-up. To
require an rstream account before visitors reach Grafana, set
`RSTREAM_HTTP_AUTH_RSTREAM=true` in `.env` and rerun `make run` on a project
that supports rstream Auth. Use a browser that can complete both sign-in
flows. See [Stable Domains](https://rstream.io/docs/tunnels/stable-domains)
and [rstream Auth](https://rstream.io/docs/tunnels/http-tunnels/authentication#rstream-auth).

The watcher runs as a non-root user with Docker socket group access. The socket
mount's `:ro` flag still lets the watcher send Docker API requests with
substantial host privileges. On a host that requires a restricted daemon API,
provide a socket proxy or host policy designed for that boundary.

## Configuration

| Setting | Default and use |
| --- | --- |
| `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD` | `admin` and a generated bootstrap password. The password is required. |
| `GRAFANA_PORT`, `PROMETHEUS_PORT` | Loopback ports `13000` and `19090`. |
| `RSTREAM_ENGINE`, `RSTREAM_AUTHENTICATION_TOKEN` | Engine address and agent identity required for publication. |
| `RSTREAM_ENGINE_HOSTNAME` | `rstream.local`. Override for an engine on the Docker host. |
| `RSTREAM_UID`, `RSTREAM_GID`, `DOCKER_GID` | Watcher user and group `1000:1000` and the socket's supplemental group. The helper sets `DOCKER_GID`. Compose falls back to `0`. |
| `COMPOSE_FILE`, `COMPOSE_PATH_SEPARATOR` | Select the base and hostname override files for a stable URL. The base alone requests an allocated address. |
| `RSTREAM_GRAFANA_HOSTNAME`, `GRAFANA_ROOT_URL` | Requested stable hostname and matching external Grafana origin. |
| `RSTREAM_HTTP_AUTH_RSTREAM` | `false`. Set to `true` to require rstream account sign-in on a supported project. |
| `RSTREAM_SITE_LABEL` | `home`. Grafana's tunnel also carries `env=homelab`, `service=grafana`, and `stack=monitoring`. |
| `RSTREAM_IMAGE`, `GRAFANA_IMAGE`, `PROMETHEUS_IMAGE` | Override the images selected in `compose.yaml`. |

## Stop or remove the stack

Run `make stop` to stop the services and keep the Grafana and Prometheus
data volumes. Run `make clean` when you want to remove the containers, network,
and both data volumes. Remove the private `.env` separately when retiring the
sample.
