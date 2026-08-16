# Adaptive real-time WebRTC video

This reference implementation covers the path from a remote video source to a
browser-facing product. The device adapts its encoder to live congestion,
bounds sender queues, repairs recent packet loss, and keeps the WebRTC session
recoverable as network paths change. rstream publishes signaling through an
outbound tunnel and supplies managed STUN/TURN connectivity; media remains a
standard WebRTC session between the device and viewer.

The implementation is split by responsibility:

- [`producer/`](./producer/) is the Go device agent. It captures with
  GStreamer, serves signaling and diagnostics, controls Pion WebRTC, and owns
  the adaptive media loop.
- [`platform/`](./platform/) is the Next.js product layer. It provisions
  devices, issues scoped producer and viewer access, and exposes live tunnel
  state without proxying the media session.

The root Makefile targets the device role, so `make build`, `make run`,
`make test`, `make verify`, and `make clean` delegate to `producer/`. The
[producer README](./producer/README.md) starts with the standalone path and
continues through the congestion, repair, mobility, and qualification model.

Run the platform directly with npm:

```bash
cd platform
npm install
npm run prisma:migrate
npm run dev
```
