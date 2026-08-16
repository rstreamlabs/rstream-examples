# Netcat media qualification

This pack converts the interactive netcat media examples into finite,
machine-verifiable scenarios. It validates decoded frame counts rather than
merely checking that processes start, and it runs the reliable paths through
both the Go CLI and the C++ netcat implementation.

`run.sh` requires `rstream`, `rstream-ncat`, FFmpeg, GStreamer, Python 3, and a
timeout implementation (`timeout` or `gtimeout`). The commands may be selected
explicitly:

```bash
RSTREAM_CONTEXT=your-context \
RSTREAM_GO_BIN=/path/to/rstream \
RSTREAM_CPP_BIN=/path/to/rstream-ncat \
  ./qualification/run.sh
```

Run `make check` first when preparing a workstation. Missing dependencies are
reported as harness errors before an artifact directory is created; they are
never counted as rstream failures.

The runner creates `.artifacts/<UTC timestamp>/` with a pinned runtime manifest,
per-scenario logs, decoded-frame measurements, and an aggregate summary. It
removes tunnel processes on success, failure, or interruption. The large raw
frame buffers remain ignored local/CI artifacts; reports and their generators
are suitable for versioned evidence after a pinned release run.
`report.md`, `packet-delivery.svg`, and `video-quality.svg` are generated with
the Python standard library, so publishing the evidence does not depend on a
developer's plotting environment.

The datagram baselines transparently probe the RFC 4571 framed RTP stream
before and after rstream. They record packet delivery, loss, duplicates,
reordering, timestamp regressions, and reference-identical decoded frames
without changing the media bytes. The best-effort profile requires at least
99% packet delivery and 90% identical frames. The guaranteed-delivery profile
requires 100% for both. `RSTREAM_MIN_PACKET_DELIVERY_PERCENT` and
`RSTREAM_MIN_IDENTICAL_FRAMES_PERCENT` may only be changed when a qualification
profile explicitly documents the network impairment and its acceptance
threshold. RTCP repair is qualified separately because retransmission, rather
than reliable transport head-of-line blocking, is normally preferable for
interactive video.
