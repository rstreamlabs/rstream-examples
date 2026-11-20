# Run a private MASQUE egress gateway

Run a gateway on the network that should provide Internet egress. The gateway
opens an outbound connection to rstream and clients use its published HTTPS
endpoint. Plain HTTP `CONNECT` carries TCP traffic. MASQUE `CONNECT-UDP` over
HTTP/3 carries UDP, including QUIC. The gateway authenticates requests and
checks target addresses before dialing them.

## Verify locally

Use Go 1.27.4 or newer, Git, Make, and a C compiler for race tests. Commands run
from this directory. The local tests use loopback fixtures and require no
rstream account.

```sh
make verify
```

The test suite checks TCP `CONNECT` and `CONNECT-UDP` over HTTP/3 with local echo
servers, token rejection, target policy, certificate verification, and retry
behavior. It also builds `out/bin/private-masque-egress-gateway` and
`out/bin/private-masque-egress-probe`.

To run the gateway itself on loopback, start it in one terminal:

```sh
PRIVATE_EGRESS_TOKEN=dev ./out/bin/private-masque-egress-gateway \
  --local --allow-loopback-targets
```

Expect `READY https://127.0.0.1:9443`. The probe needs TCP and UDP echo
services on loopback ports `9000` and `9001` respectively.

<details>
<summary>Start temporary loopback echo servers with Python 3</summary>

```sh
python3 - <<'PY'
from socketserver import BaseRequestHandler, TCPServer, UDPServer
from threading import Thread

class TCPEcho(BaseRequestHandler):
    def handle(self):
        while data := self.request.recv(4096):
            self.request.sendall(data)

class UDPEcho(BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)

with TCPServer(("127.0.0.1", 9000), TCPEcho) as tcp, \
     UDPServer(("127.0.0.1", 9001), UDPEcho) as udp:
    Thread(target=tcp.serve_forever, daemon=True).start()
    udp.serve_forever()
PY
```

</details>

With the gateway and echo services running, probe from another terminal:

```sh
./out/bin/private-masque-egress-probe \
  --addr https://127.0.0.1:9443 \
  --downstream h3 --insecure-demo-tls \
  --tcp-target 127.0.0.1:9000 --udp-target 127.0.0.1:9001 \
  --access-token dev
```

Expect `CONNECT h3 ok` and `CONNECT-UDP ok`. The local gateway generates an
ephemeral self-signed certificate. The explicit TLS bypass is limited to
literal loopback addresses and cannot be combined with CA or server-name
overrides.

## Publish from the egress network

| Deployment | What to use |
| --- | --- |
| Hosted rstream Pro or Enterprise | A project context with permission to publish an HTTP/3 datagram tunnel and a runtime with the corresponding HTTP/3 and MASQUE listeners enabled. For an interactive setup, run `rstream login` and `rstream project use <project-endpoint>`. |
| Private rstream deployment | An agent context for a runtime with published HTTP/3 upstream and MASQUE enabled. Confirm these capabilities with the deployment operator. |
| Community Edition or no rstream runtime | Use the local HTTP/3 mode. Publishing this gateway requires the HTTP/3 upstream and MASQUE capabilities of one of the deployments above. |

The gateway reads the selected rstream context through the Go SDK. That
context authenticates the agent when it creates the tunnel. Use a separate
random gateway token for downstream proxy requests. Supply one before
publishing, since an empty token leaves gateway admission open. The basic
probe below assumes the project allows published endpoints without additional
edge authentication. See [Editions and Plans](https://rstream.io/docs/support/editions)
and [Connection Upgrades](https://rstream.io/docs/tunnels/http-tunnels/connection-upgrades).

On the gateway host, create and retain a private token file outside the
checkout, then start the gateway. This step uses OpenSSL.

```sh
egress_dir="$HOME/.config/rstream-egress"
install -d -m 700 "$egress_dir"
token_file="$egress_dir/demo-token"
if [ ! -e "$token_file" ]; then
  (umask 077 && openssl rand -hex 32 > "$token_file")
fi
chmod 600 "$token_file"
export PRIVATE_EGRESS_TOKEN="$(cat "$token_file")"

test -n "$PRIVATE_EGRESS_TOKEN" && ./out/bin/private-masque-egress-gateway \
  --name home-egress --label role=egress \
  --metrics 127.0.0.1:9090
```

Keep this terminal running and copy the HTTPS URL printed after `READY`.
By default, the engine allocates the hostname. On a project or deployment
that supports stable hostnames, add `--hostname <stable-hostname>` to use a
name available to that project.

Published mode reconnects after transient failures using capped exponential
backoff with jitter. Invalid local configuration and non-retryable Engine
errors exit.

## Check the published endpoint

In a second terminal, load the same gateway token. If the client is on
another host, transfer that token through your secret store. Set
`gateway_url` to the printed URL and `tcp_target` and `udp_target` to the
`host:port` addresses of echo services you control, reachable from the
gateway and permitted by its address policy.

```sh
export PRIVATE_EGRESS_TOKEN="$(cat "$HOME/.config/rstream-egress/demo-token")"
./out/bin/private-masque-egress-probe \
  --addr "$gateway_url" \
  --tcp-target "$tcp_target" --udp-target "$udp_target" \
  --access-token "$PRIVATE_EGRESS_TOKEN"
```

With all downstream HTTP versions enabled, expect:

```text
CONNECT h1 ok
CONNECT h2 ok
CONNECT h3 ok
CONNECT-UDP ok
```

Use `--downstream h3` to check only the HTTP/3 paths. The probe verifies the
gateway certificate and hostname using system trust. For a private CA, add
`--ca-file /path/to/gateway-ca.pem`. Use `--server-name` when the intended
certificate name differs from the endpoint host. These flags control the
client's connection to the published gateway.

The gateway returns `407` for a missing or incorrect access token and `403`
for a denied destination. Inspect request counters on the gateway host with
`curl -fsS http://127.0.0.1:9090/metrics`.

## Operate the gateway

Direct public traffic through a runtime and project with the required
edge policy. If the project requires an rstream proof, enable the matching
`--rstream-token-auth` or `--rstream-auth` option and use a client that can
send both the edge proof and the gateway access header. The supplied probe
and generated profile each send the configured gateway header.

The gateway blocks private, loopback, link-local, multicast, documentation,
and other special-purpose ranges by default. Keep the target allowances
disabled for Internet egress. For a local lab or an intentionally scoped
internal gateway, enable only the target class you need. The target policy
applies to the resolved address.

For an always-on host, run the built gateway binary under the host's
service manager. Give the service account a protected configuration and
credential for the intended rstream context. Supply `PRIVATE_EGRESS_TOKEN` in
the service's secret configuration and set `RSTREAM_CONFIG` and
`RSTREAM_CONTEXT` to that account's configuration file and context. Use the
same flags and token as in manual verification. See
[Run the gateway at boot](https://rstream.io/guides/build-private-residential-egress-gateway-with-masque-and-rstream#run-the-gateway-at-boot).

### Apple Network Relay

With the published endpoint working, restart the gateway with a stable
hostname, one or more explicit `--match-domain` flags, and
`--write-mobileconfig "$HOME/.config/rstream-egress/home-egress.mobileconfig"`.
The profile carries an HTTP/3 relay URL, an HTTP/2 fallback URL, and the
gateway token. Keep the profile in the private directory and distribute it
only to intended client devices. On a supported Mac, use
`plutil -lint "$HOME/.config/rstream-egress/home-egress.mobileconfig"` before
installing it in System Settings. Test a matching domain and observe the
gateway counters. An empty match-domain list makes the relay eligible for
broad client routing. Match domains select client traffic. The gateway
enforces its own destination policy.

The sample generates the `com.apple.relay.managed` profile payload. Apple
deprecates this payload on macOS 27 and iOS 27 and documents a declarative
Network Relay configuration for managed deployments. See
[Relay payload](https://developer.apple.com/documentation/devicemanagement/relay)
and [Network Relay declaration](https://developer.apple.com/documentation/devicemanagement/networkrelay).

### IP packet routing

Leave `CONNECT-IP` disabled for TCP and UDP egress. The
`--connect-ip-diagnostic` flag enables a packet echo backend for protocol
testing. Real IP forwarding requires a packet-routing backend such as a TUN
device or router integration.

## Configuration

| Gateway setting | Default and use |
| --- | --- |
| `PRIVATE_EGRESS_TOKEN`, `--access-token`, `--access-header` | Use the retained gateway token in `PRIVATE_EGRESS_TOKEN`. The flag overrides it and the header defaults to `X-Egress-Token`. |
| `RSTREAM_CONFIG`, `RSTREAM_CONTEXT` | Select the configuration file and agent context in published mode. |
| `--name`, `--hostname`, `--label` | Tunnel name defaults to `private-masque-egress`. Use a stable hostname and repeatable `key=value` labels as needed. |
| `--local`, `--listen` | Run a standalone HTTP/3 listener. Default UDP bind is `127.0.0.1:9443`. |
| `--metrics` | Optional bind address for Prometheus counters. Keep it on loopback for local inspection. |
| `--rstream-auth`, `--rstream-token-auth` | Request the relevant edge authentication method when the project and clients support it. Both default to `false`. |
| `--allow-private-targets`, `--allow-loopback-targets`, `--allow-link-local-targets` | Opt in to the corresponding destination class. All default to `false`. |
| `--retry`, `--retry-interval` | Published mode retries by default with a five-second base interval. Set `--retry=false` for a single attempt. |
| `--write-mobileconfig`, `--match-domain` | Output path and repeatable domain routes for an Apple Relay profile. |
| `--connect-ip-diagnostic`, `--verbose` | Enable the diagnostic packet echo backend or debug logging. Both default to `false`. |

| Probe setting | Default and use |
| --- | --- |
| `--addr` | Required gateway URL. The port defaults to `443` when omitted. |
| `--tcp-target`, `--udp-target`, `--downstream` | Echo targets in `host:port` form. Select `all` (default), `h1`, `h2`, or `h3` for TCP. Use `none` for a UDP-only check. Supply a UDP target to check `CONNECT-UDP`. |
| `--access-header`, `--access-token` | Gateway header and credential. The header defaults to `X-Egress-Token`. |
| `--ca-file`, `--server-name` | Add PEM roots to system trust or set the certificate verification name and SNI. |
| `--insecure-demo-tls` | `false`. Local self-signed demo on a literal loopback address only. |
| `--timeout` | Total probe time budget, `20s`. |

## Stop the gateway

Use Ctrl+C for the local echo servers and an interactive gateway, or stop a
managed gateway using its host service. Remove any installed Relay profile
when retiring the endpoint. Delete the generated profile and token file when
you no longer need that credential. Run `make clean` to remove the local
build outputs.
