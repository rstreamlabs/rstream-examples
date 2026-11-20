# Publish SSH over a TCP tunnel

Give an OpenSSH client a public hostname and TCP port while keeping the SSH
container on its private Compose network. The rstream agent connects outbound
to the engine and forwards each incoming TCP connection to `ssh:22`. SSH
encrypts the session end to end and authenticates the server and user.
For services that need rstream to enforce TLS at the edge, use a TLS tunnel.

## Prepare the project or engine

Use Docker with Compose on the server and OpenSSH on the client. Have an SSH
key pair ready and copy only its public key to the server.

| Deployment | Requirements |
| --- | --- |
| Hosted or managed rstream | A Pro or Enterprise project with **Published TCP tunnels** enabled and public access set to `allowed`. Raw TCP is incompatible with `auth-required` because application authentication happens inside SSH. Provide a scoped agent token authorized to create this published TCP tunnel. |
| Direct Community Edition engine | An engine with an ephemeral `published_tcp` listener range configured and reachable from the client. Provide an agent JWT issued by the engine operator. |

See [TCP Tunnels](https://rstream.io/docs/tunnels/tcp-tunnels) for published TCP
policy and engine configuration.

Set the runtime values from this directory. Use the path to your public key
and a protected file containing the agent token.

```sh
export SSH_PUBLIC_KEY_PATH="$HOME/.ssh/id_ed25519.pub"
export RSTREAM_ENGINE="<engine-host>:443"
export RSTREAM_AUTHENTICATION_TOKEN="$(cat /path/to/agent-token)"
docker compose config --quiet
```

| Setting | Purpose |
| --- | --- |
| `SSH_PUBLIC_KEY_PATH` | Required readable file. The container installs this public key for `demo`. |
| `RSTREAM_ENGINE` | Required agent connection address in `host:port` form. |
| `RSTREAM_AUTHENTICATION_TOKEN` | Required engine or project credential for the agent. |
| `RSTREAM_IMAGE` | Override for the agent image. Defaults to `rstream/rstream:latest`. |
| `--tcp-port` | Optional previously reserved project port for `rstream forward`. The Compose command defaults to an ephemeral address. |

## Connect over the published address

```sh
docker compose up -d --build
docker compose logs -f rstream
```

Copy the public hostname and port from the agent log. The stack keeps running
after Ctrl+C exits the log view. From a trusted shell on the Compose host, get
the server's host key fingerprint before the first SSH connection.

```sh
docker compose exec -T ssh ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

On the client, set `tcp_host` and `tcp_port` to the logged values. Use the
private key that matches the public key mounted by Compose.

```sh
ssh -o HostKeyAlgorithms=ssh-ed25519 \
  -i "$HOME/.ssh/id_ed25519" -p "$tcp_port" "demo@$tcp_host" id -un
```

Compare the fingerprint shown by the client with the one on the Compose host.
Expect the command to print `demo`. Omit `id -un` to open a shell.
The server allows public-key authentication for the demo account and disables
password, keyboard-interactive, and root login.

## Use a reserved address for recurring access

The ephemeral address lasts for the current tunnel. On a managed project, use
the project's **TCP** page or run `rstream project tcp-address reserve` in the
project's CLI context to obtain a durable hostname and port. Add
`"--tcp-port", "<reserved-port>"` to the end of the rstream service's
`command` list in `compose.yaml`, substituting the numeric port. Run
`docker compose up -d rstream` to recreate the agent and connect using the
reserved address.

The demo creates SSH host keys in the container. If you recreate the SSH
container, obtain its new fingerprint before accepting it. Manage persistent
host keys separately for a long-lived SSH service.

## Stop the example

With the Compose variables still set, run `docker compose down` to stop
the tunnel and SSH server. For a managed reservation that you no longer need,
release it from the project's **TCP** page or run
`rstream project tcp-address release <port>` after stopping the tunnel.
