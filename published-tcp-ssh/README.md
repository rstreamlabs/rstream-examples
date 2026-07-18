# Publish SSH over a TCP tunnel

This example exposes an OpenSSH server through a published rstream TCP tunnel. The SSH container is reachable only from the private Compose network; the rstream agent creates the public TCP listener and forwards accepted connections to it.

SSH provides encryption, server identity, and public-key authentication. rstream does not add security to the downstream side of a raw TCP tunnel. Use this pattern only with an application protocol that provides its own protection, and use a TLS tunnel for TLS traffic.

## Prerequisites

- Docker with Compose
- a Pro or Enterprise rstream project
- published TCP enabled in the project settings
- a project public access policy set to `allowed`
- an SSH public key
- an engine address and authentication token for the project

Set the runtime values without copying private key material into the example:

```bash
export SSH_PUBLIC_KEY_PATH="$HOME/.ssh/id_ed25519.pub"
export RSTREAM_ENGINE="<project-engine>:443"
export RSTREAM_AUTHENTICATION_TOKEN="<token>"
```

The token should be scoped to the project and to the tunnel properties needed by this example.

## Run

Start the SSH server and rstream agent:

```bash
docker compose up --build
```

The rstream log prints the allocated public hostname and port. Connect as the `demo` user with the private key matching `SSH_PUBLIC_KEY_PATH`:

```bash
ssh -i "$HOME/.ssh/id_ed25519" -p <port> demo@<tcp-hostname>
```

The address is ephemeral and changes when the tunnel is recreated. For a stable address, reserve one from the project TCP page or with `rstream project tcp-address reserve`, then add `--tcp-port <reserved-port>` to the rstream service command.

Stop the example with:

```bash
docker compose down
```

No SSH port is published from the container to the Docker host. The only public entrypoint is the TCP address allocated by the engine.
