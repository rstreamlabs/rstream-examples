# See LICENSE file in the project root for license information.

"""Vision device: capture frames, get detections from the worker pool, serve a viewer.

The device owns every piece of state in the system. It captures frames and
timestamps them on its own monotonic clock, discovers inference workers
through tunnel labels and the real-time watch, and pairs results back to
frames by ``frame_id``. Worker selection combines rendezvous hashing with the
power of two choices over the session count each worker reports in its hello,
so load spreads across the pool with no balancer. When the pool empties the
device suspends detection and resumes it when capacity returns; an explicit
user action always overrides the automatism.

Display is decoupled from inference. The video always plays; while detection
runs it sits behind a small synchronization delay sized from the measured
round trip, so each frame is rendered together with the detections computed
for that exact frame, and the displayed frame id only moves forward. A
ByteTrack tracker carries box identities across frames and across worker
failovers, since workers are stateless. The
viewer stream adapts its JPEG quality to the measured drain time toward the
browser, trading fidelity for cadence when the link is the bottleneck.
"""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import time
import urllib.request
from collections import OrderedDict
from contextlib import suppress
from pathlib import Path

import cv2
import numpy as np
import supervision as sv
from fastapi import FastAPI
from fastapi.responses import HTMLResponse, Response, StreamingResponse
from trackers import ByteTrackTracker

import rstream
from protocol import read_message, send_message

WEB_DIR = Path(__file__).parent / "web"
WORKER_FILTERS = rstream.TunnelFilters(labels={"role": "inference"})
RESPONSE_TIMEOUT = 20.0
DIAL_COOLDOWN = 1.0
SESSION_COOLDOWN = 3.0
MAX_IN_FLIGHT = 2
FRESHNESS_LIMIT = 1.0
FRAME_BUFFER_SIZE = 64  # ~1 s at 60 fps, above the 500 ms jitter-target cap
DISPLAY_MAX_WIDTH = 960
DISPLAY_FPS = 24.0
CODECS = {"jpeg": ".jpg", "webp": ".webp", "png": ".png"}

# Highway traffic clip from Pexels (https://www.pexels.com/video/2103099/),
# distributed under the Pexels license (free to use). Downloaded once and
# cached next to the sample.
SAMPLE_VIDEO_URL = (
    "https://videos.pexels.com/video-files/2103099/2103099-hd_1280_720_60fps.mp4"
)


class LatencyEstimator:
    """RFC 6298-style smoothed RTT used to size the display jitter buffer."""

    def __init__(self) -> None:
        self.srtt_ms: float | None = None
        self.rttvar_ms = 0.0

    def update(self, rtt_ms: float) -> None:
        if self.srtt_ms is None:
            self.srtt_ms = rtt_ms
            self.rttvar_ms = rtt_ms / 2
        else:
            self.rttvar_ms += 0.25 * (abs(self.srtt_ms - rtt_ms) - self.rttvar_ms)
            self.srtt_ms += 0.125 * (rtt_ms - self.srtt_ms)

    def target_latency_ms(self) -> float:
        if self.srtt_ms is None:
            return 100.0
        return min(max(self.srtt_ms + 4 * self.rttvar_ms, 50.0), 500.0)


class RateCounter:
    def __init__(self) -> None:
        self.count = 0.0
        self.window_start = time.monotonic()
        self.value = 0.0

    def tick(self, amount: float = 1.0) -> None:
        self.count += amount
        now = time.monotonic()
        elapsed = now - self.window_start
        if elapsed >= 1.0:
            self.value = self.count / elapsed
            self.count = 0.0
            self.window_start = now


class DeviceState:
    """Shared state between the capture, inference, render, and viewer sides."""

    def __init__(self) -> None:
        self.frames: OrderedDict[int, tuple[float, np.ndarray]] = OrderedDict()
        self.next_frame_id = 0
        self.frame_event = asyncio.Event()
        self.detections: OrderedDict[int, tuple[float, sv.Detections]] = OrderedDict()
        self.annotated: tuple[bytes, float] | None = None  # (jpeg, capture_ts)
        self.annotated_event = asyncio.Event()
        self.workers: dict[str, str] = {}
        self.worker_labels: dict[str, dict[str, str]] = {}
        self.workers_event = asyncio.Event()
        self.cooldown: dict[str, float] = {}
        self.detection_enabled = True
        self.detection_event = asyncio.Event()
        self.detection_event.set()
        self.resume_on_capacity = False
        self.pinned_worker: str | None = None
        self.device_name = "device"
        self.codec = "jpeg"
        self.quality = 80
        self.display_quality = 70
        self.latency = LatencyEstimator()
        self.fps_source = RateCounter()
        self.fps_detect = RateCounter()
        self.uplink = RateCounter()
        self.tracker = ByteTrackTracker()
        self.class_ids: dict[str, int] = {}
        self.status: dict[str, object] = {"worker": None, "workers": []}
        self.subscribers: set[asyncio.Queue[str]] = set()

    def add_frame(self, frame: np.ndarray) -> int:
        frame_id = self.next_frame_id
        self.next_frame_id += 1
        self.frames[frame_id] = (time.monotonic(), frame)
        while len(self.frames) > FRAME_BUFFER_SIZE:
            self.frames.popitem(last=False)
        self.fps_source.tick()
        self.frame_event.set()
        self.frame_event = asyncio.Event()
        return frame_id

    def class_id(self, label: str) -> int:
        if label not in self.class_ids:
            self.class_ids[label] = len(self.class_ids)
        return self.class_ids[label]

    def pick_workers(self, count: int) -> list[str]:
        """Rendezvous-ordered candidates: each device prefers different workers,
        with minimal reshuffling when the pool changes."""
        now = time.monotonic()
        candidates = [
            name for name in self.workers if now >= self.cooldown.get(name, 0.0)
        ]
        candidates.sort(
            key=lambda name: hashlib.sha256(
                f"{self.device_name}|{name}".encode()
            ).digest(),
            reverse=True,
        )
        return candidates[:count]

    def worker_available(self, name: str) -> bool:
        return name in self.workers and time.monotonic() >= self.cooldown.get(
            name, 0.0
        )

    def select_candidates(self) -> list[str]:
        """Honor a user pin when the pinned worker is available, otherwise fall
        back to automatic selection while keeping the pin, so the session snaps
        back as soon as the worker returns."""
        if self.pinned_worker is not None and self.worker_available(
            self.pinned_worker
        ):
            return [self.pinned_worker]
        return self.pick_workers(2)

    def publish(self, update: dict[str, object]) -> None:
        self.status.update(update)
        self.status["workers"] = [
            {
                "name": name,
                "accelerator": self.worker_labels.get(name, {}).get("accelerator"),
                "model": self.worker_labels.get(name, {}).get("model"),
                "device": self.worker_labels.get(name, {}).get("device"),
            }
            for name in sorted(self.workers)
        ]
        self.status["detection_enabled"] = self.detection_enabled
        self.status["auto_suspended"] = self.resume_on_capacity
        self.status["pinned_worker"] = self.pinned_worker
        payload = json.dumps(self.status)
        for queue in tuple(self.subscribers):
            if queue.full():
                with suppress(asyncio.QueueEmpty):
                    queue.get_nowait()
            queue.put_nowait(payload)

    def clear_session_stats(self) -> None:
        self.publish(
            {
                "worker": None,
                "fps_detect": None,
                "infer_ms": None,
                "network_ms": None,
                "buffer_ms": None,
                "uplink_kbps": None,
                "detections": [],
            }
        )


def ensure_source(source: str) -> str:
    if source != "sample":
        return source
    cache = Path(__file__).parent / ".cache"
    cache.mkdir(exist_ok=True)
    target = cache / "highway-traffic.mp4"
    if target.exists():
        return str(target)
    try:
        print("Downloading the sample highway clip (Pexels, free license)…", flush=True)
        urllib.request.urlretrieve(SAMPLE_VIDEO_URL, target)
        return str(target)
    except OSError as error:
        print(f"Sample video unavailable ({error}); using the synthetic source.")
        return "synthetic"


def _fit_display(frame: np.ndarray) -> np.ndarray:
    height, width = frame.shape[:2]
    if width <= DISPLAY_MAX_WIDTH:
        return frame
    scale = DISPLAY_MAX_WIDTH / width
    return cv2.resize(frame, (DISPLAY_MAX_WIDTH, round(height * scale)))


def _synthetic_frame(tick: int) -> np.ndarray:
    frame = np.full((480, 854, 3), 235, dtype=np.uint8)
    x = 80 + int(320 * (0.5 + 0.5 * np.sin(tick / 18)))
    y = 90 + int(180 * (0.5 + 0.5 * np.cos(tick / 23)))
    cv2.circle(frame, (x + 120, y + 90), 70, (90, 90, 100), -1)
    cv2.rectangle(frame, (x, y), (x + 240, y + 180), (60, 60, 70), 3)
    return frame


async def capture_loop(state: DeviceState, source: str, fps_override: float) -> None:
    loop = asyncio.get_running_loop()
    if source == "synthetic":
        tick = 0
        fps = fps_override or 15.0
        while True:
            state.add_frame(_synthetic_frame(tick))
            tick += 1
            await asyncio.sleep(1.0 / fps)
    capture = cv2.VideoCapture(int(source) if source.isdigit() else source)
    if not capture.isOpened():
        raise SystemExit(f"failed to open capture source {source!r}")
    is_file = not source.isdigit()
    fps = fps_override or (capture.get(cv2.CAP_PROP_FPS) if is_file else 0.0) or 25.0
    interval = 1.0 / fps
    next_tick = time.monotonic()
    while True:
        ok, frame = await loop.run_in_executor(None, capture.read)
        if not ok:
            if is_file:
                capture.set(cv2.CAP_PROP_POS_FRAMES, 0)
                continue
            raise SystemExit("capture source ended")
        state.add_frame(_fit_display(frame))
        next_tick += interval
        delay = next_tick - time.monotonic()
        if delay > 0:
            await asyncio.sleep(delay)
        else:
            next_tick = time.monotonic()


async def registry_loop(state: DeviceState, client: rstream.Client) -> None:
    """Keep the worker pool current; survive losing the watch connection.

    Each attempt re-seeds the pool from the inventory, then follows the
    real-time watch. While the watch is down the last known pool stays in
    place, since a stale pool degrades into dial failures and cooldowns,
    which the inference loop already handles.
    """
    backoff = 1.0
    while True:
        try:
            # Seed the pool from the inventory: list_tunnels filtered by label
            # is a point-in-time snapshot of the registry.
            state.workers.clear()
            state.worker_labels.clear()
            for tunnel in await client.list_tunnels(filters=WORKER_FILTERS):
                name = tunnel.properties.name
                if name and tunnel.status == "online":
                    state.workers[name] = tunnel.status
                    state.worker_labels[name] = dict(tunnel.properties.labels)
            state.workers_event.set()
            _apply_capacity_transition(state)
            state.publish({})
            backoff = 1.0
            # watch streams the same registry live: one event per worker
            # arrival or departure, so the pool stays current with no polling.
            async with client.watch(tunnels=WORKER_FILTERS) as events:
                async for event in events:
                    obj = event.object or {}
                    name = obj.get("name")
                    if not isinstance(name, str):
                        continue
                    if event.type == "tunnel.deleted" or obj.get("status") == "offline":
                        state.workers.pop(name, None)
                        state.worker_labels.pop(name, None)
                    elif isinstance(obj.get("labels"), dict):
                        state.workers[name] = str(obj.get("status", "online"))
                        state.worker_labels[name] = {
                            str(k): str(v) for k, v in obj["labels"].items()
                        }
                        state.cooldown.pop(name, None)
                    state.workers_event.set()
                    _apply_capacity_transition(state)
                    state.publish({})
            raise ConnectionError("watch stream ended")
        except Exception as error:
            print(
                f"registry watch lost ({error!r}); retrying in {backoff:.0f}s",
                flush=True,
            )
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, 15.0)


def _apply_capacity_transition(state: DeviceState) -> None:
    """Suspend detection while the pool is empty, resume it when capacity returns.

    A user decision always wins: explicit enable/disable clears the pending
    resume, so only pool-driven suspensions auto-resume.
    """
    if not state.workers and state.detection_enabled:
        state.detection_enabled = False
        state.resume_on_capacity = True
        state.detection_event.clear()
    elif state.workers and state.resume_on_capacity:
        state.resume_on_capacity = False
        state.detection_enabled = True
        state.detection_event.set()


def _encode_for_worker(
    frame: np.ndarray, input_size: int, codec: str, quality: int
) -> tuple[bytes, float]:
    height, width = frame.shape[:2]
    scale = max(width, height) / float(input_size)
    if scale > 1.0:
        frame = cv2.resize(frame, (round(width / scale), round(height / scale)))
    else:
        scale = 1.0
    params: list[int] = []
    if codec == "jpeg":
        params = [cv2.IMWRITE_JPEG_QUALITY, quality]
    elif codec == "webp":
        params = [cv2.IMWRITE_WEBP_QUALITY, quality]
    ok, encoded = cv2.imencode(CODECS[codec], frame, params)
    if not ok:
        raise ValueError(f"failed to encode frame with codec {codec}")
    return encoded.tobytes(), scale


def _to_tracked_detections(
    state: DeviceState, raw: list[dict[str, object]], scale: float
) -> sv.Detections:
    if not raw:
        detections = sv.Detections.empty()
    else:
        detections = sv.Detections(
            xyxy=np.array([d["box"] for d in raw], dtype=float) * scale,
            confidence=np.array([d["confidence"] for d in raw], dtype=float),
            class_id=np.array([state.class_id(str(d["label"])) for d in raw]),
        )
    return state.tracker.update(detections)


async def open_session(
    client: rstream.Client, worker: str
) -> tuple[rstream.RstreamStream, dict[str, object]]:
    """Dial a worker and read its hello, cleaning up on any failure."""
    # dial opens a bytestream to the worker's private tunnel by name. rstream
    # routes it through the engine; the device never learns the worker's IP or
    # port, and the worker exposes neither.
    stream = await client.dial(worker)
    try:
        hello = await asyncio.wait_for(read_message(stream), RESPONSE_TIMEOUT)
        if hello is None or hello[0].get("type") != "hello":
            raise ConnectionError("worker did not send a hello")
        return stream, hello[0]
    except BaseException:
        with suppress(Exception):
            stream.close()
            await stream.wait_closed()
        raise


async def run_session(
    state: DeviceState,
    stream: rstream.RstreamStream,
    header: dict[str, object],
    codec: str,
    quality: int,
) -> None:
    loop = asyncio.get_running_loop()
    input_size = int(header.get("input_size", 640))
    offered = header.get("codecs")
    if isinstance(offered, list) and codec not in offered:
        codec = "jpeg"
    state.publish(
        {
            "worker": header.get("worker"),
            "model": header.get("model"),
            "accelerator": header.get("accelerator"),
            "input_size": input_size,
            "codec": codec,
            "state": "connected",
        }
    )
    in_flight: dict[int, tuple[float, float]] = {}
    in_flight_slots = asyncio.Semaphore(MAX_IN_FLIGHT)
    last_publish = 0.0

    async def sender() -> None:
        while True:
            await in_flight_slots.acquire()
            while True:
                event = state.frame_event
                if state.frames:
                    frame_id, (_, frame) = next(reversed(state.frames.items()))
                    if frame_id not in in_flight:
                        break
                await event.wait()
            payload, scale = await loop.run_in_executor(
                None, _encode_for_worker, frame, input_size, codec, quality
            )
            in_flight[frame_id] = (time.monotonic(), scale)
            await send_message(
                stream, {"type": "frame", "frame_id": frame_id, "codec": codec}, payload
            )
            state.uplink.tick(len(payload))

    send_task = asyncio.create_task(sender())
    try:
        while True:
            message = await asyncio.wait_for(read_message(stream), RESPONSE_TIMEOUT)
            if message is None:
                raise ConnectionError("worker closed the session")
            header, _ = message
            if header.get("type") != "result":
                continue
            frame_id = int(header.get("frame_id", -1))
            sent = in_flight.pop(frame_id, None)
            in_flight_slots.release()
            if sent is None:
                continue
            sent_at, scale = sent
            rtt_ms = (time.monotonic() - sent_at) * 1000.0
            infer_ms = float(header.get("infer_ms", 0.0))
            state.latency.update(rtt_ms)
            raw = header.get("detections")
            tracked = _to_tracked_detections(
                state, raw if isinstance(raw, list) else [], scale
            )
            state.detections[frame_id] = (time.monotonic(), tracked)
            while len(state.detections) > FRAME_BUFFER_SIZE:
                state.detections.popitem(last=False)
            state.fps_detect.tick()
            if time.monotonic() - last_publish < 0.25:
                continue
            last_publish = time.monotonic()
            state.publish(
                {
                    "fps_detect": round(state.fps_detect.value, 1),
                    "infer_ms": round(infer_ms, 1),
                    "network_ms": round(max(rtt_ms - infer_ms, 0.0)),
                    "buffer_ms": round(state.latency.target_latency_ms()),
                    "uplink_kbps": round(state.uplink.value * 8 / 1000),
                    "detections": [
                        {
                            "label": name,
                            "count": int(count),
                        }
                        for name, count in _class_counts(state, tracked)
                    ],
                }
            )
    finally:
        send_task.cancel()
        await asyncio.gather(send_task, return_exceptions=True)


def _class_counts(state: DeviceState, tracked: sv.Detections) -> list[tuple[str, int]]:
    names = {v: k for k, v in state.class_ids.items()}
    counts: dict[str, int] = {}
    if tracked.class_id is not None:
        for cid in tracked.class_id:
            label = names.get(int(cid), "?")
            counts[label] = counts.get(label, 0) + 1
    return sorted(counts.items())


async def inference_loop(state: DeviceState, client: rstream.Client) -> None:
    while True:
        if not state.detection_enabled:
            state.clear_session_stats()
            state.publish(
                {
                    "state": "waiting for workers"
                    if state.resume_on_capacity
                    else "disabled"
                }
            )
            await state.detection_event.wait()
            continue
        candidates = state.select_candidates()
        if not candidates:
            state.clear_session_stats()
            state.publish(
                {"state": "reconnecting" if state.workers else "waiting for workers"}
            )
            state.workers_event.clear()
            with suppress(TimeoutError):
                await asyncio.wait_for(state.workers_event.wait(), 0.5)
            continue
        # Power of two choices: ask both candidates, keep the lighter one.
        # Dials are cheap, so balancing costs one extra hello.
        opened: list[tuple[str, rstream.RstreamStream, dict[str, object]]] = []
        for name in candidates:
            try:
                opened.append((name, *await open_session(client, name)))
            except Exception as error:
                print(f"dial to {name} failed: {error!r}", flush=True)
                state.cooldown[name] = time.monotonic() + DIAL_COOLDOWN
        if not opened:
            continue
        opened.sort(key=lambda item: int(item[2].get("active_sessions", 0)))
        worker, stream, hello = opened[0]
        for _, other_stream, _ in opened[1:]:
            with suppress(Exception):
                other_stream.close()
                await other_stream.wait_closed()
        session = asyncio.create_task(
            run_session(state, stream, hello, state.codec, state.quality)
        )
        toggle = asyncio.create_task(_wait_disabled(state))
        repin = asyncio.create_task(_wait_repin(state, worker))
        done, pending = await asyncio.wait(
            {session, toggle, repin}, return_when=asyncio.FIRST_COMPLETED
        )
        for task in pending:
            task.cancel()
        await asyncio.gather(*pending, return_exceptions=True)
        with suppress(Exception):
            # Closing a stream mid-traffic can surface TLS shutdown noise;
            # none of it is a worker fault.
            stream.close()
            await stream.wait_closed()
        if session not in done:
            # The user disabled detection or pinned another worker: a clean
            # teardown, reselect on the next pass, not a failure.
            continue
        try:
            session.result()
        except (
            TimeoutError,
            ConnectionError,
            OSError,
            ValueError,
            rstream.RstreamError,
        ) as error:
            if state.detection_enabled:
                print(f"session with {worker} failed: {error!r}", flush=True)
            state.cooldown[worker] = time.monotonic() + SESSION_COOLDOWN
            state.publish({"state": "reconnecting"})


async def _wait_disabled(state: DeviceState) -> None:
    while state.detection_enabled:
        await asyncio.sleep(0.1)


async def _wait_repin(state: DeviceState, current: str) -> None:
    """Return once the user has pinned a different, available worker, so the
    loop can switch to it. Unpinning never forces a switch."""
    while True:
        target = state.pinned_worker
        if target is not None and target != current and state.worker_available(
            target
        ):
            return
        await asyncio.sleep(0.1)


async def render_loop(state: DeviceState) -> None:
    loop = asyncio.get_running_loop()
    box_annotator = sv.BoxAnnotator(color_lookup=sv.ColorLookup.CLASS, thickness=2)
    label_annotator = sv.LabelAnnotator(
        color_lookup=sv.ColorLookup.CLASS, text_scale=0.5
    )
    fps_display = RateCounter()
    last_stats = 0.0
    last_display_id = -1
    interval = 1.0 / DISPLAY_FPS
    next_tick = time.monotonic()
    while True:
        # Absolute-clock pacing keeps the cadence even; sleep(interval) alone
        # would drift by the render time of every tick.
        next_tick += interval
        delay = next_tick - time.monotonic()
        if delay > 0:
            await asyncio.sleep(delay)
        else:
            next_tick = time.monotonic()
        if not state.frames:
            continue
        # The display delay is a synchronization buffer, not a smoothing one:
        # it holds each frame just long enough for its own detections to come
        # back, so boxes land on the exact frames they were computed for.
        # Smoothing against network jitter is the browser player's job. With
        # detection off there is nothing to align, so frames show immediately.
        target_s = (
            state.latency.target_latency_ms() / 1000.0
            if state.detection_enabled
            else 0.0
        )
        now = time.monotonic()
        display_id = None
        for frame_id, (ts, _) in reversed(state.frames.items()):
            if now - ts >= target_s:
                display_id = frame_id
                break
        if display_id is None:
            display_id = next(iter(state.frames))
        if display_id <= last_display_id:
            # The adaptive latency target just grew: rather than stepping
            # backwards in time, hold the current frame.
            continue
        last_display_id = display_id
        ts, frame = state.frames[display_id]
        overlay: sv.Detections | None = None
        for det_id, (det_ts, det) in reversed(state.detections.items()):
            if det_id <= display_id:
                if now - det_ts <= FRESHNESS_LIMIT and state.detection_enabled:
                    overlay = det
                break
        annotated = await loop.run_in_executor(
            None, _render, state, frame, overlay, box_annotator, label_annotator
        )
        if annotated:
            state.annotated = (annotated, ts)
            state.annotated_event.set()
            state.annotated_event = asyncio.Event()
        fps_display.tick()
        now = time.monotonic()
        if now - last_stats >= 1.0:
            last_stats = now
            state.publish({"fps_source": round(state.fps_source.value, 1)})


def _render(
    state: DeviceState,
    frame: np.ndarray,
    overlay: sv.Detections | None,
    box_annotator: sv.BoxAnnotator,
    label_annotator: sv.LabelAnnotator,
) -> bytes:
    canvas = frame.copy()
    if overlay is not None and len(overlay) > 0:
        names = {v: k for k, v in state.class_ids.items()}
        labels = [
            f"{names.get(int(cid), '?')} #{tid}"
            for cid, tid in zip(overlay.class_id, overlay.tracker_id, strict=False)
        ]
        canvas = box_annotator.annotate(scene=canvas, detections=overlay)
        canvas = label_annotator.annotate(
            scene=canvas, detections=overlay, labels=labels
        )
    ok, jpeg = cv2.imencode(
        ".jpg", canvas, [cv2.IMWRITE_JPEG_QUALITY, state.display_quality]
    )
    return jpeg.tobytes() if ok else b""


def build_app(state: DeviceState) -> FastAPI:
    app = FastAPI(title="python-vision-inference")

    no_cache = {"Cache-Control": "no-store"}

    @app.get("/")
    async def index() -> HTMLResponse:
        return HTMLResponse((WEB_DIR / "index.html").read_text(), headers=no_cache)

    @app.get("/app.css")
    async def stylesheet() -> Response:
        css = (WEB_DIR / "generated" / "app.css").read_bytes()
        return Response(css, media_type="text/css", headers=no_cache)

    @app.get("/app.js")
    async def script() -> Response:
        return Response(
            (WEB_DIR / "generated" / "app.js").read_bytes(),
            media_type="text/javascript",
            headers=no_cache,
        )

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        return {"status": "healthy"}

    @app.post("/detection")
    async def set_detection(payload: dict[str, object]) -> dict[str, bool]:
        enabled = bool(payload.get("enabled", True))
        state.resume_on_capacity = False
        if enabled != state.detection_enabled:
            state.detection_enabled = enabled
            if enabled:
                state.detection_event.set()
                state.publish({"state": "connecting"})
            else:
                state.detection_event.clear()
                state.publish({"state": "disabling"})
        return {"detection_enabled": state.detection_enabled}

    @app.post("/pin")
    async def set_pin(payload: dict[str, object]) -> dict[str, str | None]:
        # A worker name pins the session to it; null returns to automatic
        # selection. The pin is advisory: the inference loop falls back to
        # auto when the pinned worker is unavailable and snaps back when it
        # returns.
        worker = payload.get("worker")
        state.pinned_worker = worker if isinstance(worker, str) and worker else None
        state.publish({})
        return {"pinned_worker": state.pinned_worker}

    @app.get("/stream")
    async def stream() -> StreamingResponse:
        async def frames():  # type: ignore[no-untyped-def]
            # Adaptive quality: each yield returns once the chunk has drained
            # to the viewer, so the time between yields measures the path.
            # When the link cannot keep up, lower the encode quality instead
            # of letting frames queue into bursts.
            budget = 1.0 / DISPLAY_FPS
            healthy_iterations = 0
            last_yield = time.monotonic()
            while True:
                event = state.annotated_event
                await event.wait()
                annotated = state.annotated
                if annotated is None:
                    continue
                jpeg, capture_ts = annotated
                yield (
                    b"X-Frame-Ts: "
                    + str(round(capture_ts * 1000)).encode()
                    + b"\r\nContent-Length: "
                    + str(len(jpeg)).encode()
                    + b"\r\n\r\n"
                    + jpeg
                )
                now = time.monotonic()
                elapsed = now - last_yield
                last_yield = now
                if elapsed > budget * 1.5:
                    state.display_quality = max(45, state.display_quality - 5)
                    healthy_iterations = 0
                else:
                    healthy_iterations += 1
                    if healthy_iterations >= int(DISPLAY_FPS * 3):
                        state.display_quality = min(85, state.display_quality + 5)
                        healthy_iterations = 0

        # A deliberately neutral content type. multipart/x-mixed-replace is
        # special-cased by WebKit's network layer (it predates fetch), and
        # reading it through fetch() fails in Safari with no console error.
        # The viewer parses the per-frame headers itself, so nothing standard
        # is lost by not claiming multipart.
        return StreamingResponse(
            frames(), media_type="application/octet-stream", headers=no_cache
        )

    @app.get("/events")
    async def events() -> StreamingResponse:
        async def generate():  # type: ignore[no-untyped-def]
            queue: asyncio.Queue[str] = asyncio.Queue(maxsize=8)
            state.subscribers.add(queue)
            queue.put_nowait(json.dumps(state.status))
            try:
                while True:
                    payload = await queue.get()
                    yield f"data: {payload}\n\n"
            finally:
                state.subscribers.discard(queue)

        return StreamingResponse(generate(), media_type="text/event-stream")

    return app


async def viewer_loop(
    state: DeviceState,
    app: FastAPI,
    client: rstream.Client,
    name: str,
    host: str | None,
    token_auth: bool,
) -> None:
    """Publish the viewer and serve it; recreate the tunnel if the engine
    connection drops. ``host`` is resolved once by the caller (explicit or a
    generated stable domain) and reused on every recreation, so the viewer
    address stays constant across reconnects.
    """
    backoff = 1.0
    while True:
        try:
            async with await client.connect() as control:
                viewer = await control.create_tunnel(
                    name=f"{name}-viewer",
                    protocol="http",
                    http_version="http/1.1",
                    # Published: the engine mints a public HTTPS URL for
                    # standard browsers, optionally gated by an edge token.
                    publish=True,
                    hostname=host,
                    auth=rstream.TunnelAuth(token=True) if token_auth else None,
                    labels={"role": "viewer", "device": name},
                )
                print("Viewer address:", viewer.forwarding_address, flush=True)
                backoff = 1.0
                # Serve the ASGI app straight onto accepted tunnel streams.
                # There is no local web server or loopback port; rstream hands
                # each HTTP request to the app in-process.
                await rstream.asgi.serve(app, viewer)
            raise ConnectionError("viewer tunnel closed")
        except Exception as error:
            print(
                f"viewer tunnel lost ({error!r}); retrying in {backoff:.0f}s",
                flush=True,
            )
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, 15.0)


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--name", default="vision-device", help="device name")
    parser.add_argument(
        "--source",
        default="sample",
        help="capture source: 'sample' (downloaded traffic clip), 'synthetic', "
        "a camera index, or a video path",
    )
    parser.add_argument("--fps", type=float, default=0.0, help="capture rate override")
    parser.add_argument(
        "--codec",
        default="jpeg",
        choices=sorted(CODECS),
        help="encoding for frames sent to workers (png is lossless)",
    )
    parser.add_argument(
        "--quality", type=int, default=80, help="lossy codec quality (1-100)"
    )
    parser.add_argument(
        "--token-auth",
        action="store_true",
        help="require an rstream token at the edge of the viewer endpoint",
    )
    parser.add_argument(
        "--host",
        default=None,
        help="explicit public hostname for the viewer (project-scoped, e.g. "
        "vision-<project>.t.<engine-host>); by default a stable one is "
        "generated so the address survives restarts",
    )
    args = parser.parse_args()
    state = DeviceState()
    state.device_name = args.name
    state.codec = args.codec
    state.quality = args.quality
    source = ensure_source(args.source)
    # One client for the whole device: from_env resolves the project and
    # credentials from the rstream CLI configuration. The same client serves
    # the viewer, dials workers, and watches the registry.
    async with rstream.Client.from_env() as client:
        # Resolve the viewer hostname once: an explicit --host, otherwise a
        # generated stable domain. Reusing it on every reconnect is what keeps
        # the address constant, so the generation must happen here, not inside
        # the loop that recreates the tunnel.
        host = args.host or await client.generate_stable_hostname()
        await asyncio.gather(
            capture_loop(state, source, args.fps),
            registry_loop(state, client),
            inference_loop(state, client),
            render_loop(state),
            viewer_loop(
                state, build_app(state), client, args.name, host, args.token_auth
            ),
        )


if __name__ == "__main__":
    with suppress(KeyboardInterrupt):
        asyncio.run(main())
