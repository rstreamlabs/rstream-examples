# Netcat media streaming

Send a GStreamer test pattern between two terminals on Linux or macOS over a private rstream tunnel. The producer writes encoded media to `rstream nc`. The consumer reads the bytes and displays the video.

## Play a test pattern

Install the rstream CLI and GStreamer on both machines. Use a project that supports [private tunnel dialing](https://rstream.io/docs/tunnels/private-tunnels). Use the same `your-project-endpoint` value on both machines.

```sh
rstream login
rstream project use your-project-endpoint
```

On the producer, verify `videotestsrc`, `videoconvert`, `x264enc`, `h264parse` and `mpegtsmux` with `gst-inspect-1.0`. On the consumer, verify `tsdemux`, `avdec_h264` and `autovideosink` and use a desktop session for playback. Install the matching elements from the GStreamer base, good, bad, ugly and libav plugin sets.

Start the producer. File descriptor 3 keeps MPEG-TS on the tunnel connection while GStreamer diagnostics go to standard error.

```sh
rstream nc -L rstrm://demo-ts -c 'exec 3>&1 1>&2
exec gst-launch-1.0 --no-position \
  videotestsrc is-live=true pattern=smpte ! \
  video/x-raw,width=1280,height=720,framerate=30/1 ! \
  videoconvert ! x264enc tune=zerolatency bitrate=2000 key-int-max=60 ! \
  h264parse config-interval=-1 ! mpegtsmux alignment=7 ! \
  fdsink fd=3 sync=false'
```

Dial `rstrm://demo-ts` from the consumer to decode and display the stream.

```sh
rstream nc rstrm://demo-ts | \
  gst-launch-1.0 --no-position \
  fdsrc fd=0 ! tsdemux ! queue ! h264parse ! avdec_h264 ! \
  videoconvert ! autovideosink sync=false
```

The consumer opens a window with SMPTE color bars. The tunnel carries an ordered byte stream and starts a fresh encoder process for each connection. Press `Ctrl-C` in both terminals when finished.

## Choose a media path

| Path                    | Use it for                                                                                                              | Client and procedure                                                                                                                                          |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Reliable MPEG-TS        | Preserve every byte in order.                                                                                           | Go `rstream nc` as above, or C++ `rstream-ncat`. Both clients work with the [FFmpeg recipes](https://rstream.io/guides/stream-video-with-ffmpeg-and-rstream). |
| Best-effort RTP         | Deliver newer packets without waiting for older missing packets.                                                        | Go `rstream nc -u`. See [Send RTP datagrams](#send-rtp-datagrams).                                                                                            |
| Guaranteed datagrams    | Preserve packet boundaries where complete delivery takes priority over recovering the latest frames quickly.            | Go `rstream nc -u` with `--datagram-guaranteed-delivery` on the listener. See the [qualification profile](qualification/README.md).                           |
| Bidirectional RTCP/NACK | Request missing packets while they are useful to the receiver.                                                          | Go `rstream nc -u`. See the [GStreamer guide](https://rstream.io/guides/stream-video-with-gstreamer-and-rstream).                                             |
| Shared RTSP             | Keep one camera source available through MediaMTX and bridge RTSP control and interleaved media over a reliable tunnel. | Go `rstream nc` or C++ `rstream-ncat`. See the [FFmpeg and MediaMTX guide](https://rstream.io/guides/stream-video-with-ffmpeg-and-rstream).                   |

## Send RTP datagrams

Use Enterprise Edition or a Cloud project with datagram support. The producer needs `rtph264pay` and `rtpstreampay`. The consumer needs `rtpstreamdepay`, `rtpjitterbuffer` and `rtph264depay` in addition to the H.264 decoder and video sink. `rtpstreampay` adds RFC 4571 length prefixes so `rstream nc -u` sends one RTP packet per datagram.

Start the RTP listener on the producer.

```sh
rstream nc -u -L rstrm://demo-rtp -c 'exec 3>&1 1>&2
exec gst-launch-1.0 --no-position \
  videotestsrc is-live=true pattern=smpte ! \
  video/x-raw,width=1280,height=720,framerate=30/1 ! \
  videoconvert ! x264enc tune=zerolatency bitrate=2000 key-int-max=60 ! \
  rtph264pay pt=96 mtu=1200 config-interval=1 ! \
  rtpstreampay ! fdsink fd=3 sync=false'
```

Dial `rstrm://demo-rtp` from the consumer.

```sh
rstream nc -u rstrm://demo-rtp | \
  gst-launch-1.0 --no-position \
  fdsrc fd=0 do-timestamp=true ! \
  application/x-rtp-stream,media=video,clock-rate=90000,encoding-name=H264,payload=96 ! \
  rtpstreamdepay ! rtpjitterbuffer latency=100 drop-on-latency=true ! \
  rtph264depay ! h264parse ! avdec_h264 ! videoconvert ! autovideosink sync=false
```

The same pattern plays with a 100 ms RTP jitter buffer. Stop both terminals with `Ctrl-C`.

| Option                           | Where to use it                                          | Effect                                                                                                                                  |
| -------------------------------- | -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `--idle-timeout 3s`              | Consumer dialing a finite datagram source.               | Ends the session three seconds after the last received datagram. Live sources normally use the default with the idle deadline disabled. |
| `--datagram-guaranteed-delivery` | Listener creating a guaranteed-delivery datagram tunnel. | Retransmits at the transport layer. The low-latency recipe above uses best-effort datagrams so the transport can drop late packets.     |
| `--max-connections N`            | Listener running `-c` per connection.                    | Caps the number of concurrent sessions and child processes.                                                                             |

## Verify a client or transport change

The [qualification suite](qualification/README.md) runs finite FFmpeg and GStreamer sources and checks decoded frames, packet delivery, repair and teardown. It lists the full toolchain, both client builds and the command that writes the report.
