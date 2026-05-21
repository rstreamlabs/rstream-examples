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

## Build and test

```bash
make verify
```

## Run through rstream

Use a local rstream context that points to an engine with HTTP/3 and MASQUE
support, then run:

```bash
PRIVATE_EGRESS_TOKEN="$(openssl rand -hex 24)" \
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

The process prints `READY <url>` when the published rstream endpoint is online.
The same endpoint carries TCP `CONNECT` and UDP `CONNECT-UDP` traffic.

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

The `probe` command can verify all protocol paths against a running gateway.
It expects TCP and UDP echo targets.

```bash
go run ./cmd/probe \
  --addr https://<published-rstream-host> \
  --tcp-target <tcp-echo-host:port> \
  --udp-target <udp-echo-host:port> \
  --access-token "$PRIVATE_EGRESS_TOKEN"
```

The probe checks plain `CONNECT` over HTTP/1.1, HTTP/2, and HTTP/3 downstreams,
then checks `CONNECT-UDP` over HTTP/3.

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
  --tcp-target 127.0.0.1:9000 \
  --udp-target 127.0.0.1:9001 \
  --access-token dev
```

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
