# Go transport dependencies

The video producer, private MASQUE gateway and LLM worker qualify the same Go
SDK transport baseline: QUIC v0.64.1, WebTransport v0.13.0, MASQUE v0.6.0,
CONNECT-IP v0.6.0 and DTLS v3.1.15. Each module's
`TestReviewedTransportDependencyGraph` checks actual compiled dependency metadata
and rejects unexpected versions or replacements for this tuple. Run the producer
through its Makefile so the generated ICE modfile is also checked.

The review branch pins immutable SDK source
`v1.37.1-0.20261113005657-bc2415800ddf` from SDK PR #185. This is a Go source
commit reference, not a published stable SDK release. Replace it with the
certified stable SDK through the normal release process before integration;
source qualification does not authorize publication, merge or deployment.

Preserve the producer's WebRTC and interceptor forks and generated ICE patch.
The distributor has no QUIC dependency and retains its separately reviewed
transport graph. Updating the SDK does not justify rebasing unrelated forks.

The gateway uses CONNECT-IP v0.6 request/parsing and queued address/route APIs.
Its diagnostic backend binds cancellation to connection closure so an idle
packet reader terminates even when no more packets arrive. The real HTTP/3
cancellation regression must pass under `go test -race`; it fails without the
explicit lifecycle binding.

Use `make verify` in the three changed modules. The producer requires GStreamer
and its Node build tools; the worker requires its pinned llama.cpp build. Worker
model inference tests additionally require `MODEL` to reference a suitable GGUF
model; compiling and running the ordinary unit suite does not qualify inference.
