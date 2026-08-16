# See LICENSE file in the project root for license information.

"""Inference worker: serve a YOLO model to remote devices over a private tunnel.

The worker runs wherever the compute is. It creates a private tunnel labeled
``role=inference`` and serves sessions: each session opens with a ``hello``
advertising the model, its input size, and the supported codecs, then answers
encoded frames with JSON detections.

Workers are deliberately stateless, pure frame-to-detections functions. All
session state, tracking, and display logic live on the device, which is what
makes pool members interchangeable and failover invisible.
"""

from __future__ import annotations

import argparse
import asyncio
import math
import platform
import secrets
import ssl
import subprocess
import sys
import time
from contextlib import suppress
from dataclasses import dataclass
from pathlib import Path

import cv2
import numpy as np
from ultralytics import YOLO

import rstream

ROOT_DIR = Path(__file__).resolve().parents[1]
if str(ROOT_DIR) not in sys.path:
    sys.path.insert(0, str(ROOT_DIR))

from shared.protocol import read_message, send_message

SUPPORTED_CODECS = ("jpeg", "webp", "png")
MAX_SESSION_LIMIT = 1024
MAX_INPUT_SIZE = 8192
CONTROL_TIMEOUT = 15.0
SESSION_IDLE_TIMEOUT = 30.0


class InvalidFrameError(ValueError):
    """The device sent a frame that cannot be processed safely."""


def _default_worker_name() -> str:
    """Return a collision-resistant name without exposing the host identity."""
    return f"worker-{secrets.token_hex(4)}"


def select_device(override: str | None = None) -> tuple[str, str]:
    """Pick the inference device and a human label for it.

    Returns ``(device, accelerator)`` where ``device`` is what ultralytics
    expects (``cuda:0``, ``mps``, ``cpu``) and ``accelerator`` is a display
    name. ultralytics defaults to the CPU even when a GPU is present, so the
    worker selects explicitly and advertises the result through its labels,
    making "laptop CPU versus lab GPU" visible from any viewer. ``override``
    forces a device, which is handy for running a CPU worker and a GPU worker
    side by side from the same machine.
    """
    if override is not None:
        return override, _accelerator_name(override)
    try:
        import torch

        if torch.cuda.is_available():
            return "cuda:0", _accelerator_name("cuda:0")
        if torch.backends.mps.is_available():
            return "mps", _accelerator_name("mps")
    except Exception:
        pass
    return "cpu", _cpu_brand()


def _accelerator_name(device: str) -> str:
    if device.startswith("cuda"):
        with suppress(Exception):
            import torch

            index = int(device.split(":", 1)[1]) if ":" in device else 0
            return torch.cuda.get_device_name(index)
        return "CUDA GPU"
    # Apple Silicon shares one SoC, so the GPU is named by the chip.
    return _cpu_brand()


def _cpu_brand() -> str:
    system = platform.system()
    if system == "Darwin":
        brand = _run(["sysctl", "-n", "machdep.cpu.brand_string"])
        if brand:
            return brand
    elif system == "Linux":
        with suppress(OSError):
            for line in Path("/proc/cpuinfo").read_text().splitlines():
                if line.startswith("model name"):
                    return line.split(":", 1)[1].strip()
    return platform.processor() or platform.machine() or "CPU"


def _run(command: list[str]) -> str:
    with suppress(OSError, subprocess.SubprocessError):
        result = subprocess.run(
            command, capture_output=True, text=True, timeout=2, check=False
        )
        return result.stdout.strip()
    return ""


class SessionAdmission:
    """Bound concurrent sessions without blocking accepted peers in a queue."""

    def __init__(self, limit: int) -> None:
        if not 1 <= limit <= MAX_SESSION_LIMIT:
            raise ValueError(f"session limit must be in [1,{MAX_SESSION_LIMIT}]")
        self.limit = limit
        self._active = 0
        self._lock = asyncio.Lock()

    async def try_acquire(self) -> int | None:
        """Reserve a slot and return the load observed before this session."""
        async with self._lock:
            if self._active >= self.limit:
                return None
            already_serving = self._active
            self._active += 1
            return already_serving

    async def release(self) -> None:
        async with self._lock:
            if self._active == 0:
                raise RuntimeError("session admission release without acquisition")
            self._active -= 1

    async def active(self) -> int:
        async with self._lock:
            return self._active


def _detect(
    model: YOLO, payload: bytes, input_size: int, conf: float, device: str
) -> tuple[list[dict[str, object]], float]:
    frame = cv2.imdecode(np.frombuffer(payload, dtype=np.uint8), cv2.IMREAD_COLOR)
    if frame is None:
        raise InvalidFrameError("payload is not a supported encoded image")
    started = time.perf_counter()
    results = model.predict(
        frame, imgsz=input_size, conf=conf, device=device, verbose=False
    )
    elapsed_ms = (time.perf_counter() - started) * 1000.0
    detections: list[dict[str, object]] = []
    for result in results:
        names = result.names
        for box in result.boxes:
            x1, y1, x2, y2 = (float(v) for v in box.xyxy[0].tolist())
            detections.append(
                {
                    "box": [x1, y1, x2, y2],
                    "label": names[int(box.cls[0])],
                    "confidence": round(float(box.conf[0]), 3),
                }
            )
    return detections, elapsed_ms


def _validate_frame(header: dict[str, object], payload: bytes) -> int:
    if header.get("type") != "frame":
        raise InvalidFrameError("expected a frame message")
    frame_id = header.get("frame_id")
    if (
        not isinstance(frame_id, int)
        or isinstance(frame_id, bool)
        or not 0 <= frame_id <= (1 << 63) - 1
    ):
        raise InvalidFrameError("frame_id must be an unsigned 63-bit integer")
    codec = header.get("codec")
    if codec not in SUPPORTED_CODECS:
        raise InvalidFrameError(f"unsupported frame codec {codec!r}")
    if not payload:
        raise InvalidFrameError("frame payload must not be empty")
    return frame_id


@dataclass(frozen=True)
class WorkerConfig:
    name: str
    model: str
    input_size: int
    conf: float
    device: str
    accelerator: str


class InferenceRunner:
    """Serialize access to one model without violating it on cancellation."""

    def __init__(self, model: YOLO, config: WorkerConfig) -> None:
        self.model = model
        self.config = config
        self._lock = asyncio.Lock()

    async def run(self, payload: bytes) -> tuple[list[dict[str, object]], float]:
        """Run one call and keep the model locked until its thread has stopped.

        Python cannot stop a function already running in an executor thread.
        If the session is cancelled, the inner future is shielded and joined
        before leaving the model lock. Otherwise another session could enter
        YOLO while the cancelled call still uses the same model/GPU state.
        """
        loop = asyncio.get_running_loop()
        async with self._lock:
            future = loop.run_in_executor(
                None,
                _detect,
                self.model,
                payload,
                self.config.input_size,
                self.config.conf,
                self.config.device,
            )
            try:
                return await asyncio.shield(future)
            except asyncio.CancelledError:
                with suppress(Exception):
                    await future
                raise


async def serve_session(
    stream: rstream.RstreamStream,
    inference: InferenceRunner,
    admission: SessionAdmission,
    already_serving: int,
    engine_region: str,
) -> None:
    config = inference.config
    print("device session opened", flush=True)
    try:
        async with stream:
            await send_message(
                stream,
                {
                    "type": "hello",
                    "worker": config.name,
                    "model": config.model,
                    "input_size": config.input_size,
                    "codecs": list(SUPPORTED_CODECS),
                    "active_sessions": already_serving,
                    "max_sessions": admission.limit,
                    "device": config.device,
                    "accelerator": config.accelerator,
                    "engine_region": engine_region,
                },
            )
            while True:
                try:
                    message = await asyncio.wait_for(
                        read_message(stream),
                        SESSION_IDLE_TIMEOUT,
                    )
                except TimeoutError:
                    await asyncio.wait_for(
                        send_message(
                            stream,
                            {
                                "type": "error",
                                "code": "idle_timeout",
                                "message": "device stopped sending frames",
                            },
                        ),
                        timeout=1.0,
                    )
                    return
                if message is None:
                    return
                header, payload = message
                try:
                    frame_id = _validate_frame(header, payload)
                    detections, infer_ms = await inference.run(payload)
                except InvalidFrameError as error:
                    await send_message(
                        stream,
                        {
                            "type": "error",
                            "code": "invalid_frame",
                            "message": str(error),
                        },
                    )
                    return
                await send_message(
                    stream,
                    {
                        "type": "result",
                        "frame_id": frame_id,
                        "infer_ms": round(infer_ms, 1),
                        "detections": detections,
                    },
                )
    except (ConnectionError, ssl.SSLError, OSError):
        # The device hung up mid-exchange, which is how sessions end when
        # detection is disabled or the device fails over. A normal goodbye.
        return
    finally:
        await admission.release()
        print("device session closed", flush=True)


async def reject_session(stream: rstream.RstreamStream) -> None:
    """Reject an excess session without allowing an unbounded waiting queue."""
    try:
        async with stream:
            await asyncio.wait_for(
                send_message(
                    stream,
                    {
                        "type": "error",
                        "code": "at_capacity",
                        "message": "worker has reached its session capacity",
                    },
                ),
                timeout=1.0,
            )
    except (TimeoutError, ConnectionError, ssl.SSLError, OSError):
        return


def _task_finished(task: asyncio.Task[None], tasks: set[asyncio.Task[None]]) -> None:
    tasks.discard(task)
    if task.cancelled():
        return
    error = task.exception()
    if error is not None:
        print(f"device session failed: {error!r}", flush=True)


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--name", default=_default_worker_name(), help="worker tunnel name"
    )
    parser.add_argument("--model", default="yolov8n.pt", help="ultralytics model")
    parser.add_argument("--imgsz", type=int, default=640, help="model input size")
    parser.add_argument(
        "--conf", type=float, default=0.4, help="confidence threshold"
    )
    parser.add_argument(
        "--max-sessions",
        type=int,
        default=4,
        help="maximum concurrent device sessions (default: 4)",
    )
    parser.add_argument(
        "--device",
        default=None,
        help="force the inference device (cpu, mps, cuda:0); auto-selected by default",
    )
    args = parser.parse_args()
    if not 32 <= args.imgsz <= MAX_INPUT_SIZE:
        parser.error(f"--imgsz must be in [32,{MAX_INPUT_SIZE}]")
    if not math.isfinite(args.conf) or not 0 <= args.conf <= 1:
        parser.error("--conf must be finite and in [0,1]")
    if not 1 <= args.max_sessions <= MAX_SESSION_LIMIT:
        parser.error(f"--max-sessions must be in [1,{MAX_SESSION_LIMIT}]")
    device, accelerator = select_device(args.device)
    config = WorkerConfig(
        name=args.name,
        model=Path(args.model).name,
        input_size=args.imgsz,
        conf=args.conf,
        device=device,
        accelerator=accelerator,
    )
    admission = SessionAdmission(args.max_sessions)
    model = YOLO(args.model)
    model.predict(
        np.zeros((args.imgsz, args.imgsz, 3), dtype=np.uint8),
        imgsz=args.imgsz,
        device=device,
        verbose=False,
    )
    inference = InferenceRunner(model, config)
    tasks: set[asyncio.Task[None]] = set()
    backoff = 1.0
    # from_env reads the rstream CLI configuration (project and credentials),
    # so the worker needs no connection details in code.
    async with rstream.Client.from_env() as client:
        # The tunnel is the worker's registration, so it must outlive engine
        # hiccups: recreate it whenever the control connection drops. Sessions
        # already being served ride their own connections and are unaffected.
        while True:
            try:
                # connect opens the control channel to the engine; create_tunnel
                # then registers this worker on it.
                connection = await asyncio.wait_for(
                    client.connect(),
                    CONTROL_TIMEOUT,
                )
                async with connection as control:
                    engine_region = control.server_details.region or "unknown"
                    tunnel = await asyncio.wait_for(
                        control.create_tunnel(
                            name=args.name,
                            # Private: no public URL is minted. The tunnel is
                            # reachable only by an rstream dial from a device.
                            publish=False,
                            # Labels turn the registry into a signaling channel:
                            # devices read model and accelerator from the pool.
                            labels={
                                "role": "inference",
                                "model": config.model,
                                "device": device,
                                "accelerator": accelerator,
                                "capacity": str(args.max_sessions),
                                "engine_region": engine_region,
                            },
                        ),
                        CONTROL_TIMEOUT,
                    )
                    print(
                        f"Worker ready: {args.name} "
                        f"({config.model}, imgsz={args.imgsz}, "
                        f"{accelerator} [{device}], "
                        f"rstream region={engine_region})",
                        flush=True,
                    )
                    backoff = 1.0
                    # Each iteration yields one accepted stream: a device that
                    # dialed this worker. Sessions run concurrently.
                    async for stream in tunnel:
                        already_serving = await admission.try_acquire()
                        if already_serving is None:
                            await reject_session(stream)
                            continue
                        task = asyncio.create_task(
                            serve_session(
                                stream,
                                inference,
                                admission,
                                already_serving,
                                engine_region,
                            )
                        )
                        tasks.add(task)
                        task.add_done_callback(
                            lambda finished, active=tasks: _task_finished(
                                finished, active
                            )
                        )
                raise ConnectionError("tunnel closed")
            except Exception as error:
                print(
                    f"engine connection lost ({error!r}); retrying in {backoff:.0f}s",
                    flush=True,
                )
                await asyncio.sleep(backoff)
                backoff = min(backoff * 2, 15.0)


if __name__ == "__main__":
    with suppress(KeyboardInterrupt):
        asyncio.run(main())
