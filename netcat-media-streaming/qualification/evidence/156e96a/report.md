# Netcat media qualification record

## Method

Every scenario starts from a finite 300-frame source. Reliable and RTSP streams decode to raw I420 and are checked frame by frame against the source. Datagram scenarios also parse the RFC 4571-framed RTP stream, account for every sequence number, and compare decoded frames with the reference in order. The repair scenario removes one percent of RTP packets before rstream and checks every RTCP/NACK lookup together with the decoded output.

## Acceptance gates

- Reliable and RTSP paths decode all 300 frames.
- Clean best-effort RTP delivers at least 99% of packets and 90% of reference-identical frames; guaranteed datagrams require 100% of both.
- The injected-loss path decodes all 300 frames and satisfies every NACK lookup inside the repair window.
- RTP analysis rejects malformed packets, timestamp regressions, and values outside the profile's duplicate or reordering budget.
- Unknown warning or error lines and incomplete process teardown fail the run.

## Recorded results

| Scenario | Observed result | Revision | Working tree |
| --- | --- | --- | --- |
| datagram-media-best-effort | PASS video quality identical_frames=300/300 identical=100.000% candidate_frames=300 altered_candidate_frames=0<br />PASS RTP packets=1221/1221 delivery=100.000% missing=0 duplicates=0 out_of_order=0 | `156e96a33b88` | clean |
| datagram-media-guaranteed | PASS video quality identical_frames=300/300 identical=100.000% candidate_frames=300 altered_candidate_frames=0<br />PASS RTP packets=1221/1221 delivery=100.000% missing=0 duplicates=0 out_of_order=0 | `156e96a33b88` | clean |
| reliable-media-matrix | PASS reliable media matrix: 4/4 scenarios passed | `156e96a33b88` | clean |
| rtcp-repair | PASS RTCP repair frames=300/300 delivery=100.0% feedback_bytes=13590 requests=258 found=258 loss=0.01 | `156e96a33b88` | clean |
| rtsp-media-matrix | PASS RTSP media matrix: Go and C++ bridges decoded 300 frames | `156e96a33b88` | clean |

Best-effort RTP, guaranteed datagrams, reliable byte streams, RTCP repair, and RTSP bridging remain separate verdicts because they exercise different delivery semantics. A clean best-effort run establishes fidelity on the recorded path; the injected-loss scenario establishes application-level repair.

Regenerate with `./qualification/run.sh`. Only evidence produced from a clean, pinned working tree is suitable for publication.
