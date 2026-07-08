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
import platform
import re
import secrets
import socket
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


def _default_worker_name() -> str:
    # Hostnames vary with the network and may carry dots or uppercase,
    # which are not valid in tunnel names.
    host = socket.gethostname().split(".")[0].lower()
    host = re.sub(r"[^a-z0-9-]+", "-", host).strip("-")[:24] or "worker"
    return f"worker-{host}-{secrets.token_hex(2)}"


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

# The model is one shared resource, so concurrent sessions serialize their
# inference through this lock rather than thrashing the GPU; transfer and
# encoding still overlap across sessions.
_inference_lock = asyncio.Lock()
# Number of sessions currently being served, advertised in each hello so a
# device can balance across the pool (power of two choices).
_active_sessions = 0


def _detect(
    model: YOLO, payload: bytes, input_size: int, conf: float, device: str
) -> tuple[list[dict[str, object]], float]:
    frame = cv2.imdecode(np.frombuffer(payload, dtype=np.uint8), cv2.IMREAD_COLOR)
    if frame is None:
        return [], 0.0
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


@dataclass(frozen=True)
class WorkerConfig:
    name: str
    model: str
    input_size: int
    conf: float
    device: str
    accelerator: str


async def serve_session(
    stream: rstream.RstreamStream, model: YOLO, config: WorkerConfig
) -> None:
    global _active_sessions
    loop = asyncio.get_running_loop()
    print("device session opened", flush=True)
    # Report the load seen by a new device: sessions already being served.
    already_serving = _active_sessions
    _active_sessions += 1
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
                    "device": config.device,
                    "accelerator": config.accelerator,
                },
            )
            while True:
                message = await read_message(stream)
                if message is None:
                    return
                header, payload = message
                if header.get("type") != "frame":
                    continue
                async with _inference_lock:
                    detections, infer_ms = await loop.run_in_executor(
                        None,
                        _detect,
                        model,
                        payload,
                        config.input_size,
                        config.conf,
                        config.device,
                    )
                await send_message(
                    stream,
                    {
                        "type": "result",
                        "frame_id": header.get("frame_id"),
                        "infer_ms": round(infer_ms, 1),
                        "detections": detections,
                    },
                )
    except (ConnectionError, ssl.SSLError, OSError):
        # The device hung up mid-exchange, which is how sessions end when
        # detection is disabled or the device fails over. A normal goodbye.
        return
    finally:
        _active_sessions -= 1
        print("device session closed", flush=True)


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
        "--device",
        default=None,
        help="force the inference device (cpu, mps, cuda:0); auto-selected by default",
    )
    args = parser.parse_args()
    device, accelerator = select_device(args.device)
    config = WorkerConfig(
        name=args.name,
        model=args.model,
        input_size=args.imgsz,
        conf=args.conf,
        device=device,
        accelerator=accelerator,
    )
    model = YOLO(args.model)
    model.predict(
        np.zeros((args.imgsz, args.imgsz, 3), dtype=np.uint8),
        imgsz=args.imgsz,
        device=device,
        verbose=False,
    )
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
                async with await client.connect() as control:
                    tunnel = await control.create_tunnel(
                        name=args.name,
                        # Private: no public URL is minted. The tunnel is
                        # reachable only by an rstream dial from a device.
                        publish=False,
                        # Labels turn the registry into a signaling channel:
                        # devices read the model and accelerator straight from
                        # the pool, no extra protocol.
                        labels={
                            "role": "inference",
                            "model": args.model,
                            "device": device,
                            "accelerator": accelerator,
                        },
                    )
                    print(
                        f"Worker ready: {args.name} "
                        f"({args.model}, imgsz={args.imgsz}, {accelerator} [{device}])",
                        flush=True,
                    )
                    backoff = 1.0
                    # Each iteration yields one accepted stream: a device that
                    # dialed this worker. Sessions run concurrently.
                    async for stream in tunnel:
                        task = asyncio.create_task(
                            serve_session(stream, model, config)
                        )
                        tasks.add(task)
                        task.add_done_callback(tasks.discard)
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
