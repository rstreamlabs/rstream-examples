# Netcat media qualification — PASS

| Scenario | Verdict | Result |
| --- | --- | --- |
| reliable-media-matrix | PASS | PASS reliable media matrix: 4/4 scenarios passed |
| datagram-media | PASS | PASS video quality identical_frames=300/300 identical=100.000% candidate_frames=300 altered_candidate_frames=0 PASS RTP packets=1221/1221 delivery=100.000% missing=0 duplicates=0 out_of_order=0 |
| datagram-media-guaranteed | PASS | PASS video quality identical_frames=300/300 identical=100.000% candidate_frames=300 altered_candidate_frames=0 PASS RTP packets=1221/1221 delivery=100.000% missing=0 duplicates=0 out_of_order=0 |
| rtcp-repair | PASS | PASS RTCP repair frames=300/300 delivery=100.0% feedback_bytes=13590 requests=258 found=258 loss=0.01 |
| rtsp-media-matrix | PASS | PASS RTSP media matrix: Go and C++ bridges decoded 300 frames |
| log-quality | PASS | # Qualification log quality — PASS  \| Class \| Count \| \| --- \| ---: \| \| gstreamer_fd_teardown \| 6 \| \| mpegts_latency_probe \| 4 \| \| rtp_sender_running_time \| 1 \| \| rtp_expired_nack \| 4 \|  Unknown warning or error lines: 0. Exceeded warning ceilings: 0. |

Each scenario directory contains its pinned manifest, process logs, exact frame-count evidence, and raw decoded buffers.
The result qualifies only the recorded repository revision, binaries, environment, and parameters.
