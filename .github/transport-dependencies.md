# Go transport dependencies

The LLM worker and private MASQUE gateway use Go SDK v1.38.1 with QUIC
v0.60.0, WebTransport v0.11.1,
MASQUE v0.4.0, CONNECT-IP v0.1.0 and DTLS v3.1.15.
`TestReviewedTransportDependencyGraph` checks the linked versions.

The video producer retains its verified module graph. Run it through its
Makefile to apply the matching ICE patch.
Keep its WebRTC and Interceptor forks compatible with that patch. The video
distributor has its own module graph.

Run `make verify` in each module. The producer requires GStreamer and Node
build tools. The worker requires its pinned llama.cpp build. Worker inference
tests also require `MODEL` to point to a suitable GGUF model.
