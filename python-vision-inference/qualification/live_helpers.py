"""Dependency-free helpers shared by live qualification and its unit tests."""

from __future__ import annotations

import argparse
import asyncio
import os
import signal
import subprocess
from collections.abc import Awaitable, Callable
from contextlib import suppress
from pathlib import Path
from typing import TypeVar

CLOSE_TIMEOUT = 5.0
T = TypeVar("T")


def loopback_ready_path(output: Path, run_id: str) -> Path:
    """Return an absolute rendezvous path safe for a child with another cwd."""
    return (output.parent / f"vision-loopback-{run_id}.ready").resolve()


def threshold_violations(
    args: argparse.Namespace,
    *,
    rstream_p95: float,
    failover_ms: float,
    loopback_p95: float,
) -> list[str]:
    """Return every breached live qualification budget."""
    violations = []
    if args.max_rtt_p95_ms and rstream_p95 > args.max_rtt_p95_ms:
        violations.append(
            f"rstream RTT p95 {rstream_p95:.3f} ms exceeds "
            f"{args.max_rtt_p95_ms:.3f} ms"
        )
    if args.max_failover_ms and failover_ms > args.max_failover_ms:
        violations.append(
            f"failover {failover_ms:.3f} ms exceeds {args.max_failover_ms:.3f} ms"
        )
    transport_overhead = max(rstream_p95 - loopback_p95, 0)
    if (
        args.max_transport_overhead_p95_ms
        and transport_overhead > args.max_transport_overhead_p95_ms
    ):
        violations.append(
            f"transport overhead p95 {transport_overhead:.3f} ms exceeds "
            f"{args.max_transport_overhead_p95_ms:.3f} ms"
        )
    return violations


async def stop_process_group(
    process: subprocess.Popen[str], abrupt: bool, timeout: float = 10
) -> None:
    if process.poll() is not None:
        return
    with suppress(ProcessLookupError):
        os.killpg(process.pid, signal.SIGKILL if abrupt else signal.SIGTERM)
    try:
        await asyncio.wait_for(asyncio.to_thread(process.wait), timeout)
    except TimeoutError:
        with suppress(ProcessLookupError):
            os.killpg(process.pid, signal.SIGKILL)
        await asyncio.to_thread(process.wait)


def remaining(deadline: float) -> float:
    delay = deadline - asyncio.get_running_loop().time()
    if delay <= 0:
        raise TimeoutError("operation deadline exceeded")
    return delay


async def before_deadline(
    deadline: float,
    operation: Callable[[], Awaitable[T]],
) -> T:
    return await asyncio.wait_for(operation(), remaining(deadline))
