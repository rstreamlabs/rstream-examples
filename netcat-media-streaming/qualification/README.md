# Netcat media qualification

This pack turns the interactive media pipelines into finite tests whose output
can be compared with the source. It qualifies five distinct paths: reliable
byte streams, best-effort RTP datagrams, guaranteed datagrams, bidirectional
RTCP/NACK repair, and RTSP bridging.

## Method

Every scenario starts from a 300-frame source. Reliable and RTSP paths decode
to raw I420 and must produce the expected frame count. Datagram paths also
capture the RFC 4571-framed RTP stream on both sides of rstream, account for
every sequence number, and compare decoded frames with the reference in order.
The repair scenario removes one percent of RTP packets before rstream and sends
RTCP/NACK feedback over the reverse channel.

Reliable-stream and RTSP scenarios cross FFmpeg and GStreamer with both client
implementations. Datagram and RTCP/NACK scenarios use the Go CLI. The C++
client provides bytestream connectivity.

| Scenario                  | Clients                         |
| ------------------------- | ------------------------------- |
| Reliable media            | Go and C++, in both directions. |
| Best-effort RTP datagrams | Go.                             |
| Guaranteed datagrams      | Go.                             |
| RTCP/NACK repair          | Go.                             |
| RTSP bridging             | Go and C++, in both directions. |

The matrix checks framing, half-close, standard-input/output and teardown
across those supported paths.

## Acceptance gates

- Reliable and RTSP paths decode all 300 frames.
- Clean best-effort RTP delivers at least 99% of packets and 90% of
  reference-identical frames; guaranteed datagrams require 100% of both.
- The loss-repair path decodes all 300 frames and satisfies every NACK lookup
  inside the one-second repair window.
- RTP analysis rejects malformed packets, timestamp regressions, and values
  outside the profile's duplicate or reordering budget.
- Every child process exits within its deadline. Unknown warning or error lines
  fail the run.

Threshold overrides are intended for an explicitly impaired profile and are
recorded in its manifest. They are not a way to accept a failing clean-path
run.

## Run the matrix

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

The runner creates `.artifacts/<UTC timestamp>/` with the exact commands,
versions, source revision, thresholds, analyzer output, frame counts, and
classified logs. It owns every child process and cleans up on success, failure,
timeout, or interruption. Raw frame buffers stay in local or CI artifacts;
`report.md`, the manifests, and compact JSON measurements are sufficient to
review a published run.
