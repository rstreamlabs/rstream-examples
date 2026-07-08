# WebRTC video

This sample groups the WebRTC reference implementation around its two roles:

- `producer/`: Go agent that runs on a device or homelab machine, captures video, creates the rstream tunnel, and serves the WebRTC session.
- `platform/`: Next.js product application that provisions producer tunnels, authorizes viewers, and watches tunnel state.

The root Makefile targets the agent role, so `make build`, `make run`,
`make test`, `make verify`, and `make clean` delegate to `producer/`.

Use the platform directly with npm:

```bash
cd platform
npm install
npm run prisma:migrate
npm run dev
```
