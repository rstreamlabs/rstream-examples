#!/usr/bin/env python3
"""Compare framed echo latency on loopback and through a private rstream tunnel."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import secrets
import time
from contextlib import suppress
from pathlib import Path

import rstream

MAX_ECHO_SIZE = 16 * 1024 * 1024


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--repetitions", type=int, default=20)
    parser.add_argument(
        "--sizes",
        type=int,
        nargs="+",
        default=[1024, 8192, 36_083, 131_072, 524_288],
    )
    parser.add_argument("--timeout", type=float, default=20)
    parser.add_argument(
        "--region",
        help="pin both tunnel owner and dialer to an authorized project region",
    )
    return parser.parse_args()


def validate_args(args: argparse.Namespace) -> None:
    if not 1 <= args.repetitions <= 1_000:
        raise ValueError("repetitions must be in [1,1000]")
    if not args.sizes or any(not 1 <= size <= 16 << 20 for size in args.sizes):
        raise ValueError("sizes must contain values in [1,16777216]")
    if len(set(args.sizes)) != len(args.sizes):
        raise ValueError("sizes must not contain duplicates")
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        raise ValueError("timeout must be finite and positive")


def validate_echo_size(size: int) -> None:
    if not 1 <= size <= MAX_ECHO_SIZE:
        raise ValueError(f"echo size must be in [1,{MAX_ECHO_SIZE}]")


async def echo_session(stream: rstream.RstreamStream, timeout: float) -> None:
    async with stream:
        while True:
            try:
                prefix = await asyncio.wait_for(stream.readexactly(4), timeout)
            except asyncio.IncompleteReadError:
                return
            size = int.from_bytes(prefix, "big")
            validate_echo_size(size)
            payload = await asyncio.wait_for(stream.readexactly(size), timeout)
            stream.write(size.to_bytes(4, "big") + payload)
            await asyncio.wait_for(stream.drain(), timeout)


async def exchange(
    stream: rstream.RstreamStream,
    payload: bytes,
    timeout: float,
) -> float:
    message = len(payload).to_bytes(4, "big") + payload
    started = time.perf_counter()
    stream.write(message)
    await asyncio.wait_for(stream.drain(), timeout)
    size = int.from_bytes(
        await asyncio.wait_for(stream.readexactly(4), timeout),
        "big",
    )
    if size != len(payload):
        raise ValueError(
            f"echo response length {size} does not match request {len(payload)}"
        )
    response = await asyncio.wait_for(stream.readexactly(size), timeout)
    elapsed_ms = (time.perf_counter() - started) * 1000
    if response != payload:
        raise ValueError("echo payload was corrupted")
    return elapsed_ms


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        raise ValueError("cannot calculate a percentile of an empty sample")
    ordered = sorted(values)
    index = min(len(ordered) - 1, math.ceil(fraction * len(ordered)) - 1)
    return ordered[index]


async def measure(
    stream: rstream.RstreamStream,
    sizes: list[int],
    repetitions: int,
    timeout: float,
) -> list[dict[str, object]]:
    await exchange(stream, b"warmup", timeout)
    rows = []
    for size in sizes:
        payload = bytes((index % 251 for index in range(size)))
        values = [
            await exchange(stream, payload, timeout)
            for _ in range(repetitions)
        ]
        rows.append(
            {
                "bytes": size,
                "repetitions": repetitions,
                "p50MS": round(percentile(values, 0.50), 3),
                "p95MS": round(percentile(values, 0.95), 3),
                "maxMS": round(max(values), 3),
            }
        )
    return rows


async def loopback_profile(args: argparse.Namespace) -> list[dict[str, object]]:
    async def handle(
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        await echo_session(rstream.RstreamStream(reader, writer), args.timeout)

    server = await asyncio.start_server(handle, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    async with server:
        reader, writer = await asyncio.open_connection("127.0.0.1", port)
        stream = rstream.RstreamStream(reader, writer)
        try:
            return await measure(
                stream,
                args.sizes,
                args.repetitions,
                args.timeout,
            )
        finally:
            stream.close()
            await stream.wait_closed()


async def rstream_profile(
    args: argparse.Namespace,
) -> tuple[str, list[dict[str, object]]]:
    name = f"vision-transport-q-{secrets.token_hex(4)}"
    async with rstream.Client.from_env(region=args.region) as client:
        connection = await asyncio.wait_for(client.connect(), args.timeout)
        async with connection as control:
            owner_region = control.server_details.region or "unknown"
            tunnel = await asyncio.wait_for(
                control.create_tunnel(
                    name=name,
                    publish=False,
                    labels={"role": "qualification", "sample": "vision"},
                ),
                args.timeout,
            )

            async def serve_once() -> None:
                async for accepted in tunnel:
                    await echo_session(accepted, args.timeout)
                    return

            server_task = asyncio.create_task(serve_once())
            stream = await asyncio.wait_for(client.dial(name), args.timeout)
            try:
                rows = await measure(
                    stream, args.sizes, args.repetitions, args.timeout
                )
                return owner_region, rows
            finally:
                stream.close()
                await stream.wait_closed()
                with suppress(TimeoutError):
                    await asyncio.wait_for(server_task, args.timeout)
                if not server_task.done():
                    server_task.cancel()
                    await asyncio.gather(server_task, return_exceptions=True)


async def run(args: argparse.Namespace) -> dict[str, object]:
    loopback = await loopback_profile(args)
    owner_region, routed = await rstream_profile(args)
    rows = []
    for local, remote in zip(loopback, routed, strict=True):
        rows.append(
            {
                "bytes": local["bytes"],
                "repetitions": local["repetitions"],
                "loopbackP50MS": local["p50MS"],
                "loopbackP95MS": local["p95MS"],
                "rstreamP50MS": remote["p50MS"],
                "rstreamP95MS": remote["p95MS"],
                "overheadP50MS": round(
                    float(remote["p50MS"]) - float(local["p50MS"]),
                    3,
                ),
                "overheadP95MS": round(
                    float(remote["p95MS"]) - float(local["p95MS"]),
                    3,
                ),
            }
        )
    return {
        "requestedRegion": args.region or "auto",
        "tunnelOwnerRegion": owner_region,
        "rows": rows,
        "violations": [],
    }


def main() -> int:
    args = parse_args()
    validate_args(args)
    result = asyncio.run(run(args))
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
