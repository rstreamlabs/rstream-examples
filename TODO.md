# Deferred dependency migrations

The dependency update on August 23, 2026 intentionally left the following migrations for dedicated work.

- `nextjs-rstream-preview` and `webrtc-video/platform` — migrate to TypeScript 7 after the Next.js applications and their generated types are qualified together.
- `private-llm-mesh/web` — migrate to Motion 13 after validating animation behavior and the generated interface at every supported viewport.
- `private-llm-mesh/web` — migrate to ESLint 10 and TypeScript 7 after the Next.js lint toolchain supports them without runtime plugin failures.
- `private-llm-mesh/web` — update Shiki beyond 4.3 after `streamdown` and `@streamdown/code` agree on the same highlighter types.
- `private-masque-egress-gateway` — update `connect-ip-go` and `quic-go` after the rstream Go SDK and `masque-go` support the new capsule API together.
- `python-vision-inference/worker` — evaluate `supervision` 0.30 separately because the current dependency range deliberately excludes that release.
- `webrtc-video/distributor` — update the rstream Pion interceptor fork after migrating the FlexFEC decoder construction removed by the latest fork revision.
- `webrtc-video/producer` — evaluate the Go GStreamer bindings separately. The `v1.4.x` module line is retracted and upstream restarted releases on `v0.0.x`, so this is a source migration rather than a routine version bump.
