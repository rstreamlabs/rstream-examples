# Python vision inference over rstream

This sample splits an edge vision workload across two roles connected only by
rstream tunnels. A device captures video and serves an annotated viewer. A
worker runs a YOLO model wherever the compute is. Neither side has a public
IP, an open port, or any knowledge of the other's network, and rstream
provides both the transport and the signaling between them.

## How it works

Workers join the pool by creating a private tunnel labeled `role=inference`.
There is no registry service: the tunnel inventory is the registry. Devices
seed their worker pool from `list_tunnels` filtered by that label, then keep
it current through the real-time watch API, so starting a worker anywhere adds
capacity within seconds and stopping one removes it. The labels also carry the
`model`, the `device` (`cpu`, `mps`, `cuda:0`), and the `accelerator` name, so
the pool view in any viewer reads the hardware behind each worker straight from
the registry, with no extra protocol. Both roles also survive losing the engine
connection itself: the worker recreates its registration tunnel, the device
re-seeds its pool and republishes the viewer, each with a capped backoff, while
sessions in flight ride their own connections.

Each device dials one worker and opens a session. The worker starts the
session with a `hello` advertising its model, input size, and supported
codecs, and the device adapts its encoding policy to that answer: frames are
resized to the model input before encoding, which is the cheapest bandwidth
win available, then sent as JPEG by default (`--codec png` switches to
lossless for fidelity-critical domains, `--codec webp` trades CPU for
bandwidth). Up to two frames stay in flight so transfer overlaps inference,
and since the device always sends its most recent frame, a slow worker simply
lowers the detection rate without building a queue anywhere.

Every frame leaves the device with a `frame_id` and a timestamp from the
device's own monotonic clock, and results echo the `frame_id`. The device
never compares clocks across machines: the worker reports its inference time
as an interval, the device measures the round trip on its own clock, and the
difference is the network share. From the smoothed round trip the device sizes
a small display delay, an application-layer jitter buffer, so each rendered
frame carries the detections computed for that exact frame rather than boxes
trailing behind moving objects.

Load spreads across the pool without a balancer. Each device ranks workers
by rendezvous hashing, so different devices prefer different workers, then
probes its two best candidates and keeps the less loaded one, the classic
power of two choices, using the session count each worker reports in its
hello. When the pool empties, the device suspends detection and resumes it by
itself as soon as capacity returns; an explicit enable or disable from the UI
always wins over that automatism. The viewer can also pin the session to a
specific worker by clicking it in the pool, which routes inference there
explicitly; the pin is advisory, so if that worker disappears the device falls
back to automatic selection and snaps back when it returns.

Display is fully decoupled from inference. The video always plays, whether or
not a worker is reachable and whether or not detection is enabled. A ByteTrack
tracker runs on the device and carries box identities across frames, and
across worker failovers: workers are stateless on purpose, so any pool member
can serve any device and a kill mid-session only causes a brief gap in the
overlay while the video keeps playing. When detections stop being fresh, the
overlay simply disappears.

## Install

Both roles need Python 3.10+. Each role owns its own dependencies and Makefile:
`worker/` installs the YOLO inference stack, while `device/` installs the
viewer, capture, registry, and tracking stack.

```bash
make build
```

You can also prepare a single role directly:

```bash
make -C worker build
make -C device build
```

## Run a worker

Run this on the machine that has the compute, with a project selected through
the rstream CLI (`rstream login`, then `rstream project use <endpoint>
--default`).

```bash
make -C worker run
# or, from this directory
make run-worker ARGS="--device cpu"
```

The first run downloads the YOLO weights. `--model`, `--imgsz`, and `--conf`
select the model, its input size, and the confidence threshold. The worker
picks the best inference device automatically (CUDA, then Apple MPS, then CPU,
since ultralytics defaults to CPU even when a GPU is present) and advertises it
through its labels; `--device cpu` forces one, which is handy for running a CPU
worker and a GPU worker side by side from the same machine. Start more workers
on other machines to grow the pool; every worker registers itself through its
tunnel labels.

## Run the device

Run this where the camera is. The default source downloads a short highway
traffic clip on first run ([Pexels video 2103099](https://www.pexels.com/video/2103099/),
free Pexels license, 720p60), so the sample is reproducible without hardware
and gives the model something dense to detect.

```bash
make -C device run ARGS="--source 0"          # first local camera
make -C device run                            # downloaded highway clip
make -C device run ARGS="--source synthetic"
```

The device prints the viewer address. By default it derives a stable
project-scoped domain once at startup, the same way the Go and C++ SDKs do, and
reuses it on every reconnect, so when the engine connection drops and the tunnel
is recreated the open tab keeps working on the same URL. The address is fresh
on each process start; pass `--host` for a fixed name that also survives full
restarts (see [Stable Domains](https://rstream.io/docs/tunnels/stable-domains)).
The page shows the annotated stream, the
live worker pool, the active session, and the latency breakdown: source and
detection FPS, inference time, network time, the display buffer, and the
uplink bandwidth. The Enable/Disable detection button pauses inference while the
video keeps playing, with the device exposing its actual state (connected,
reconnecting, waiting for workers, disabled) alongside the desired one; since the viewer is published, protect it with
`--token-auth` when the URL leaves your hands.

Stop the worker currently serving the device and watch the session move to
another worker within seconds, with the video playing through the switch. Add
a worker back and the pool regrows, with no restart anywhere.

## Web UI

The viewer page is served by the device process itself through the published
tunnel. Frames travel as timestamped JPEGs with backpressure-driven adaptive
quality, over a plain byte stream rather than a multipart response, since
WebKit special-cases `multipart/x-mixed-replace` and silently fails `fetch()`
reads of it. The page plays the frames through a browser-side jitter buffer: a
playback clock renders them at their true cadence behind a small adaptive
delay, so network jitter shallower than the buffer never reaches the eye. Two
buffers thus protect different things, the device delays display only while
detection runs, just long enough to align each frame with its own boxes, and
the browser buffers to absorb the network. For full-codec browser delivery,
see the WebRTC guides.

The front end is TypeScript bundled with esbuild, and the stylesheet is
generated with Tailwind:

```bash
cd device
npm install
npm run build
```

The equivalent Makefile command is:

```bash
make -C device build
```

The generated files under `device/web/generated/` are ignored and rebuilt by
the device Makefile.
