#!/usr/bin/env python3
"""Qualify PostgreSQL semantics and recovery through a private rstream tunnel."""

from __future__ import annotations

import argparse
import hashlib
import html
import json
import math
import os
import platform
import re
import signal
import socket
import subprocess
import sys
import time
from contextlib import closing
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
REPOSITORY = SAMPLE.parent
QUERY_TIMEOUT_SECONDS = 8
RECOVERY_BUDGET_SECONDS = 20.0
CANCELLATION_BUDGET_SECONDS = 5.0
AVERAGE_LATENCY_BUDGET_MS = 500.0
P95_LATENCY_BUDGET_MS = 750.0
MAXIMUM_LATENCY_BUDGET_MS = 2_000.0
MINIMUM_TPS = 20.0
MINIMUM_BULK_BYTES_PER_SECOND = 250_000.0
PG_BENCH_CLIENTS = 8
PG_BENCH_THREADS = 4
PG_BENCH_TRANSACTIONS = 20
BULK_ROWS = 10_000
BULK_PAYLOAD_BYTES = 512


class QualificationError(RuntimeError):
    """Separate an invalid harness or environment from a product verdict."""


class ManagedProcess:
    """Own one process group and terminate it within a fixed deadline."""

    def __init__(self, name: str, command: list[str], log: Path) -> None:
        self.name = name
        self.command = command
        self.log_path = log
        self.log = log.open("w", encoding="utf-8")
        try:
            self.process = subprocess.Popen(
                command,
                cwd=REPOSITORY,
                stdout=self.log,
                stderr=subprocess.STDOUT,
                text=True,
                start_new_session=True,
            )
        except Exception:
            self.log.close()
            raise

    def terminate(self) -> None:
        if self.process.poll() is None:
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
                self.process.wait(timeout=5)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                try:
                    os.killpg(self.process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                self.process.wait(timeout=5)
        self.log.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--environment-label", default="staging")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--allow-dirty", action="store_true")
    parser.add_argument("--skip-build", action="store_true")
    return parser.parse_args()


def capture(command: list[str], cwd: Path = REPOSITORY) -> str:
    result = subprocess.run(
        command,
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
    return result.stdout.strip()


def available_port() -> int:
    with closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_for(predicate: Any, timeout: float, description: str) -> float:
    started = time.monotonic()
    last_error: Exception | None = None
    while time.monotonic() - started < timeout:
        try:
            if predicate():
                return time.monotonic() - started
        except Exception as error:  # noqa: BLE001 - retain the last probe cause
            last_error = error
        time.sleep(0.25)
    detail = f": {last_error}" if last_error else ""
    raise QualificationError(f"timed out waiting for {description}{detail}")


def tcp_open(port: int) -> bool:
    with closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as client:
        client.settimeout(0.5)
        return client.connect_ex(("127.0.0.1", port)) == 0


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        raise ValueError("cannot calculate a percentile without values")
    ordered = sorted(values)
    index = max(0, math.ceil(len(ordered) * fraction) - 1)
    return ordered[index]


def parse_pgbench(output: str) -> dict[str, float]:
    latency = re.search(r"latency average = ([0-9.]+) ms", output)
    throughput = re.search(r"tps = ([0-9.]+) ", output)
    if not latency or not throughput:
        raise QualificationError("pgbench did not report latency and throughput")
    return {
        "averageLatencyMS": float(latency.group(1)),
        "transactionsPerSecond": float(throughput.group(1)),
    }


def parse_pgbench_logs(
    paths: list[Path], expected_transactions: int
) -> dict[str, float]:
    latencies: list[float] = []
    for path in paths:
        for line in path.read_text(encoding="utf-8").splitlines():
            columns = line.split()
            if len(columns) < 3:
                raise QualificationError(
                    f"malformed pgbench transaction log: {path.name}"
                )
            try:
                latencies.append(int(columns[2]) / 1000)
            except ValueError as error:
                raise QualificationError(
                    f"invalid pgbench latency in {path.name}"
                ) from error
    if len(latencies) != expected_transactions:
        raise QualificationError(
            f"pgbench logged {len(latencies)} transactions, expected {expected_transactions}"
        )
    return {
        "p50LatencyMS": percentile(latencies, 0.50),
        "p95LatencyMS": percentile(latencies, 0.95),
        "p99LatencyMS": percentile(latencies, 0.99),
        "maximumLatencyMS": max(latencies),
    }


def repository_state(allow_dirty: bool) -> dict[str, Any]:
    revision = capture(["git", "rev-parse", "HEAD"])
    status = capture(["git", "status", "--porcelain=v1", "--untracked-files=all"])
    dirty = bool(status)
    if dirty and not allow_dirty:
        raise QualificationError(
            "worktree is dirty; commit first or pass --allow-dirty"
        )
    diff = subprocess.run(
        ["git", "diff", "--binary", "HEAD"],
        cwd=REPOSITORY,
        check=False,
        capture_output=True,
    ).stdout
    return {
        "revision": revision,
        "dirty": dirty,
        "diffSha256": hashlib.sha256(diff).hexdigest(),
    }


def render_svg(recoveries: dict[str, float]) -> str:
    width = 960
    height = 430
    left = 105
    top = 65
    chart_height = 255
    chart_width = 780
    ceiling = max(RECOVERY_BUDGET_SECONDS, max(recoveries.values(), default=0.0))
    scale = chart_height / (ceiling * 1.1)
    group = chart_width / max(1, len(recoveries))
    bars: list[str] = []
    labels: list[str] = []
    for index, (name, seconds) in enumerate(recoveries.items()):
        center = left + group * (index + 0.5)
        bar_height = seconds * scale
        bars.append(
            f'<rect x="{center - 36:.1f}" y="{top + chart_height - bar_height:.1f}" '
            f'width="72" height="{bar_height:.1f}" rx="6" fill="#2563eb" />'
        )
        bars.append(
            f'<text x="{center:.1f}" y="{top + chart_height - bar_height - 10:.1f}" '
            f'text-anchor="middle" class="value">{seconds:.2f} s</text>'
        )
        labels.append(
            f'<text x="{center:.1f}" y="{top + chart_height + 32}" '
            f'text-anchor="middle">{html.escape(name)}</text>'
        )
    budget_y = top + chart_height - RECOVERY_BUDGET_SECONDS * scale
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">
<style>text {{ font: 16px system-ui, sans-serif; fill: #172033; }} .title {{ font-size: 24px; font-weight: 700; }} .value {{ font-size: 14px; font-weight: 650; }}</style>
<rect width="100%" height="100%" fill="#ffffff" />
<text x="{left}" y="35" class="title">Private PostgreSQL recovery time</text>
<line x1="{left}" y1="{top + chart_height}" x2="{left + chart_width}" y2="{top + chart_height}" stroke="#9ca3af" />
<line x1="{left}" y1="{budget_y:.1f}" x2="{left + chart_width}" y2="{budget_y:.1f}" stroke="#dc2626" stroke-width="2" stroke-dasharray="8 6" />
<text x="{left + chart_width}" y="{budget_y - 8:.1f}" text-anchor="end" fill="#dc2626">{RECOVERY_BUDGET_SECONDS:.0f} s recovery budget</text>
{''.join(bars)}
{''.join(labels)}
<text x="{left}" y="405">Measured end to end through PostgreSQL TLS and both rstream tunnel processes.</text>
</svg>
"""


def evaluate(measurements: dict[str, Any]) -> list[dict[str, Any]]:
    checks = [
        (
            "postgres-tls",
            measurements["tls"] is True,
            "PostgreSQL reports TLS for the tunneled session",
        ),
        (
            "transaction-rollback",
            measurements["rollbackRows"] == 0,
            "rollback leaves no row",
        ),
        (
            "transaction-commit",
            measurements["committedRows"] == 1,
            "commit leaves exactly one row",
        ),
        (
            "bulk-copy",
            measurements["bulk"]["verified"] is True,
            "bulk row count, bytes, and digest match",
        ),
        (
            "bulk-throughput",
            measurements["bulk"]["bytesPerSecond"] >= MINIMUM_BULK_BYTES_PER_SECOND,
            f"verified bulk copy sustains at least {MINIMUM_BULK_BYTES_PER_SECOND / 1000:.0f} kB/s",
        ),
        (
            "concurrent-throughput",
            measurements["load"]["transactionsPerSecond"] >= MINIMUM_TPS,
            f"pgbench throughput is at least {MINIMUM_TPS:.0f} transactions/s",
        ),
        (
            "concurrent-latency",
            measurements["load"]["averageLatencyMS"] <= AVERAGE_LATENCY_BUDGET_MS,
            f"pgbench average latency is at most {AVERAGE_LATENCY_BUDGET_MS:.0f} ms",
        ),
        (
            "concurrent-tail-latency",
            measurements["load"]["p95LatencyMS"] <= P95_LATENCY_BUDGET_MS
            and measurements["load"]["maximumLatencyMS"] <= MAXIMUM_LATENCY_BUDGET_MS,
            f"pgbench p95 is at most {P95_LATENCY_BUDGET_MS:.0f} ms and maximum is at most {MAXIMUM_LATENCY_BUDGET_MS:.0f} ms",
        ),
        (
            "query-cancellation",
            measurements["cancellationSeconds"] <= CANCELLATION_BUDGET_SECONDS,
            f"long query cancellation completes within {CANCELLATION_BUDGET_SECONDS:.0f} s",
        ),
    ]
    checks.extend(
        (
            f"{name}-recovery",
            seconds <= RECOVERY_BUDGET_SECONDS,
            f"{name} recovers within {RECOVERY_BUDGET_SECONDS:.0f} s",
        )
        for name, seconds in measurements["recoverySeconds"].items()
    )
    return [
        {"name": name, "passed": bool(passed), "description": description}
        for name, passed, description in checks
    ]


def render_report(
    manifest: dict[str, Any], measurements: dict[str, Any], checks: list[dict[str, Any]]
) -> str:
    verdict = "PASS" if all(check["passed"] for check in checks) else "FAIL"
    recovery_rows = "\n".join(
        f"| {html.escape(name)} | {seconds:.2f} s | {RECOVERY_BUDGET_SECONDS:.0f} s |"
        for name, seconds in measurements["recoverySeconds"].items()
    )
    check_rows = "\n".join(
        f"- **{'PASS' if check['passed'] else 'FAIL'}** — `{check['name']}`: {check['description']}"
        for check in checks
    )
    return f"""# Private PostgreSQL qualification — {verdict}

Revision `{manifest['repository']['revision']}` was exercised through a private rstream tunnel with PostgreSQL TLS enabled.

![Recovery times](recovery.svg)

The run verified exact rollback and commit behavior, copied {measurements['bulk']['rows']:,} deterministic rows ({measurements['bulk']['bytes']:,} bytes), cancelled a long query, and restored service after independent PostgreSQL, publishing-tunnel, and client-listener interruptions.

## Concurrent workload

Eight clients completed {PG_BENCH_CLIENTS * PG_BENCH_TRANSACTIONS} transactions through the private tunnel. Average latency was {measurements['load']['averageLatencyMS']:.2f} ms, p95 was {measurements['load']['p95LatencyMS']:.2f} ms, maximum was {measurements['load']['maximumLatencyMS']:.2f} ms, and throughput was {measurements['load']['transactionsPerSecond']:.2f} transactions/s.

## Recovery

| Interrupted component | End-to-end recovery | Budget |
| --- | ---: | ---: |
{recovery_rows}

## Automated verdict

{check_rows}

The result qualifies this recorded revision and environment. It is a regression gate for PostgreSQL semantics and tunnel recovery, not a database capacity forecast.
"""


def assert_portable(paths: list[Path]) -> None:
    hostname = platform.node()
    home = Path.home()
    forbidden = {
        str(home),
        home.name,
        os.environ.get("USER", ""),
        hostname,
        hostname.split(".", maxsplit=1)[0],
    }
    forbidden.discard("")
    secret_patterns = (
        re.compile(r"eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}"),
        re.compile(
            r"(?i)(client_secret|authentication_token|api[_-]?key)\s*[=:]\s*[^\s\"']{12,}"
        ),
    )
    for path in paths:
        content = path.read_text(encoding="utf-8")
        if any(value in content for value in forbidden):
            raise QualificationError(f"personal host data detected in {path.name}")
        if any(pattern.search(content) for pattern in secret_patterns):
            raise QualificationError(f"credential-shaped data detected in {path.name}")


class Runner:
    def __init__(self, args: argparse.Namespace, output: Path) -> None:
        self.args = args
        self.output = output
        self.project = f"rstream-pg-q-{os.getpid()}"
        self.server_port = available_port()
        self.client_port = available_port()
        self.tunnel_name = f"postgres-q-{datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S')}-{os.getpid()}"
        self.environment = os.environ.copy()
        self.environment["POSTGRES_HOST_PORT"] = str(self.server_port)
        self.processes: dict[str, ManagedProcess] = {}
        self.process_generations = {"server": 0, "client": 0}
        self.cleaned = False
        self.compose = [
            "docker",
            "compose",
            "-p",
            self.project,
            "-f",
            str(SAMPLE / "compose.yaml"),
            "-f",
            str(QUALIFICATION / "compose.yaml"),
        ]

    def execute(
        self, command: list[str], *, timeout: int = 60, check: bool = True
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            command,
            cwd=SAMPLE,
            env=self.environment,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=check,
        )

    def compose_run(
        self, *arguments: str, timeout: int = 120
    ) -> subprocess.CompletedProcess[str]:
        return self.execute([*self.compose, *arguments], timeout=timeout)

    def start_database(self, build: bool) -> None:
        arguments = ["up", "-d"]
        if build:
            arguments.append("--build")
        self.compose_run(*arguments, timeout=300)
        wait_for(
            lambda: self.compose_run(
                "exec",
                "-T",
                "postgres",
                "pg_isready",
                "-h",
                "127.0.0.1",
                "-U",
                "app",
                "-d",
                "app",
                timeout=5,
            ).returncode
            == 0,
            30,
            "PostgreSQL readiness",
        )

    def start_tunnel(self, side: str) -> None:
        if side == "server":
            command = [
                "rstream",
                "nc",
                "--context",
                self.args.context,
                "--log-format=json",
                "--log-level=info",
                "-L",
                f"rstrm://{self.tunnel_name}",
                "-R",
                f"127.0.0.1:{self.server_port}",
            ]
        elif side == "client":
            command = [
                "rstream",
                "nc",
                "--context",
                self.args.context,
                "--log-format=json",
                "--log-level=info",
                "-L",
                f"127.0.0.1:{self.client_port}",
                "-R",
                f"rstrm://{self.tunnel_name}",
            ]
        else:
            raise ValueError(f"unknown tunnel side: {side}")
        self.stop_tunnel(side)
        self.process_generations[side] += 1
        generation = self.process_generations[side]
        self.processes[side] = ManagedProcess(
            side,
            command,
            self.output / f"{side}-{generation}.log",
        )

    def stop_tunnel(self, side: str) -> None:
        process = self.processes.pop(side, None)
        if process is not None:
            process.terminate()

    def psql(
        self, sql: str, *, timeout: int = QUERY_TIMEOUT_SECONDS, check: bool = True
    ) -> subprocess.CompletedProcess[str]:
        connection = (
            f"host=host.docker.internal port={self.client_port} dbname=app "
            "user=app sslmode=require connect_timeout=3"
        )
        command = [
            "docker",
            "run",
            "--rm",
            "--add-host=host.docker.internal:host-gateway",
            "-e",
            "PGPASSWORD=app",
            "postgres:18-alpine",
            "psql",
            "-X",
            "-v",
            "ON_ERROR_STOP=1",
            "-At",
            "-d",
            connection,
            "-c",
            sql,
        ]
        return self.execute(command, timeout=timeout, check=check)

    def query_ready(self) -> bool:
        stopped = [
            name
            for name, process in self.processes.items()
            if process.process.poll() is not None
        ]
        if stopped:
            raise QualificationError(
                f"tunnel process exited unexpectedly: {', '.join(stopped)}"
            )
        result = self.psql("SELECT 1", check=False)
        return result.returncode == 0 and result.stdout.strip() == "1"

    def require_query_failure(self) -> None:
        result = self.psql("SELECT 1", check=False)
        if result.returncode == 0:
            raise QualificationError(
                "query unexpectedly succeeded while a required component was unavailable"
            )

    def transaction_semantics(self) -> tuple[int, int]:
        table = f"qualification_tx_{os.getpid()}"
        self.psql(
            f"DROP TABLE IF EXISTS {table}; CREATE TABLE {table} (id bigint primary key, body text not null)"
        )
        self.psql(f"BEGIN; INSERT INTO {table} VALUES (1, 'rolled back'); ROLLBACK")
        rollback_rows = int(self.psql(f"SELECT count(*) FROM {table}").stdout.strip())
        self.psql(f"BEGIN; INSERT INTO {table} VALUES (2, 'committed'); COMMIT")
        committed_rows = int(self.psql(f"SELECT count(*) FROM {table}").stdout.strip())
        return rollback_rows, committed_rows

    def bulk_copy(self) -> dict[str, Any]:
        table = f"qualification_bulk_{os.getpid()}"
        bulk_path = self.output / "bulk.csv"
        digest = hashlib.sha256()
        total_bytes = 0
        with bulk_path.open("w", encoding="utf-8") as destination:
            for row in range(1, BULK_ROWS + 1):
                payload = hashlib.sha256(f"rstream-postgres-{row}".encode()).hexdigest()
                payload = (payload * ((BULK_PAYLOAD_BYTES // len(payload)) + 1))[
                    :BULK_PAYLOAD_BYTES
                ]
                logical = f"{row}:{payload}\n".encode()
                digest.update(logical)
                total_bytes += len(payload.encode())
                destination.write(f"{row},{payload}\n")
        self.psql(
            f"CREATE EXTENSION IF NOT EXISTS pgcrypto; DROP TABLE IF EXISTS {table}; "
            f"CREATE TABLE {table} (id bigint primary key, payload text not null)"
        )
        connection = (
            f"host=host.docker.internal port={self.client_port} dbname=app "
            "user=app sslmode=require connect_timeout=3"
        )
        started = time.monotonic()
        result = self.execute(
            [
                "docker",
                "run",
                "--rm",
                "--add-host=host.docker.internal:host-gateway",
                "-e",
                "PGPASSWORD=app",
                "-v",
                f"{bulk_path}:/work/bulk.csv:ro",
                "postgres:18-alpine",
                "psql",
                "-X",
                "-v",
                "ON_ERROR_STOP=1",
                "-d",
                connection,
                "-c",
                f"\\copy {table}(id,payload) FROM '/work/bulk.csv' WITH (FORMAT csv)",
            ],
            timeout=120,
        )
        elapsed = time.monotonic() - started
        if result.returncode != 0:
            raise QualificationError("bulk copy failed")
        verification = self.psql(
            f"SELECT count(*), sum(octet_length(payload)), encode(digest(string_agg(id::text || ':' || payload || E'\\n', '' ORDER BY id), 'sha256'), 'hex') FROM {table}",
            timeout=30,
        ).stdout.strip()
        rows_text, bytes_text, remote_digest = verification.split("|")
        return {
            "rows": int(rows_text),
            "bytes": int(bytes_text),
            "seconds": round(elapsed, 3),
            "bytesPerSecond": round(total_bytes / elapsed, 1),
            "verified": int(rows_text) == BULK_ROWS
            and int(bytes_text) == total_bytes
            and remote_digest == digest.hexdigest(),
            "sha256": digest.hexdigest(),
        }

    def load(self) -> dict[str, float]:
        script = self.output / "pgbench.sql"
        script.write_text(
            "SELECT md5(:client_id::text || ':' || :random_value::text);\n",
            encoding="utf-8",
        )
        log_prefix = self.output / "pgbench_log"
        connection_arguments = [
            "-h",
            "host.docker.internal",
            "-p",
            str(self.client_port),
            "-U",
            "app",
            "-d",
            "app",
        ]
        result = self.execute(
            [
                "docker",
                "run",
                "--rm",
                "--user",
                f"{os.getuid()}:{os.getgid()}",
                "--add-host=host.docker.internal:host-gateway",
                "-e",
                "HOME=/tmp",
                "-e",
                "PGPASSWORD=app",
                "-e",
                "PGSSLMODE=require",
                "-v",
                f"{self.output}:/work",
                "postgres:18-alpine",
                "pgbench",
                *connection_arguments,
                "-n",
                "-c",
                str(PG_BENCH_CLIENTS),
                "-j",
                str(PG_BENCH_THREADS),
                "-t",
                str(PG_BENCH_TRANSACTIONS),
                "-f",
                "/work/pgbench.sql",
                "--log",
                f"--log-prefix=/work/{log_prefix.name}",
                "-D",
                "random_value=8675309",
            ],
            timeout=120,
        )
        summary = parse_pgbench(result.stdout + result.stderr)
        transaction_logs = sorted(self.output.glob(f"{log_prefix.name}.*"))
        summary.update(
            parse_pgbench_logs(
                transaction_logs,
                PG_BENCH_CLIENTS * PG_BENCH_TRANSACTIONS,
            )
        )
        for path in transaction_logs:
            path.unlink()
        return summary

    def cancellation(self) -> float:
        connection = (
            f"host=host.docker.internal port={self.client_port} dbname=app "
            "user=app sslmode=require connect_timeout=3"
        )
        container_name = f"{self.project}-cancel"
        command = [
            "docker",
            "run",
            "--rm",
            "--name",
            container_name,
            "--add-host=host.docker.internal:host-gateway",
            "-e",
            "PGPASSWORD=app",
            "postgres:18-alpine",
            "psql",
            "-X",
            "-v",
            "ON_ERROR_STOP=1",
            "-d",
            connection,
            "-c",
            "SELECT pg_sleep(30)",
        ]
        try:
            process = subprocess.Popen(
                command,
                cwd=SAMPLE,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )
            wait_for(
                lambda: self.psql(
                    "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND query LIKE 'SELECT pg_sleep(30)%'",
                    check=False,
                ).stdout.strip()
                == "1",
                10,
                "server-side long query",
            )
            started = time.monotonic()
            process.send_signal(signal.SIGINT)
            try:
                process.communicate(timeout=CANCELLATION_BUDGET_SECONDS)
            except subprocess.TimeoutExpired as error:
                process.kill()
                process.wait()
                raise QualificationError(
                    "cancelled query did not terminate within its budget"
                ) from error
            elapsed = time.monotonic() - started
            if process.returncode == 0:
                raise QualificationError("cancelled query returned success")
            wait_for(
                lambda: self.psql(
                    "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND query LIKE 'SELECT pg_sleep(30)%'",
                    check=False,
                ).stdout.strip()
                == "0",
                CANCELLATION_BUDGET_SECONDS,
                "server-side query cancellation",
            )
        finally:
            subprocess.run(
                ["docker", "rm", "--force", container_name],
                cwd=SAMPLE,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=10,
                check=False,
            )
        wait_for(
            self.query_ready, RECOVERY_BUDGET_SECONDS, "query path after cancellation"
        )
        return elapsed

    def recover_database(self) -> float:
        self.compose_run("stop", "postgres")
        self.require_query_failure()
        started = time.monotonic()
        self.compose_run("start", "postgres")
        wait_for(self.query_ready, RECOVERY_BUDGET_SECONDS, "database recovery")
        return time.monotonic() - started

    def recover_tunnel(self, side: str) -> float:
        self.stop_tunnel(side)
        if side == "client":
            wait_for(
                lambda: not tcp_open(self.client_port), 5, "client listener shutdown"
            )
        self.require_query_failure()
        started = time.monotonic()
        self.start_tunnel(side)
        wait_for(self.query_ready, RECOVERY_BUDGET_SECONDS, f"{side} tunnel recovery")
        return time.monotonic() - started

    def cleanup(self, *, require_clean: bool = False) -> None:
        if self.cleaned:
            return
        for side in list(self.processes):
            self.stop_tunnel(side)
        failure = ""
        try:
            result = subprocess.run(
                [*self.compose, "down", "--volumes", "--remove-orphans"],
                cwd=SAMPLE,
                env=self.environment,
                capture_output=True,
                text=True,
                timeout=60,
                check=False,
            )
            if result.returncode != 0:
                failure = (result.stderr or result.stdout).strip()
            remaining = subprocess.run(
                [*self.compose, "ps", "--all", "--quiet"],
                cwd=SAMPLE,
                env=self.environment,
                capture_output=True,
                text=True,
                timeout=15,
                check=False,
            )
            if remaining.returncode != 0 or remaining.stdout.strip():
                failure = failure or "Compose resources remain after cleanup"
        except (OSError, subprocess.TimeoutExpired) as error:
            failure = str(error)
        if failure:
            if require_clean:
                raise QualificationError(f"cleanup failed: {failure}")
            print(f"cleanup warning: {failure}", file=sys.stderr)
            return
        self.cleaned = True


def tool_version(command: list[str]) -> str:
    try:
        result = subprocess.run(
            command, capture_output=True, text=True, timeout=10, check=False
        )
    except (OSError, subprocess.TimeoutExpired):
        return "unavailable"
    output = (result.stdout or result.stderr).strip()
    return output.splitlines()[0] if output else "unavailable"


def main() -> int:
    args = parse_args()
    state = repository_state(args.allow_dirty)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    output = (args.output or QUALIFICATION / "results" / timestamp).resolve()
    output.mkdir(parents=True, exist_ok=False)
    os.chmod(output, 0o700)
    runner = Runner(args, output)
    manifest = {
        "schemaVersion": 1,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "repository": state,
        "environment": args.environment_label,
        "runtime": {
            "system": platform.system(),
            "release": platform.release(),
            "machine": platform.machine(),
            "python": platform.python_version(),
            "docker": tool_version(["docker", "--version"]),
            "rstream": tool_version(["rstream", "--version"]),
        },
        "parameters": {
            "pgbenchClients": PG_BENCH_CLIENTS,
            "pgbenchThreads": PG_BENCH_THREADS,
            "transactionsPerClient": PG_BENCH_TRANSACTIONS,
            "bulkRows": BULK_ROWS,
            "bulkPayloadBytes": BULK_PAYLOAD_BYTES,
            "averageLatencyBudgetMS": AVERAGE_LATENCY_BUDGET_MS,
            "p95LatencyBudgetMS": P95_LATENCY_BUDGET_MS,
            "maximumLatencyBudgetMS": MAXIMUM_LATENCY_BUDGET_MS,
            "minimumTransactionsPerSecond": MINIMUM_TPS,
            "minimumBulkBytesPerSecond": MINIMUM_BULK_BYTES_PER_SECOND,
            "cancellationBudgetSeconds": CANCELLATION_BUDGET_SECONDS,
            "recoveryBudgetSeconds": RECOVERY_BUDGET_SECONDS,
        },
    }
    try:
        runner.start_database(not args.skip_build)
        runner.start_tunnel("server")
        runner.start_tunnel("client")
        establishment = wait_for(
            runner.query_ready, RECOVERY_BUDGET_SECONDS, "initial tunneled query"
        )
        tls = (
            runner.psql(
                "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()"
            ).stdout.strip()
            == "t"
        )
        rollback_rows, committed_rows = runner.transaction_semantics()
        bulk = runner.bulk_copy()
        load = runner.load()
        cancellation = runner.cancellation()
        recoveries = {
            "database": runner.recover_database(),
            "publisher": runner.recover_tunnel("server"),
            "client listener": runner.recover_tunnel("client"),
        }
        runner.cleanup(require_clean=True)
        measurements = {
            "establishmentSeconds": round(establishment, 3),
            "tls": tls,
            "rollbackRows": rollback_rows,
            "committedRows": committed_rows,
            "bulk": bulk,
            "load": load,
            "cancellationSeconds": round(cancellation, 3),
            "recoverySeconds": {
                name: round(seconds, 3) for name, seconds in recoveries.items()
            },
        }
        checks = evaluate(measurements)
        verdict = "PASS" if all(check["passed"] for check in checks) else "FAIL"
        manifest["verdict"] = verdict
        manifest["tunnel"] = {
            "kind": "private-tcp",
            "name": "ephemeral qualification name",
        }
        manifest_path = output / "manifest.json"
        measurements_path = output / "measurements.json"
        report_path = output / "report.md"
        svg_path = output / "recovery.svg"
        manifest_path.write_text(
            json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
        )
        measurements_path.write_text(
            json.dumps(
                {"measurements": measurements, "checks": checks},
                indent=2,
                sort_keys=True,
            )
            + "\n",
            encoding="utf-8",
        )
        report_path.write_text(
            render_report(manifest, measurements, checks), encoding="utf-8"
        )
        svg_path.write_text(render_svg(recoveries), encoding="utf-8")
        bulk_path = output / "bulk.csv"
        bulk_path.unlink(missing_ok=True)
        (output / "pgbench.sql").unlink(missing_ok=True)
        assert_portable([manifest_path, measurements_path, report_path, svg_path])
        print(f"{verdict}: {report_path}")
        return 0 if verdict == "PASS" else 1
    except (
        QualificationError,
        subprocess.CalledProcessError,
        subprocess.TimeoutExpired,
    ) as error:
        print(f"QUALIFICATION ERROR: {error}", file=sys.stderr)
        return 2
    finally:
        runner.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
