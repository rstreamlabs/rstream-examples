# rstream examples

This repository contains end-to-end examples built around `rstream`.

Each example shows a complete integration path and provides a base that can be adapted to an actual device, service, or deployment. The focus is on runnable architectures, operational behavior, packaging, and integration with the rest of the application stack.

## Repository layout

The repository currently includes the following example:

| Directory | What it shows |
| --- | --- |
| `webrtc-video-streaming` | A device-side Go application that captures video with GStreamer, serves its own browser UI, publishes that UI through an `rstream` HTTP tunnel, uses the managed `rstream` STUN/TURN service for WebRTC connectivity, and packages cleanly for Linux devices. |

Each example directory contains its own README with the platform-specific setup, configuration profiles, build commands, and operational notes required by that example.

## Getting started

Most examples assume an active local `rstream` context. Start by installing the CLI, creating an account, creating a project, and selecting that project locally.

```bash
rstream login
rstream project use <project-endpoint> --default
```

Once the local context is in place, move into the example directory you want to run and follow its README.

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
