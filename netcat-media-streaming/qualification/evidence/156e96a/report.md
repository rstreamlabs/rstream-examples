# Netcat media qualification — revision 156e96a

The complete qualification passed on a clean checkout with rstream CLI 1.28.1
and rstream-ncat 1.14.2. The run exercised finite, decoded video streams rather
than process liveness alone.

![RTP packet delivery](packet-delivery.svg)

![Reference-identical decoded frames](video-quality.svg)

| Scenario | RTP delivery | Identical video frames | Revision | Working tree |
| --- | ---: | ---: | --- | --- |
| datagram-media-best-effort | 100.000% | 100.000% | `156e96a33b88` | clean |
| datagram-media-guaranteed | 100.000% | 100.000% | `156e96a33b88` | clean |
| reliable-media-matrix | — | — | `156e96a33b88` | clean |
| rtcp-repair | — | — | `156e96a33b88` | clean |
| rtsp-media-matrix | — | — | `156e96a33b88` | clean |

The reliable matrix decoded 300/300 frames through all four FFmpeg/GStreamer
and Go/C++ combinations. Both RTP profiles delivered 1,221/1,221 packets and
decoded 300/300 reference-identical frames. With 1% packet loss injected before
the tunnel, the bidirectional RTCP/NACK profile decoded 300/300 frames and
satisfied all 258 retransmission requests. The Go and C++ RTSP bridges each
decoded 300/300 frames from the same MediaMTX source. The log classifier found
no unknown warning or error.

The amber marker is the declared acceptance threshold. Best-effort RTP,
guaranteed datagrams, and application-level RTCP repair remain separate because
they make different latency and delivery trade-offs. A clean local run does not
claim that best-effort delivery is lossless on every network; the injected-loss
scenario is the evidence for the repair path.

The scenario directories retain the runtime manifests and exact analyzer
outputs. Regenerate the pack with `./qualification/run.sh`.
