# rstream examples

This repository contains end-to-end examples built around `rstream`.

Each example shows a complete integration path and provides a base that can be adapted to an actual device, service, or deployment. The focus is on runnable architectures, operational behavior, packaging, and integration with the rest of the application stack.

## Repository layout

The repository currently includes the following examples:

| Directory                       | What it shows                                                                                                                                                                                                                                                 |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cpp-beast-rstream-tunnel`      | A native C++ Boost.Beast HTTP server that creates a published rstream HTTP tunnel with the C++ SDK and accepts inbound requests as `rstream::io_rstrm::socket` streams.                                                                                      |
| `python-fastapi-rstream-tunnel` | A FastAPI application served directly through a published rstream HTTP tunnel with the Python SDK, including an edge token-auth mode and a private variant consumed over a raw rstream dial.                                                                  |
| `homelab-rstream`               | A Docker-based homelab monitoring stack that publishes Grafana from Docker labels, keeps Prometheus private, and uses labels for inventory.                                                                                                                 |
| `nextjs-rstream-preview`        | A standard Next.js App Router application with a signed webhook endpoint and an advanced SDK tunnel mode that serves a custom Next.js HTTP server directly through rstream while preserving the normal `next dev` and `next start` commands.                  |
| `private-llm-mesh`              | A private LLM mesh with a Next.js chat app, live tunnel discovery, scoped worker tokens, and Go workers that embed llama.cpp to serve local OpenAI-compatible models over rstream.                                                                           |
| `private-masque-egress-gateway` | A private MASQUE egress gateway that publishes a rstream HTTP/3 endpoint and serves plain `CONNECT` for TCP plus `CONNECT-UDP` for UDP/QUIC, with conservative target policy, auth, metrics, and Apple Relay profile generation.                              |
| `private-postgres-access`       | A private bytestream tunnel pattern for PostgreSQL, using `rstream nc` to connect local tools, migrations, or CI jobs to a database that stays inside a private network without a VPN.                                                                        |
| `python-vision-inference`       | An edge vision workload split across `device/` and `worker/` roles with the Python SDK: devices stream frames to private YOLO inference workers discovered through tunnel labels and the real-time watch API, with live failover and a published annotated viewer. |
| `webrtc-video`                  | A WebRTC scenario split into a Go `producer/` agent that runs on a device or homelab machine and a `platform/` Next.js application that provisions producers, authorizes viewers, and watches tunnel state.                                                |

Each example directory contains its own README with the platform-specific setup, configuration profiles, build commands, and operational notes required by that example.

## Integration postures

The examples do not all place `rstream` authority in the same part of the
architecture. That is intentional: depending on the product shape, `rstream` can
be abstracted behind an application backend, or it can remain explicit on the
process running on a device, worker, or homelab machine.

| Posture | Cloud application / backend | Device, worker, or homelab process |
| ------- | --------------------------- | ---------------------------------- |
| Application-provisioned runtime | The backend owns the rstream application credentials, knows the target project, mints short-lived scoped tokens, and exposes a product API. | The remote process has no local rstream context. It receives a product secret and fetches its rstream runtime configuration when it starts. |
| Self-managed rstream runtime | A backend may still use rstream for discovery, authorization, or routing, but it does not necessarily provision the runtime identity of each agent. | The remote process carries its own rstream context through the CLI, local config, or injected environment variables such as an engine address and token. |

The application-provisioned posture is shown by `webrtc-video/platform` with the
producer provisioning profile. The producer does not run `rstream login`, does
not select a project locally, and does not handle long-lived rstream
credentials. It only knows the platform URL and a `DEVICE_SECRET`; the platform
returns the short-lived rstream configuration required to create exactly the
producer tunnel and to refresh TURN credentials.

This can be the right shape for a SaaS product, a multi-device platform, or an
onboarding flow where rstream should be hidden behind domain-specific product
concepts. It avoids project-selection mistakes on devices and centralizes
authorization in the backend. The tradeoff is that the product must operate a
provisioning layer: application credentials, device secrets, rotation,
revocation, auditing, and failure handling when a device cannot reach the
product API.

The self-managed runtime posture is used by most other examples. The process on
the remote machine creates or dials tunnels with its own rstream context, using
`rstream login` / `rstream project use`, a config file, or injected runtime
credentials. This is often simpler for labs, homelabs, internal fleets,
infrastructure tools, and examples that focus directly on SDK or CLI
primitives. It also keeps the runtime flexible: changing the active project or
environment is an operational decision made where the process runs, and
debugging can use the same CLI context as the application. The tradeoff is that
rstream is less abstracted from the machine: every host must be provisioned
carefully, host credentials must be protected, and the active project must be
intentional. That mobility is useful for operators, but it can be a source of
mistakes in a product meant for unmanaged devices.

`private-llm-mesh` combines the two ideas without introducing a third posture.
The web application is rstream-aware: it uses application credentials to watch
the worker pool, discover workers, and mint short-lived tokens for calls to a
selected worker. The workers themselves join the pool with their own runtime
rstream context. This keeps the application in control of request routing while
letting each compute machine remain an autonomous rstream runtime participant.

## Getting started

For self-managed runtime examples, start by installing the CLI, creating an
account, creating a project, and selecting that project locally.

```bash
rstream login
rstream project use <project-endpoint> --default
```

Once the local context is in place, move into the example directory you want to
run and follow its README. Application-provisioned examples document their own
backend credentials and device secret flow instead.

```bash
cd webrtc-video
```

## Design goals

The examples in this repository follow a few simple rules:

- keep the code small enough to read and modify
- keep the architecture close to a real deployment
- prefer standard interfaces and idiomatic integrations
- make packaging and deployment paths explicit when they matter

In practice, that means an example may still be compact while handling the kinds of issues that appear quickly on real systems, such as disconnects, media-pipeline failures, or deployment on machines without a local toolchain.
