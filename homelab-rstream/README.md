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

## Run the stack

```bash
make verify
make start
```

Grafana is available locally at:

```text
http://127.0.0.1:13000
```

Prometheus is available locally at:

```text
http://127.0.0.1:19090
```

The default Grafana user is `admin`. Override the password before using the
stack for anything beyond local testing:

```bash
GRAFANA_ADMIN_PASSWORD="$(openssl rand -base64 32)" make start
```

## Publish Grafana from Docker labels

The Compose file carries rstream tunnel labels on the `grafana` service. Run the
rstream reconciler as a Compose service:

```bash
export RSTREAM_ENGINE="<engine-host>:443"
export RSTREAM_AUTHENTICATION_TOKEN="<agent-token>"
make run
```

`make run` is an alias for the full Docker profile with the rstream reconciler;
`make start` runs only Grafana and Prometheus for local checks.

This keeps tunnel configuration next to the container that owns the browser
surface. It scales cleanly when a homelab host runs several services and a
single rstream agent should reconcile them from Docker labels.

The sample mounts `/var/run/docker.sock` so the reconciler can inspect
containers and receive Docker events. Treat that as privileged access to the
Docker host. On hardened hosts, run the reconciler under a dedicated Docker
access policy or place a restricted Docker socket proxy in front of the daemon.

For local testing against an engine running on the Docker host, also set
`RSTREAM_ENGINE_HOSTNAME` to the hostname inside `RSTREAM_ENGINE`. The Compose
file maps that hostname to the Docker host through `host-gateway` so TLS keeps
the expected server name while the container reaches the local engine.

For a stable browser URL, set the tunnel hostname label and Grafana root URL
before starting the stack:

```bash
export RSTREAM_GRAFANA_HOSTNAME="<your-stable-domain>"
export GRAFANA_ROOT_URL="https://<your-stable-domain>/"
export GRAFANA_ADMIN_PASSWORD="$(openssl rand -base64 32)"
make run
```

Recreate the Grafana container after changing `GRAFANA_ROOT_URL`.

## Access policy

Grafana keeps its own login enabled and anonymous access disabled. For team
browser access, enable rstream Auth at the edge when the selected rstream
project supports it:

```bash
RSTREAM_ENGINE="<engine-host>:443" \
  RSTREAM_AUTHENTICATION_TOKEN="<agent-token>" \
  RSTREAM_HTTP_AUTH_RSTREAM=true \
  make run
```

If `RSTREAM_ENGINE` and `RSTREAM_AUTHENTICATION_TOKEN` are already exported,
only the `RSTREAM_HTTP_AUTH_RSTREAM=true` flag is needed for this policy change.

Token authentication is a better fit for API endpoints and automation than for
a browser application like Grafana, because browsers will not attach a bearer
token to every Grafana asset and API request automatically.

## Clean up

```bash
make clean
```
