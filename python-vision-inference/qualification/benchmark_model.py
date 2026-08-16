#!/usr/bin/env python3
"""Benchmark the exact model and media used by the Vision qualification."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import statistics
import sys
import time
from pathlib import Path

QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
WORKER = SAMPLE / "worker"
for path in (SAMPLE, WORKER):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))

import worker
from shared.images import encode_for_worker


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--media", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--runs", type=int, default=20)
    parser.add_argument("--concurrency", type=int, default=8)
    parser.add_argument("--imgsz", type=int, default=640)
    parser.add_argument("--conf", type=float, default=0.4)
    parser.add_argument("--device")
    parser.add_argument("--codec", choices=("jpeg", "webp", "png"), default="jpeg")
    parser.add_argument("--quality", type=int, default=80)
    parser.add_argument("--max-p95-ms", type=float, default=0)
    parser.add_argument("--min-throughput-fps", type=float, default=0)
    return parser.parse_args()


def validate_args(args: argparse.Namespace) -> None:
    if not args.model.is_file():
        raise ValueError(f"model is not a file: {args.model}")
    if not args.media.is_file():
        raise ValueError(f"media is not a file: {args.media}")
    if not 1 <= args.runs <= 10_000:
        raise ValueError("runs must be in [1,10000]")
    if not 1 <= args.concurrency <= 1_000:
        raise ValueError("concurrency must be in [1,1000]")
    if not 32 <= args.imgsz <= worker.MAX_INPUT_SIZE:
        raise ValueError(f"imgsz must be in [32,{worker.MAX_INPUT_SIZE}]")
    if not math.isfinite(args.conf) or not 0 <= args.conf <= 1:
        raise ValueError("conf must be finite and in [0,1]")
    if not 1 <= args.quality <= 100:
        raise ValueError("quality must be in [1,100]")
    for name in ("max_p95_ms", "min_throughput_fps"):
        value = getattr(args, name)
        if not math.isfinite(value) or value < 0:
            raise ValueError(
                f"{name.replace('_', '-')} must be finite and non-negative"
            )


def read_first_frame(path: Path) -> object:
    frame = worker.cv2.imread(str(path))
    if frame is not None:
        return frame
    capture = worker.cv2.VideoCapture(str(path))
    try:
        ok, frame = capture.read()
    finally:
        capture.release()
    if not ok or frame is None:
        raise ValueError(f"media has no decodable frame: {path}")
    return frame


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        raise ValueError("cannot calculate a percentile of an empty sample")
    ordered = sorted(values)
    index = min(len(ordered) - 1, math.ceil(fraction * len(ordered)) - 1)
    return ordered[index]


def validate_result(result: tuple[list[dict[str, object]], float]) -> None:
    detections, infer_ms = result
    if not detections:
        raise ValueError("reference frame produced no detections")
    if not math.isfinite(infer_ms) or infer_ms <= 0:
        raise ValueError("model returned an invalid inference duration")


async def run_benchmark(args: argparse.Namespace) -> dict[str, object]:
    device, accelerator = worker.select_device(args.device)
    frame = read_first_frame(args.media)
    payload, _ = encode_for_worker(frame, args.imgsz, args.codec, args.quality)
    model = worker.YOLO(str(args.model))
    config = worker.WorkerConfig(
        name="qualification-worker",
        model=args.model.name,
        input_size=args.imgsz,
        conf=args.conf,
        device=device,
        accelerator=accelerator,
    )
    inference = worker.InferenceRunner(model, config)
    warmup = await inference.run(payload)
    validate_result(warmup)
    sequential: list[tuple[list[dict[str, object]], float]] = []
    started = time.perf_counter()
    for _ in range(args.runs):
        result = await inference.run(payload)
        validate_result(result)
        sequential.append(result)
    sequential_wall = time.perf_counter() - started
    started = time.perf_counter()
    concurrent = await asyncio.gather(
        *(inference.run(payload) for _ in range(args.concurrency))
    )
    concurrent_wall = time.perf_counter() - started
    for result in concurrent:
        validate_result(result)
    durations = [result[1] for result in sequential]
    labels = sorted(
        {
            str(detection["label"])
            for detections, _ in sequential
            for detection in detections
        }
    )
    summary = {
        "device": device,
        "accelerator": accelerator,
        "payloadBytes": len(payload),
        "codec": args.codec,
        "quality": args.quality,
        "runs": args.runs,
        "concurrency": args.concurrency,
        "detectionCount": len(sequential[0][0]),
        "labels": labels,
        "inferenceMS": {
            "min": round(min(durations), 3),
            "mean": round(statistics.fmean(durations), 3),
            "p50": round(percentile(durations, 0.50), 3),
            "p95": round(percentile(durations, 0.95), 3),
            "max": round(max(durations), 3),
        },
        "sequentialWallSeconds": round(sequential_wall, 3),
        "sequentialThroughputFPS": round(args.runs / sequential_wall, 3),
        "concurrentWallSeconds": round(concurrent_wall, 3),
        "concurrentThroughputFPS": round(args.concurrency / concurrent_wall, 3),
        "violations": [],
    }
    violations = summary["violations"]
    if args.max_p95_ms and summary["inferenceMS"]["p95"] > args.max_p95_ms:
        violations.append(
            f"inference p95 {summary['inferenceMS']['p95']} ms exceeds "
            f"{args.max_p95_ms} ms"
        )
    if (
        args.min_throughput_fps
        and summary["sequentialThroughputFPS"] < args.min_throughput_fps
    ):
        violations.append(
            f"throughput {summary['sequentialThroughputFPS']} fps is below "
            f"{args.min_throughput_fps} fps"
        )
    return summary


def main() -> int:
    args = parse_args()
    validate_args(args)
    result = asyncio.run(run_benchmark(args))
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 1 if result["violations"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
