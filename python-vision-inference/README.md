# Python vision inference over rstream

Run a video source and a YOLO worker on one laptop, then open a browser to see tracked detections over the video. The same two commands can run on separate machines later. The device captures and displays the video and discovers workers through rstream. Each worker runs inference and returns detections over a private tunnel. The device publishes a separate HTTPS viewer at the edge. Both processes connect outbound and need no local inbound port.

## Before you start

| Where          | What you need                                                                                                                                                                          |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Both roles     | Git, Make, and Python with `venv` and pip. Use Python 3.13 for this walkthrough. CI covers Python 3.11 and 3.13.                                                                       |
| Device         | Node.js 24 and npm to build the browser assets. The default highway clip lets you start without a camera.                                                                              |
| Worker         | Enough disk space to install PyTorch and download the default `yolov8n.pt` weights. A CPU is sufficient for the default model.                                                         |
| Hosted rstream | The [rstream CLI](https://rstream.io/download) and one hosted Pro or Enterprise project shared by both roles. Workers use [private tunnels](https://rstream.io/docs/support/editions). |

## 1. Prepare the laptop

Use the same project for the device and worker. Replace `YOUR_PROJECT_ENDPOINT` with the endpoint from the Dashboard.

```bash
rstream login
rstream project use YOUR_PROJECT_ENDPOINT
git clone https://github.com/rstreamlabs/rstream-examples.git
cd rstream-examples/python-vision-inference
make build PYTHON=python3.13
```

The build creates a separate virtual environment for each role and builds the viewer assets. On a separate worker machine, `make -C worker build PYTHON=python3.13` builds just the inference role. If Python 3.13 is the default `python3`, you can omit the `PYTHON` override.

## 2. Start a worker and open the viewer

From the example directory, start a worker in the first terminal.

```bash
make run-worker PYTHON=python3.13 ARGS="--name vision-first --device cpu"
```

Wait for `Worker ready: vision-first` after the model download and warmup. In a second terminal in the same directory, start the device.

```bash
make run-device PYTHON=python3.13
```

Open the **Viewer address** printed by the device. The highway clip should play, `vision-first` should appear in the worker pool, and the viewer should show detections on vehicles along with the inference time and detection frame rate. The device downloads and caches the [Pexels highway clip](https://www.pexels.com/video/2103099/) on first run. If the clip is unavailable, it falls back to a synthetic animation. The animation is enough to check video playback and an active inference session. Use the highway clip or another scene with recognizable objects to check bounding boxes.

The default viewer URL gives anyone who can reach it access to the video and the detection controls. Use the sample clip for the first run. For private footage, provide browser-session access control or an authenticated gateway in front of the viewer. On hosted rstream, `--token-auth` requires a scoped token on every request, including assets, streams, and controls. A gateway can supply that token on each upstream request. See [HTTP tunnel authentication](https://rstream.io/docs/tunnels/http-tunnels/authentication).

## 3. Verify failover

1. Start a second worker with `make run-worker PYTHON=python3.13 ARGS="--name vision-second --device cpu"`.
2. Wait for both workers to appear, then stop the worker serving the device. The video continues while detection reconnects. Confirm the active worker changes and detections resume.
3. Restart the stopped worker and verify it rejoins the pool. Click a worker in the pool to pin detection to it. If it disappears, the device uses another worker until the pinned worker returns.

Switch detection off in the UI to check that the video plays without the overlay. To finish, stop the device and both workers with Ctrl+C in their respective terminals.

If a worker is missing, run `rstream tunnel list --filter 'status=online,labels.role=inference'` with the same context used by the device. Confirm the worker is online in that project or on that engine. Check the worker terminal for model download, warmup, or registration errors. An empty pool still allows video playback.

## Choose credentials and deployment

Both Python entry points use `rstream.Client.from_env()`. Run both roles with a shared hosted project or a shared direct engine.

| Credential source              | Use                                                                                                                                                                                                                                                                                                                                |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hosted CLI context             | Run `rstream login` and `rstream project use YOUR_PROJECT_ENDPOINT` on each host. Set `RSTREAM_CONTEXT` to use a specific named context.                                                                                                                                                                                           |
| Provisioned engine credentials | Set `RSTREAM_ENGINE` to the engine address and `RSTREAM_AUTHENTICATION_TOKEN` to its agent token for each process. On hosted rstream, use project-scoped tokens and keep the device's viewer token separate from its agent token.                                                                                                  |
| Self-hosted Community Edition  | Use the direct engine address and a JWT signed for that engine. The engine must serve HTTPS for the device viewer with working DNS and trusted certificates. Restrict viewer reachability at your own network boundary. CE supports private bytestream and published HTTP tunnels, and hosted `--token-auth` is unavailable there. |

With scoped hosted credentials, grant workers access to register their private inference tunnels. The device needs to list and watch those tunnels, dial workers, and publish its viewer. See the [SDK configuration guide](https://github.com/rstreamlabs/rstream-python/blob/main/docs/001-configuration.md) and [Community Edition setup](https://rstream.io/docs/tunnels/self-hosted).

## Change the source or model

Pass flags to the appropriate Make target with `ARGS`, for example `make run-device ARGS="--source 0"`.

| Role   | Option                         | Default and use                                                                                                                                                                                                                                                                             |
| ------ | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Device | `--source`                     | `sample`. Use a camera index such as `0`, a video file path, or `synthetic` for generated frames without a media download.                                                                                                                                                                  |
| Device | `--name`                       | `vision-device`. Choose a distinct name for each capture device.                                                                                                                                                                                                                            |
| Device | `--host`                       | On hosted rstream, the device generates a hostname at startup and reuses it across tunnel reconnects. Set an explicit hostname to reuse it after a process restart or to give a direct-engine viewer a fixed address. See [Stable domains](https://rstream.io/docs/tunnels/stable-domains). |
| Device | `--token-auth`                 | Off by default. Require a scoped HTTP edge token for the hosted viewer.                                                                                                                                                                                                                     |
| Device | `--fps`                        | `0` to use the source rate or the capture fallback. Set an explicit capture rate when needed.                                                                                                                                                                                               |
| Device | `--codec`, `--quality`         | `jpeg` at quality `80`. Choose `webp` or lossless `png` for the worker uplink. Quality applies to lossy codecs.                                                                                                                                                                             |
| Worker | `--model`, `--imgsz`, `--conf` | `yolov8n.pt`, `640`, and `0.4`. Select the weights, input size, and confidence threshold.                                                                                                                                                                                                   |
| Worker | `--device`                     | Automatic selection tries CUDA, then Apple MPS, then CPU. Set `cpu`, `mps`, or a CUDA device such as `cuda:0` explicitly.                                                                                                                                                                   |
| Worker | `--name`                       | Ephemeral name. Set a distinct stable name when you want to pin a device to this worker.                                                                                                                                                                                                    |
| Worker | `--max-sessions`               | `4` concurrent sessions. Excess connections are rejected, and idle sessions time out.                                                                                                                                                                                                       |

## How the sample works

The device lists and watches tunnels labeled `role=inference`. The tunnel labels describe the worker's model, accelerator, and engine region. Each worker session starts with a `hello` that advertises its input size, codecs, and current load. Automatic routing uses rendezvous hashing to pick up to two candidates, probes them concurrently, and prefers the lower active-session count divided by capacity. Equal normalized load is resolved by session establishment time.

The device resizes each input for the worker's model before encoding, with up to two frames in flight. When inference is slower than capture, the device sends the newest available frame and drops older ones from the inference path. Worker results echo the frame ID. The device measures round-trip time on its own monotonic clock and uses the worker's reported inference duration to estimate the remaining network time. A display buffer aligns detections with the corresponding video frame. ByteTrack state stays on the device, so tracking can continue across worker changes.

The device serves the viewer in-process with the Python SDK's ASGI bridge. The browser receives timestamped JPEGs and buffers them for steady playback. This is separate from the device's frame alignment buffer.

## Verify changes and explore further

Run `make verify PYTHON=python3.13` from this directory to check the Python roles, protocol, and viewer build with local fixtures. Use the [qualification tools](qualification/README.md) for real models, workload thresholds, and controlled multi-worker selection and recovery. See [Testing](../docs/001-testing.md) for the shared workflow.

The [rstream vision guide](https://rstream.io/guides/run-edge-vision-inference-on-your-own-machines-with-rstream) explains the integration. See the [WebRTC example](../webrtc-video/README.md) if you need full-codec browser video delivery.
