# Upgrade the QUIC companion libraries together

The examples use the Go SDK’s compatible QUIC 0.64.1 baseline. QUIC 0.65 changes the HTTP/3 stream API used by its companions; CONNECT-IP 0.7 cannot be adopted independently.

Wait for compatible published WebTransport and MASQUE releases, then update the affected examples with the SDK. Verify private egress, CONNECT-IP and WebTransport sessions, cancellation and cleanup. Keep the separate native datagram acceptance gate visible in the [Go SDK transport roadmap](https://github.com/rstreamlabs/rstream-go/blob/main/roadmap/002-quic-companions.md).
