#!/usr/bin/env python3
"""Prove latency-aware worker selection across two rstream regions."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import os
import secrets
import sys
from pathlib import Path

QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
DEVICE = SAMPLE / "device"
WORKER = SAMPLE / "worker"
for path in (SAMPLE, DEVICE):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))

import rstream

import device
from live_mesh import (
    ManagedWorker,
    close_stream,
    infer_many,
    percentile,
    read_reference_frame,
    wait_inventory,
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--media", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--worker-python", required=True, type=Path)
    parser.add_argument("--local-region", default="eu-west-3")
    parser.add_argument("--remote-region", default="us-east-1")
    parser.add_argument("--frames", type=int, default=5)
    parser.add_argument("--timeout", type=float, default=60)
    return parser.parse_args()


def validate_args(args: argparse.Namespace) -> None:
    for name in ("model", "media", "worker_python"):
        if not getattr(args, name).is_file():
            raise ValueError(f"{name} is not a file: {getattr(args, name)}")
    if not 1 <= args.frames <= 1_000:
        raise ValueError("frames must be in [1,1000]")
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        raise ValueError("timeout must be finite and positive")
    if not args.local_region or not args.remote_region:
        raise ValueError("both regions must be non-empty")
    if args.local_region == args.remote_region:
        raise ValueError("local and remote regions must differ")


def worker_environment(region: str) -> dict[str, str]:
    environment = os.environ.copy()
    environment["RSTREAM_REGION"] = region
    return environment


async def run(args: argparse.Namespace) -> dict[str, object]:
    run_id = secrets.token_hex(4)
    local_name = f"vision-route-{run_id}-local"
    remote_name = f"vision-route-{run_id}-remote"
    names = {local_name, remote_name}
    prefix = [
        str(args.worker_python.absolute()),
        str((WORKER / "worker.py").resolve()),
    ]
    workers = [
        ManagedWorker(
            local_name,
            [
                *prefix,
                "--name",
                local_name,
                "--model",
                str(args.model.resolve()),
                "--device",
                "cpu",
                "--max-sessions",
                "2",
            ],
            args.output.parent / f"{local_name}.log",
            worker_environment(args.local_region),
        ),
        ManagedWorker(
            remote_name,
            [
                *prefix,
                "--name",
                remote_name,
                "--model",
                str(args.model.resolve()),
                "--device",
                "cpu",
                "--max-sessions",
                "2",
            ],
            args.output.parent / f"{remote_name}.log",
            worker_environment(args.remote_region),
        ),
    ]
    payload = read_reference_frame(args.media)
    streams: list[rstream.RstreamStream] = []
    previous_region = os.environ.get("RSTREAM_REGION")
    os.environ["RSTREAM_REGION"] = ""
    try:
        async with rstream.Client.from_env() as client:
            await wait_inventory(client, names, names, workers, args.timeout)
            state = device.DeviceState()
            opened = await device._open_candidates(
                state,
                client,
                [remote_name, local_name],
            )
            if len(opened) != 2:
                raise RuntimeError("both regional candidates must open")
            streams.extend(item[1] for item in opened)
            observations = {}
            for index, (name, stream, hello, establishment_ms) in enumerate(opened):
                results = await infer_many(
                    stream,
                    payload,
                    10_000 + index * 1_000,
                    args.frames,
                    args.timeout,
                )
                observations[name] = {
                    "engineRegion": hello["engine_region"],
                    "establishmentMS": round(establishment_ms, 3),
                    "roundTripP50MS": round(
                        percentile(
                            [float(result["roundTripMS"]) for result in results],
                            0.50,
                        ),
                        3,
                    ),
                    "roundTripP95MS": round(
                        percentile(
                            [float(result["roundTripMS"]) for result in results],
                            0.95,
                        ),
                        3,
                    ),
                }
            selected = min(
                opened,
                key=lambda item: device._candidate_score(item[2], item[3]),
            )[0]
            legacy = opened[0][0]
            if legacy != remote_name:
                raise RuntimeError("probe must present the remote candidate first")
            if selected != local_name:
                raise RuntimeError(
                    "latency-aware tie-break did not select the local worker"
                )
            remote_rtt = float(observations[remote_name]["roundTripP50MS"])
            local_rtt = float(observations[local_name]["roundTripP50MS"])
            return {
                "runId": run_id,
                "referencePayloadBytes": len(payload),
                "candidateOrder": [remote_name, local_name],
                "legacyChoice": legacy,
                "latencyAwareChoice": selected,
                "observations": observations,
                "measuredMedianRTTGainMS": round(remote_rtt - local_rtt, 3),
                "violations": [],
            }
    finally:
        if previous_region is None:
            os.environ.pop("RSTREAM_REGION", None)
        else:
            os.environ["RSTREAM_REGION"] = previous_region
        for stream in streams:
            await close_stream(stream)
        cleanup = await asyncio.gather(
            *(worker.stop() for worker in workers),
            return_exceptions=True,
        )
        for error in cleanup:
            if isinstance(error, BaseException):
                raise error


def main() -> int:
    args = parse_args()
    validate_args(args)
    result = asyncio.run(run(args))
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 1 if result["violations"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
