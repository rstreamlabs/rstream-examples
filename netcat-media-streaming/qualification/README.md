# Netcat media qualification

Run a finite, headless matrix against a project that supports private byte-stream and datagram tunnels. The runner creates private tunnels with unique names and cleans up its child processes on completion or interruption.

## Run the matrix

From `netcat-media-streaming`, install `rstream`, `rstream-ncat`, FFmpeg with `libx264`, GStreamer with the base, good, bad, ugly and libav plugin sets, MediaMTX, jq, netcat, Python 3, `awk`, `shasum` and `timeout` or `gtimeout`. Select the rstream project in your CLI context before running. To use locally built clients, export `RSTREAM_GO_BIN` and `RSTREAM_CPP_BIN` before both commands.

```sh
make check
output_directory=$(mktemp -d)
./qualification/run.sh "$output_directory"
```

`make check` validates the toolchain and runs the Python analyzer tests. The runner repeats that preflight, runs all five profiles and checks their logs. Open `summary.md` for the verdict and `report.md` for the consolidated measurements in the output directory. Each scenario directory has its manifest, exact commands, logs and analyzer output. Keep raw frame buffers and other run output with local or CI artifacts. `make qualify` runs the same matrix with the default output under `qualification/.artifacts/`.

## Profiles and acceptance criteria

The default target is 300 frames at 320 × 240. The reliable and RTSP cases decode raw I420 and require the exact decoded frame count. The best-effort and guaranteed datagram cases capture the RFC 4571 RTP stream at both ends, compare unique sequence numbers and compare decoded frames against the reference in order.

| Profile                     | Clients                               | Passing result                                                                                                                                                                               |
| --------------------------- | ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `reliable-media-matrix`     | Go and C++ with FFmpeg and GStreamer. | All 300 frames decode with each media tool over both Go-to-C++ and C++-to-Go tunnels.                                                                                                        |
| `datagram-media`            | Go.                                   | At least 99% of unique RTP packets arrive and at least 90% of reference frames match.                                                                                                        |
| `datagram-media-guaranteed` | Go.                                   | All unique RTP packets and reference frames arrive intact.                                                                                                                                   |
| `rtcp-repair`               | Go.                                   | The decoder receives all 300 target frames despite dropping about 1% of RTP packets before rstream. RTCP feedback produces NACKs and the sender finds at least as many packets as requested. |
| `rtsp-media-matrix`         | Go and C++ with FFmpeg.               | Both consumers decode all 300 frames through separate bridges to one shared MediaMTX RTSP source.                                                                                            |

The datagram analyzer fails malformed RTP and unexpected sequence numbers. It reports duplicate arrivals, reordering and timestamp regressions for diagnosis. The RTCP profile uses a 1,000 ms receiver buffer, a 15,000 ms sender repair queue and 60 trailing source frames so feedback can arrive after the target frames. Teardown failures and unclassified media warnings or errors fail the matrix.

## Environment

| Variable                                                                      | Default                                                        | Purpose                                                                           |
| ----------------------------------------------------------------------------- | -------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `RSTREAM_CONTEXT`                                                             | Active CLI context.                                            | Select a different project context for both clients.                              |
| `RSTREAM_GO_BIN`                                                              | `rstream` on `PATH`.                                           | Absolute path to an executable Go CLI.                                            |
| `RSTREAM_CPP_BIN`                                                             | `rstream-ncat` on `PATH`.                                      | Absolute path to an executable C++ CLI.                                           |
| `RSTREAM_MEDIA_FRAMES`                                                        | `300`                                                          | Number of target frames in each profile.                                          |
| `RSTREAM_MEDIA_WIDTH`, `RSTREAM_MEDIA_HEIGHT`                                 | `320`, `240`                                                   | Source dimensions. Use even values for I420.                                      |
| `RSTREAM_MIN_PACKET_DELIVERY_PERCENT`, `RSTREAM_MIN_IDENTICAL_FRAMES_PERCENT` | `99` and `90` for best effort, `100` and `100` for guaranteed. | Override the two RTP acceptance thresholds for an explicitly impaired experiment. |
| `RSTREAM_RTP_DROP_PROBABILITY`                                                | `0.01`                                                         | Probability of dropping each RTP packet in the RTCP repair profile.               |
| `RSTREAM_RTCP_MIN_FRAME_DELIVERY_PERCENT`                                     | `100`                                                          | RTCP repair decoded-frame threshold.                                              |
| `RSTREAM_RTCP_REPAIR_TAIL_FRAMES`                                             | `60`                                                           | Additional source frames during the RTCP repair window.                           |

To run one profile, pass a fresh output directory to its script. Each profile checks its own prerequisites. The matrix adds cross-profile log classification.

```sh
./qualification/rtcp-repair.sh "$(mktemp -d)"
```
