# rstream application examples

rstream connects services and devices through outbound tunnels. This
repository shows where that connection fits in an application, from
serving one HTTP handler to routing requests across a private worker pool
or distributing live video.

Get the source with Git.

```sh
git clone https://github.com/rstreamlabs/rstream-examples.git
cd rstream-examples
```

For a first tunnel, follow the [FastAPI example](python-fastapi-rstream-tunnel) from this repository root. Each example gives its working directory, prerequisites and expected result.
See [prepare access](docs/000-running-examples.md) to choose between a
hosted project, a direct Engine and local fixtures.

## Serve a web application

| Example | What you can run |
| --- | --- |
| [FastAPI](python-fastapi-rstream-tunnel) | Serve an async Python application over HTTP, then add caller authentication or dial it privately. |
| [Boost.Beast](cpp-beast-rstream-tunnel) | Run a C++ coroutine HTTP server on Asio tunnel sockets. |
| [Next.js preview](nextjs-rstream-preview) | Publish a self-hosted Node app and receive a signed webhook. |

## Reach an existing service

| Example | What you can run |
| --- | --- |
| [Homelab services](homelab-rstream) | Publish Grafana while keeping Prometheus accessible only from the host and the Compose network. |
| [Private PostgreSQL](private-postgres-access) | Connect standard database tools through a local relay and a private tunnel. |
| [SSH over TCP](published-tcp-ssh) | Reach an SSH server via a published TCP address using public-key authentication. |
| [Private egress gateway](private-masque-egress-gateway) | Route TCP or UDP through an authenticated gateway with explicit destination policy. |

## Connect workers and media sources

| Example | What you can run |
| --- | --- |
| [Private LLM mesh](private-llm-mesh) | Stream inference from a local-model worker, then discover and route across a pool. |
| [Vision inference](python-vision-inference) | Send frames to private inference workers and render detections at the edge. |
| [WebRTC video](webrtc-video) | View a camera stream in a browser and add a distributor for more viewers. |
| [Netcat media](netcat-media-streaming) | Deliver GStreamer or FFmpeg media over rstream streams and datagrams. |

## Contributing and security

See [Contributing](CONTRIBUTING.md) for development and verification.
Open a [GitHub issue](https://github.com/rstreamlabs/rstream-examples/issues)
for usage questions or reproducible bugs. Follow the [security
policy](SECURITY.md) for vulnerability reports. The examples are licensed
under [Apache-2.0](LICENSE).
