#!/usr/bin/env python3
"""Run the real Vision worker protocol on loopback for transport baselines."""

from __future__ import annotations

import argparse
import asyncio
import json
import sys
from pathlib import Path

WORKER = Path(__file__).resolve().parents[1] / "worker"
if str(WORKER) not in sys.path:
    sys.path.insert(0, str(WORKER))

import numpy as np
import rstream
from ultralytics import YOLO

import worker


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--ready-file", required=True, type=Path)
    parser.add_argument("--device", default="cpu")
    return parser.parse_args()


async def main() -> None:
    args = parse_args()
    if not args.model.is_file():
        raise ValueError(f"model is not a file: {args.model}")
    config = worker.WorkerConfig(
        name="vision-loopback",
        model=args.model.name,
        input_size=640,
        conf=0.4,
        device=args.device,
        accelerator=worker._accelerator_name(args.device),
    )
    model = YOLO(str(args.model.resolve()))
    model.predict(
        np.zeros((640, 640, 3), dtype=np.uint8),
        imgsz=640,
        device=args.device,
        verbose=False,
    )
    inference = worker.InferenceRunner(model, config)
    admission = worker.SessionAdmission(2)

    async def handle(
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        stream = rstream.RstreamStream(reader, writer)
        already_serving = await admission.try_acquire()
        if already_serving is None:
            await worker.reject_session(stream)
            return
        await worker.serve_session(
            stream,
            inference,
            admission,
            already_serving,
            "loopback",
        )

    server = await asyncio.start_server(handle, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    args.ready_file.write_text(
        json.dumps({"host": "127.0.0.1", "port": port}) + "\n",
        encoding="utf-8",
    )
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
