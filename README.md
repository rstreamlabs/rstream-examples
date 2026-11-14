# rstream application examples

Build services and applications around rstream’s outbound connectivity model. These examples combine SDKs, the CLI and application frameworks to show complete integration paths, from a first HTTP service to private worker pools and real-time media.

Start with the [product documentation](https://rstream.io/docs) for accounts, projects and connection concepts. The [Go SDK](https://github.com/rstreamlabs/rstream-go) is the reference implementation; the [SDK overview](https://rstream.io/sdks) helps choose a language and runtime.

## Choose an example

| Example                                                 | What it demonstrates                                                                       |
| ------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| [Python HTTP service](python-fastapi-rstream-tunnel)    | Serve FastAPI directly through an HTTP tunnel, or dial the private variant.                |
| [Native HTTP service](cpp-beast-rstream-tunnel)         | Integrate a C++ Boost.Beast server with Asio tunnel sockets.                               |
| [Next.js preview](nextjs-rstream-preview)               | Publish a web application with webhook handling and an optional native Node tunnel server. |
| [Homelab services](homelab-rstream)                     | Discover Docker workloads and publish selected services while keeping others private.      |
| [Private PostgreSQL access](private-postgres-access)    | Connect database tools and migrations through a private bytestream tunnel.                 |
| [Published SSH](published-tcp-ssh)                      | Reach an SSH server through published TCP with public-key authentication.                  |
| [Private LLM mesh](private-llm-mesh)                    | Discover local-model workers, issue scoped tokens and route streamed inference.            |
| [Vision inference](python-vision-inference)             | Distribute camera frames to private inference workers and recover from worker loss.        |
| [WebRTC video](webrtc-video)                            | Stream from a Go producer to browsers directly or through a distributor.                   |
| [Netcat media](netcat-media-streaming)                  | Carry GStreamer and FFmpeg workloads through stream or datagram connections.               |
| [Private egress gateway](private-masque-egress-gateway) | Route controlled TCP and UDP egress through HTTP CONNECT and MASQUE.                       |

Each directory’s README gives its own dependencies, configuration and run command. The small HTTP examples are the shortest path to embedding a tunnel in an application; the larger examples add their own discovery, authorization and lifecycle logic.

## Configuration model

Most examples read a CLI context or explicit engine credentials. Some device applications instead receive short-lived configuration from their own backend. The WebRTC platform demonstrates that provisioning model: the device holds a product secret while the backend owns rstream application credentials. Follow the selected example’s setup rather than copying credentials between these roles.

## Development and support

[Contributing](CONTRIBUTING.md) covers source changes and the [testing guide](docs/testing.md) explains verification and optional failure/performance tools. Keep generated results outside version control. Coordinated SDK archive verification is described in [SDK dependencies](docs/sdk-dependencies.md).

Use [GitHub issues](https://github.com/rstreamlabs/rstream-examples/issues) for usage questions and reproducible bugs, with credentials and customer details removed. Report vulnerabilities through [Security](SECURITY.md). Licensed under [Apache-2.0](LICENSE).
