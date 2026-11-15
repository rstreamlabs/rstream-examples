# Private MASQUE egress gateway

This example runs a private residential egress gateway behind a published
rstream HTTP/3 tunnel.

The gateway accepts:

- plain HTTP `CONNECT` for TCP egress
- `CONNECT-UDP` for UDP and QUIC egress
- guarded `CONNECT-IP` protocol handling for packet-routing backends

The default configuration is intentionally conservative: target addresses in
private, loopback, link-local, multicast, and documentation ranges are denied
unless explicitly allowed. This prevents an accidentally exposed relay from
becoming a path into the gateway's local network.

## Prerequisites

Use Go 1.27.4 or newer (see `go.mod`), Git, Make and a C compiler for race tests.
OpenSSL generates a demo token below. Local tests require no account; publishing
also requires the CLI, a selected permitted project and an Engine supporting the
selected HTTP/3/MASQUE path. The example does not reserve infrastructure or add
an IP-routing backend.

## Build and test

```bash
make verify
```

## Run through rstream

Use a local rstream context that points to an engine with HTTP/3 and MASQUE
support, then run:

```bash
umask 077
mkdir -p "$HOME/.config/rstream-egress"
token_file="$HOME/.config/rstream-egress/demo-token"
if [ ! -f "$token_file" ]; then openssl rand -hex 24 > "$token_file"; fi
chmod 600 "$token_file"
export PRIVATE_EGRESS_TOKEN="$(cat "$token_file")"
go run ./cmd/gateway \
  --name home-austin \
  --label role=egress \
  --label network=residential \
  --label country=US \
  --label region=us-south \
  --metrics 127.0.0.1:9090 \
  --match-domain example.com \
  --write-mobileconfig ./home-austin.mobileconfig
```

Keep terminal one running. The token is retained in the private file outside the
repository; rerunning the setup preserves it. A second terminal must load that
**same** value for the probe; do not generate a new one or copy it into logs. A prefixed assignment to `go run` alone
would not retain the value for later shell commands.

The process prints `READY <url>` when the published rstream endpoint is online.
The same endpoint carries TCP `CONNECT` and UDP `CONNECT-UDP` traffic.
Published mode reconnects automatically after transient rstream transport or
engine failures. Configuration and authorization errors still fail immediately
so that the process manager and monitoring can surface them. Reconnection uses
capped exponential backoff with jitter and coalesces repeated failures into one
degraded-state log. Use `--retry=false` for a one-shot process, or
`--retry-interval` to change the default five-second base delay.

## Run at boot

For an always-on host, build the binary once and run it with the host process
manager. The gateway host does not need Go installed if you build elsewhere and
copy the binary over. A systemd service should read `PRIVATE_EGRESS_TOKEN`,
`RSTREAM_CONFIG`, `RSTREAM_CONTEXT`, and the stable gateway hostname from a
`0600` environment file, then start `private-masque-egress-gateway` with the same
flags used during manual validation. `RSTREAM_CONTEXT` must name a context in
the deployed `RSTREAM_CONFIG`; validate that pair before enabling the service.

Keep the generated `.mobileconfig` protected like a secret because it contains
the gateway token. For a residential Internet egress gateway, leave
`--allow-private-targets`, `--allow-loopback-targets`, and
`--allow-link-local-targets` disabled.

For local lab targets on `127.0.0.1`, add:

```bash
--allow-loopback-targets
```

For LAN targets, add:

```bash
--allow-private-targets
```

Do not enable these allowances for an Internet-facing egress gateway unless the
gateway is intended to expose those networks and is protected by explicit access
policy.

## Verify

In terminal two, after loading the same `PRIVATE_EGRESS_TOKEN`, the `probe` command can verify all protocol paths against a running gateway.
It expects TCP and UDP echo targets.

```bash
export PRIVATE_EGRESS_TOKEN="$(cat "$HOME/.config/rstream-egress/demo-token")"
go run ./cmd/probe \
  --addr https://<published-rstream-host> \
  --tcp-target <tcp-echo-host:port> \
  --udp-target <udp-echo-host:port> \
  --access-token "$PRIVATE_EGRESS_TOKEN"
```

The probe checks plain `CONNECT` over HTTP/1.1, HTTP/2, and HTTP/3 downstreams,
then checks `CONNECT-UDP` over HTTP/3. It verifies the gateway certificate and
hostname using system trust by default. For a private CA, add
`--ca-file /path/to/gateway-ca.pem`; use `--server-name gateway.example` only when
that is the intended certificate name (it also sets SNI). These settings apply to
the downstream gateway, separately from the agent's Engine TLS configuration.
An untrusted certificate or wrong name is an error, including on H2/H3.

## Local HTTP/3 mode

For fast development without rstream:

```bash
PRIVATE_EGRESS_TOKEN=dev \
go run ./cmd/gateway \
  --local \
  --listen 127.0.0.1:9443 \
  --allow-loopback-targets
```

Then run the H3-only checks:

```bash
go run ./cmd/probe \
  --addr https://127.0.0.1:9443 \
  --downstream h3 \
  --insecure-demo-tls \
  --tcp-target 127.0.0.1:9000 \
  --udp-target 127.0.0.1:9001 \
  --access-token dev
```

The local gateway creates an ephemeral self-signed certificate. The explicit
`--insecure-demo-tls` option above is restricted to literal loopback addresses
(`127.0.0.1` or `::1`); it cannot be used against a published hostname or combined
with CA/name overrides. It is a local protocol demonstration, not an identity
verification. Prefer a trusted CA and matching name for a deployed gateway.

## Probe TLS options

| Option | Default / purpose |
| --- | --- |
| `--ca-file` | Add a PEM CA bundle to system roots; malformed or missing bundles fail before connecting. |
| `--server-name` | Override the address host for verification and SNI deliberately. |
| `--insecure-demo-tls` | False; literal-loopback synthetic demos only. |
| `--timeout` | Total probe budget, default 20 seconds. |
| `--access-header`, `--access-token` | Application gateway admission, separate from TLS identity and rstream agent credentials. |
| `--downstream` | `all`, `h1`, `h2`, `h3`, or `none`; UDP checks run when `--udp-target` is supplied. |

## Apple Relay profile

`--write-mobileconfig` writes a macOS/iOS Relay payload using the published
rstream endpoint. The profile includes:

- `HTTP3RelayURL` for MASQUE `CONNECT-UDP`
- `HTTP2RelayURL` as a TCP fallback endpoint on the same rstream hostname
- `AdditionalHTTPHeaderFields` containing the gateway access token

Install the profile only on devices that should use this private egress path.
Use repeated `--match-domain` flags to start with explicit domains. If no match
domain is provided, Apple treats the relay as eligible for all domains except
excluded domains configured elsewhere in the profile.

## CONNECT-IP

`CONNECT-IP` requires a real packet routing backend for production IP egress.
This example keeps that path disabled by default. `--connect-ip-diagnostic`
enables a small packet echo backend for protocol validation; use a TUN or router
backend when the gateway should forward IP packets beyond the process.
