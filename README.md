# rstream examples

This repository contains end-to-end examples built around `rstream`.

Each example shows a complete integration path and provides a base that can be adapted to an actual device, service, or deployment. The focus is on runnable architectures, operational behavior, packaging, and integration with the rest of the application stack.

## Repository layout

The repository currently includes the following examples:

| Directory                       | What it shows                                                                                                                                                                                                                                                 |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cpp-beast-rstream-tunnel`      | A native C++ Boost.Beast HTTP server that creates a published rstream HTTP tunnel with the C++ SDK and accepts inbound requests as `rstream::io_rstrm::socket` streams.                                                                                      |
| `homelab-rstream`               | A Docker-based homelab monitoring stack that publishes Grafana from Docker labels, keeps Prometheus private, and uses labels for inventory.                                                                                                                 |
| `nextjs-rstream-preview`        | A standard Next.js App Router application with a signed webhook endpoint and an advanced SDK tunnel mode that serves a custom Next.js HTTP server directly through rstream while preserving the normal `next dev` and `next start` commands.                  |
| `private-masque-egress-gateway` | A private MASQUE egress gateway that publishes a rstream HTTP/3 endpoint and serves plain `CONNECT` for TCP plus `CONNECT-UDP` for UDP/QUIC, with conservative target policy, auth, metrics, and Apple Relay profile generation.                              |
| `private-postgres-access`       | A private bytestream tunnel pattern for PostgreSQL, using `rstream nc` to connect local tools, migrations, or CI jobs to a database that stays inside a private network without a VPN.                                                                        |
| `webrtc-video-streaming`        | A device-side Go application that captures video with GStreamer, serves its own browser UI, publishes that UI through an `rstream` HTTP tunnel, uses the managed `rstream` STUN/TURN service for WebRTC connectivity, and packages cleanly for Linux devices. |
| `webrtc-video-platform`         | A third-party Next.js application that owns device inventory, GitHub authentication, producer provisioning, and short-lived viewer URLs while using the rstream JS SDKs for tunnel inventory, TURN credentials, and fine-grained auth tokens.                 |

Each example directory contains its own README with the platform-specific setup, configuration profiles, build commands, and operational notes required by that example.

## Getting started

Most examples assume an active local `rstream` context. Start by installing the CLI, creating an account, creating a project, and selecting that project locally.

```bash
rstream login
rstream project use <project-endpoint> --default
```

Once the local context is in place, move into the example directory you want to run and follow its README. The Next.js platform example uses rstream application credentials instead of a local CLI context.

```bash
cd webrtc-video-streaming
```

## Design goals

The examples in this repository follow a few simple rules:

- keep the code small enough to read and modify
- keep the architecture close to a real deployment
- prefer standard interfaces and idiomatic integrations
- make packaging and deployment paths explicit when they matter

In practice, that means an example may still be compact while handling the kinds of issues that appear quickly on real systems, such as disconnects, media-pipeline failures, or deployment on machines without a local toolchain.
