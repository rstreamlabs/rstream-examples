#!/usr/bin/env python3
"""Qualify two managed Vision workers through a live rstream environment."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import os
import secrets
import statistics
import subprocess
import sys
import time
from contextlib import suppress
from pathlib import Path

QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
WORKER = SAMPLE / "worker"
DEVICE = SAMPLE / "device"
for path in (SAMPLE, DEVICE):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))

import cv2
import rstream

import device
from live_helpers import (
    CLOSE_TIMEOUT,
    before_deadline,
    detection_signature,
    loopback_ready_path,
    remaining,
    stop_process_group,
    threshold_violations,
)
from shared.protocol import read_message, send_message

FILTERS = rstream.TunnelFilters(labels={"role": "inference"})


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--media", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--worker-python", required=True, type=Path)
    parser.add_argument("--frames-per-session", type=int, default=5)
    parser.add_argument("--startup-timeout", type=float, default=60)
    parser.add_argument("--operation-timeout", type=float, default=20)
    parser.add_argument("--max-rtt-p95-ms", type=float, default=0)
    parser.add_argument("--max-failover-ms", type=float, default=0)
    parser.add_argument("--max-transport-overhead-p95-ms", type=float, default=0)
    return parser.parse_args()


def validate_args(args: argparse.Namespace) -> None:
    for name in ("model", "media", "worker_python"):
        path = getattr(args, name)
        if not path.is_file():
            raise ValueError(f"{name} is not a file: {path}")
    if not 1 <= args.frames_per_session <= 1_000:
        raise ValueError("frames-per-session must be in [1,1000]")
    if not math.isfinite(args.startup_timeout) or args.startup_timeout <= 0:
        raise ValueError("startup-timeout must be finite and positive")
    if not math.isfinite(args.operation_timeout) or args.operation_timeout <= 0:
        raise ValueError("operation-timeout must be finite and positive")
    for name in (
        "max_rtt_p95_ms",
        "max_failover_ms",
        "max_transport_overhead_p95_ms",
    ):
        value = getattr(args, name)
        if not math.isfinite(value) or value < 0:
            raise ValueError(
                f"{name.replace('_', '-')} must be finite and non-negative"
            )


class ManagedWorker:
    def __init__(
        self,
        name: str,
        command: list[str],
        log_path: Path,
        env: dict[str, str] | None = None,
    ) -> None:
        self.name = name
        self._log = log_path.open("w", encoding="utf-8")
        try:
            self.process = subprocess.Popen(
                command,
                cwd=WORKER,
                stdout=self._log,
                stderr=subprocess.STDOUT,
                text=True,
                start_new_session=True,
                env=env,
            )
        except BaseException:
            self._log.close()
            raise

    def ensure_running(self) -> None:
        exit_code = self.process.poll()
        if exit_code is not None:
            raise RuntimeError(f"worker {self.name} exited early with {exit_code}")

    async def stop(self, abrupt: bool = False) -> None:
        try:
            await stop_process_group(self.process, abrupt)
        finally:
            self._log.close()


def read_reference_frame(path: Path) -> bytes:
    frame = cv2.imread(str(path))
    if frame is None:
        capture = cv2.VideoCapture(str(path))
        try:
            ok, frame = capture.read()
        finally:
            capture.release()
        if not ok or frame is None:
            raise ValueError(f"media has no decodable frame: {path}")
    payload, _ = device._encode_for_worker(
        frame,
        input_size=640,
        codec="jpeg",
        quality=80,
    )
    return payload


async def inventory(client: rstream.Client, names: set[str]) -> set[str]:
    tunnels = await client.list_tunnels(filters=FILTERS)
    result = {
        tunnel.properties.name
        for tunnel in tunnels
        if tunnel.status == "online" and tunnel.properties.name in names
    }
    return result


async def wait_inventory(
    client: rstream.Client,
    expected: set[str],
    all_names: set[str],
    workers: list[ManagedWorker],
    timeout: float,
) -> float:
    loop = asyncio.get_running_loop()
    started = loop.time()
    deadline = started + timeout
    observed: set[str] = set()
    while True:
        for worker in workers:
            if worker.name in expected:
                worker.ensure_running()
        observed = await before_deadline(
            deadline,
            lambda: inventory(client, all_names),
        )
        if observed == expected:
            return (loop.time() - started) * 1000
        try:
            await asyncio.sleep(min(0.1, remaining(deadline)))
        except TimeoutError as error:
            raise TimeoutError(
                f"inventory observed {sorted(observed)}, "
                f"expected {sorted(expected)}"
            ) from error


async def open_with_retry(
    client: rstream.Client,
    name: str,
    timeout: float,
) -> tuple[rstream.RstreamStream, dict[str, object]]:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    last_error: Exception | None = None
    while True:
        try:
            return await before_deadline(
                deadline,
                lambda: device.open_session(client, name),
            )
        except Exception as error:
            last_error = error
            try:
                await asyncio.sleep(min(0.05, remaining(deadline)))
            except TimeoutError as timeout_error:
                raise TimeoutError(
                    f"could not open {name}: {last_error!r}"
                ) from timeout_error


async def close_stream(stream: rstream.RstreamStream) -> None:
    with suppress(Exception):
        stream.close()
        await asyncio.wait_for(stream.wait_closed(), CLOSE_TIMEOUT)


async def infer(
    stream: rstream.RstreamStream,
    payload: bytes,
    frame_id: int,
    timeout: float,
) -> dict[str, object]:
    started = time.monotonic()
    await asyncio.wait_for(
        send_message(
            stream,
            {"type": "frame", "frame_id": frame_id, "codec": "jpeg"},
            payload,
        ),
        timeout,
    )
    sent_at = time.monotonic()
    message = await asyncio.wait_for(read_message(stream), timeout)
    if message is None:
        raise ConnectionError("worker closed before returning a result")
    header, binary = message
    if header.get("type") == "error":
        raise ConnectionError(device._worker_error(header))
    if header.get("type") != "result":
        raise ValueError(f"unexpected result type {header.get('type')!r}")
    result_id, infer_ms, detections = device._parse_result(header, binary)
    if result_id != frame_id:
        raise ValueError(f"expected frame {frame_id}, received {result_id}")
    result = {
        "frameId": frame_id,
        "roundTripMS": round((time.monotonic() - started) * 1000, 3),
        "sendDrainMS": round((sent_at - started) * 1000, 3),
        "responseWaitMS": round((time.monotonic() - sent_at) * 1000, 3),
        "inferenceMS": round(infer_ms, 3),
        "detectionCount": len(detections),
        "labels": sorted(str(item["label"]) for item in detections),
        "detectionSignatureSha256": detection_signature(detections),
    }
    return result


async def infer_many(
    stream: rstream.RstreamStream,
    payload: bytes,
    first_frame_id: int,
    count: int,
    timeout: float,
) -> list[dict[str, object]]:
    results = []
    for offset in range(count):
        results.append(
            await infer(stream, payload, first_frame_id + offset, timeout)
        )
    return results


async def expect_capacity_rejection(
    client: rstream.Client,
    name: str,
    timeout: float,
) -> float:
    started = time.monotonic()
    stream = await asyncio.wait_for(client.dial(name), timeout)
    try:
        message = await asyncio.wait_for(read_message(stream), timeout)
        if message is None:
            raise ConnectionError("worker closed without an overload response")
        header, payload = message
        if payload or header.get("type") != "error":
            raise ValueError("excess session did not receive a protocol error")
        if header.get("code") != "at_capacity":
            raise ValueError(f"unexpected overload code {header.get('code')!r}")
    finally:
        await close_stream(stream)
    return (time.monotonic() - started) * 1000


async def wait_peer_loss(
    stream: rstream.RstreamStream,
    timeout: float,
) -> str:
    try:
        message = await asyncio.wait_for(read_message(stream), timeout)
    except (ConnectionError, OSError, ValueError, rstream.RstreamError) as error:
        return type(error).__name__
    if message is not None:
        raise ValueError("abrupt worker loss yielded an unexpected message")
    return "eof"


def percentile(values: list[float], fraction: float) -> float:
    ordered = sorted(values)
    index = min(len(ordered) - 1, math.ceil(fraction * len(ordered)) - 1)
    return ordered[index]


async def measure_loopback(
    args: argparse.Namespace,
    payload: bytes,
    run_id: str,
) -> tuple[float, list[dict[str, object]]]:
    ready_file = loopback_ready_path(args.output, run_id)
    ready_file.unlink(missing_ok=True)
    process = ManagedWorker(
        f"vision-loopback-{run_id}",
        [
            # Keep the venv launcher unresolved for the same reason as the
            # live worker command below.
            str(args.worker_python.absolute()),
            str((QUALIFICATION / "loopback_worker.py").resolve()),
            "--model",
            str(args.model.resolve()),
            "--ready-file",
            str(ready_file),
            "--device",
            "cpu",
        ],
        args.output.parent / f"vision-loopback-{run_id}.log",
    )
    stream: rstream.RstreamStream | None = None
    started = time.monotonic()
    try:
        while not ready_file.is_file():
            process.ensure_running()
            if time.monotonic() - started >= args.startup_timeout:
                raise TimeoutError("loopback worker did not become ready")
            await asyncio.sleep(0.05)
        address = json.loads(ready_file.read_text(encoding="utf-8"))
        reader, writer = await asyncio.wait_for(
            asyncio.open_connection(address["host"], address["port"]),
            args.operation_timeout,
        )
        stream = rstream.RstreamStream(reader, writer)
        hello = await asyncio.wait_for(read_message(stream), args.operation_timeout)
        if hello is None or hello[0].get("type") != "hello":
            raise ConnectionError("loopback worker did not send a valid hello")
        device._validate_hello(hello[0])
        startup_ms = (time.monotonic() - started) * 1000
        results = await infer_many(
            stream,
            payload,
            100,
            args.frames_per_session,
            args.operation_timeout,
        )
        return startup_ms, results
    finally:
        if stream is not None:
            await close_stream(stream)
        await process.stop()
        ready_file.unlink(missing_ok=True)


async def run(args: argparse.Namespace) -> dict[str, object]:
    run_id = secrets.token_hex(4)
    payload = read_reference_frame(args.media)
    loopback_start_ms, loopback_results = await measure_loopback(
        args,
        payload,
        run_id,
    )
    names = {f"vision-q-{run_id}-a", f"vision-q-{run_id}-b"}
    name_a, name_b = sorted(names)
    command_prefix = [
        # Do not resolve this symlink: resolving a venv launcher to the base
        # interpreter silently discards the virtual environment.
        str(args.worker_python.absolute()),
        str((WORKER / "worker.py").resolve()),
    ]
    workers = [
        ManagedWorker(
            name,
            [
                *command_prefix,
                "--name",
                name,
                "--model",
                str(args.model.resolve()),
                "--device",
                "cpu",
                "--max-sessions",
                "2",
            ],
            args.output.parent / f"{name}.log",
        )
        for name in sorted(names)
    ]
    streams: list[rstream.RstreamStream] = []
    results: list[dict[str, object]] = []
    capacity_rejection_ms = 0.0
    capacity_recovery_ms = 0.0
    failure_detection_ms = 0.0
    failure_signal = ""
    failover_ms = 0.0
    inventory_start_ms = 0.0
    inventory_removal_ms = 0.0
    try:
        async with rstream.Client.from_env() as client:
            inventory_start_ms = await wait_inventory(
                client,
                names,
                names,
                workers,
                args.startup_timeout,
            )
            a1, hello_a1 = await open_with_retry(client, name_a, args.operation_timeout)
            a2, hello_a2 = await open_with_retry(client, name_a, args.operation_timeout)
            b1, hello_b1 = await open_with_retry(client, name_b, args.operation_timeout)
            streams.extend([a1, a2, b1])
            if [hello_a1["active_sessions"], hello_a2["active_sessions"]] != [0, 1]:
                raise ValueError("worker A did not report monotonic admitted load")
            if hello_b1["active_sessions"] != 0:
                raise ValueError("worker B did not start at zero load")
            if any(
                hello["max_sessions"] != 2
                for hello in (hello_a1, hello_a2, hello_b1)
            ):
                raise ValueError("worker capacity does not match the live profile")
            worker_regions = {
                name_a: str(hello_a1["engine_region"]),
                name_b: str(hello_b1["engine_region"]),
            }
            capacity_rejection_ms = await expect_capacity_rejection(
                client,
                name_a,
                args.operation_timeout,
            )
            batches = await asyncio.gather(
                infer_many(
                    a1,
                    payload,
                    1_000,
                    args.frames_per_session,
                    args.operation_timeout,
                ),
                infer_many(
                    a2,
                    payload,
                    2_000,
                    args.frames_per_session,
                    args.operation_timeout,
                ),
                infer_many(
                    b1,
                    payload,
                    3_000,
                    args.frames_per_session,
                    args.operation_timeout,
                ),
            )
            results.extend(item for batch in batches for item in batch)
            await close_stream(a2)
            streams.remove(a2)
            recovery_started = time.monotonic()
            a3, hello_a3 = await open_with_retry(
                client,
                name_a,
                args.operation_timeout,
            )
            capacity_recovery_ms = (time.monotonic() - recovery_started) * 1000
            streams.append(a3)
            if hello_a3["active_sessions"] != 1:
                raise ValueError("released capacity was not reflected in the hello")
            results.append(
                await infer(a3, payload, 4_000, args.operation_timeout)
            )
            await close_stream(b1)
            streams.remove(b1)
            await asyncio.sleep(0.1)
            failure_started = time.monotonic()
            await workers[0].stop(abrupt=True)
            failure_signal = await wait_peer_loss(a3, args.operation_timeout)
            failure_detection_ms = (time.monotonic() - failure_started) * 1000
            await close_stream(a3)
            streams.remove(a3)
            fallback, _ = await open_with_retry(
                client,
                name_b,
                args.operation_timeout,
            )
            streams.append(fallback)
            results.append(
                await infer(fallback, payload, 5_000, args.operation_timeout)
            )
            failover_ms = (time.monotonic() - failure_started) * 1000
            inventory_removal_ms = await wait_inventory(
                client,
                {name_b},
                names,
                workers,
                args.operation_timeout,
            )
            await close_stream(fallback)
            streams.remove(fallback)
            signatures = {
                result["detectionSignatureSha256"] for result in results
            }
            if len(signatures) != 1:
                raise ValueError("identical frames produced inconsistent detections")
    finally:
        for stream in tuple(streams):
            await close_stream(stream)
        cleanup = await asyncio.gather(
            *(worker.stop() for worker in workers),
            return_exceptions=True,
        )
        for error in cleanup:
            if isinstance(error, BaseException):
                raise error
    round_trips = [float(result["roundTripMS"]) for result in results]
    send_drains = [float(result["sendDrainMS"]) for result in results]
    response_waits = [float(result["responseWaitMS"]) for result in results]
    inference_times = [float(result["inferenceMS"]) for result in results]
    loopback_round_trips = [
        float(result["roundTripMS"]) for result in loopback_results
    ]
    loopback_p95 = percentile(loopback_round_trips, 0.95)
    rstream_p95 = percentile(round_trips, 0.95)
    result = {
        "runId": run_id,
        "workers": sorted(names),
        "workerEngineRegions": worker_regions,
        "frames": len(results),
        "referencePayloadBytes": len(payload),
        "loopback": {
            "startupMS": round(loopback_start_ms, 3),
            "frames": len(loopback_results),
            "roundTripP50MS": round(percentile(loopback_round_trips, 0.50), 3),
            "roundTripP95MS": round(loopback_p95, 3),
        },
        "inventoryStartupMS": round(inventory_start_ms, 3),
        "capacityRejectionMS": round(capacity_rejection_ms, 3),
        "capacityRecoveryMS": round(capacity_recovery_ms, 3),
        "failureDetectionMS": round(failure_detection_ms, 3),
        "failureSignal": failure_signal,
        "failoverMS": round(failover_ms, 3),
        "inventoryRemovalMS": round(inventory_removal_ms, 3),
        "roundTripMS": {
            "p50": round(percentile(round_trips, 0.50), 3),
            "p95": round(rstream_p95, 3),
            "max": round(max(round_trips), 3),
        },
        "sendDrainMS": {
            "p50": round(percentile(send_drains, 0.50), 3),
            "p95": round(percentile(send_drains, 0.95), 3),
        },
        "responseWaitMS": {
            "p50": round(percentile(response_waits, 0.50), 3),
            "p95": round(percentile(response_waits, 0.95), 3),
        },
        "transportOverheadP95MS": round(max(rstream_p95 - loopback_p95, 0), 3),
        "inferenceMS": {
            "p50": round(percentile(inference_times, 0.50), 3),
            "p95": round(percentile(inference_times, 0.95), 3),
            "max": round(max(inference_times), 3),
        },
        "detectionCount": results[0]["detectionCount"],
        "labels": sorted(set(results[0]["labels"])),
        "detectionSignatureSha256": results[0]["detectionSignatureSha256"],
        "violations": threshold_violations(
            args,
            rstream_p95=rstream_p95,
            failover_ms=failover_ms,
            loopback_p95=loopback_p95,
        ),
    }
    return result


def main() -> int:
    args = parse_args()
    validate_args(args)
    result = asyncio.run(run(args))
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 1 if result["violations"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
