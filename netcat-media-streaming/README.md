# Netcat media streaming

This example keeps live media deliberately simple: standard GStreamer or
FFmpeg processes write to and read from `rstream nc` through standard input and
output. It covers two different transport choices:

- reliable MPEG-TS over a private byte stream when every byte matters;
- packetized RTP over a private datagram tunnel when bounded latency matters
  more than retransmitting late data.

Both machines must use an rstream context for the same project. Install the
rstream CLI and GStreamer, then check the required elements:

```bash
rstream login
rstream project use <project-endpoint>
make check
```

## Reliable GStreamer stream

On the producer, create a private tunnel and attach one GStreamer process to
each connection. File descriptor 3 carries only media; diagnostics remain in
the terminal.

```bash
TTY=$(tty); rstream nc -L rstrm://demo-ts -c 'exec 3>&1 1>'"$TTY"' 2>'"$TTY"'; \
  exec env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
  gst-launch-1.0 -v --no-position \
  videotestsrc is-live=true pattern=smpte ! \
  video/x-raw,width=1280,height=720,framerate=30/1 ! \
  videoconvert ! x264enc tune=zerolatency bitrate=2000 key-int-max=60 ! \
  h264parse config-interval=-1 ! mpegtsmux alignment=7 ! \
  fdsink fd=3 sync=false'
```

On the consumer:

```bash
rstream nc rstrm://demo-ts | \
  env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
  gst-launch-1.0 -v --no-position \
  fdsrc fd=0 ! tsdemux name=demux \
  demux. ! queue ! decodebin ! videoconvert ! autovideosink sync=false
```

## Low-latency RTP datagrams

The RTP variant preserves packet boundaries and does not force late packets to
block newer frames. `rtpstreampay` supplies the RFC 4571 framing consumed by
`rstream nc -u`.

Producer:

```bash
rstream nc -u -L rstrm://demo-rtp -c 'exec 3>&1 1>&2; \
  exec env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
  gst-launch-1.0 -v --no-position \
  videotestsrc is-live=true pattern=smpte ! \
  video/x-raw,width=1280,height=720,framerate=30/1 ! \
  videoconvert ! x264enc tune=zerolatency bitrate=2000 key-int-max=60 ! \
  rtph264pay pt=96 mtu=1200 config-interval=1 ! \
  rtpstreampay ! fdsink fd=3 sync=false'
```

Consumer:

```bash
rstream nc -u rstrm://demo-rtp | \
  env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
  gst-launch-1.0 -v --no-position \
  fdsrc fd=0 do-timestamp=true ! \
  application/x-rtp-stream,media=video,clock-rate=90000,encoding-name=H264,payload=96 ! \
  rtpstreamdepay ! rtpjitterbuffer latency=100 drop-on-latency=true ! \
  rtph264depay ! h264parse ! avdec_h264 ! \
  videoconvert ! autovideosink sync=false
```

Datagrams do not carry an end-of-stream marker. For a finite producer, add an
idle deadline so the receiving CLI exits after the last packet instead of
waiting for a future datagram:

```bash
rstream nc -u --idle-timeout 3s rstrm://demo-rtp
```

Do not add that deadline to a live camera unless an idle source should close
and be restarted by its supervisor.

Do not add guaranteed datagram delivery to this RTP path: retransmitting late
packets below RTP can replace visible loss with growing latency. The full
GStreamer guide adds bidirectional RTCP/NACK repair; the FFmpeg guide adds the
direct MPEG-TS and shared MediaMTX/RTSP variants.

## Qualification

The [`qualification/`](./qualification/) pack replaces the live sources with
finite 300-frame inputs and checks the decoded result, not just process exit.
It crosses FFmpeg and GStreamer with the Go and C++ netcat implementations,
parses RTP sequence numbers, injects packet loss ahead of the tunnel, and
verifies the RTCP/NACK repair path. Process teardown and unexpected media
warnings are part of the verdict.

The published [`156e96a` evidence pack](./qualification/evidence/156e96a/report.md)
records a clean run across every profile: 300 decoded frames in all four
reliable Go/C++ and FFmpeg/GStreamer combinations, 1,221/1,221 RTP packets and
300/300 reference-identical frames in both datagram profiles, 300/300 frames
after RTCP/NACK repair at 1% injected loss, and 300/300 frames through each RTSP
bridge. The clean best-effort result establishes fidelity on the recorded path;
the injected-loss profile is the evidence for packet repair.

```bash
make qualify
```

Set `RSTREAM_CONTEXT` to select a non-default CLI context. Set
`RSTREAM_GO_BIN` or `RSTREAM_CPP_BIN` when testing locally built SDK binaries
instead of commands installed on `PATH`.
