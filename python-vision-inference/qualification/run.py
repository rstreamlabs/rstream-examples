#!/usr/bin/env python3
"""Run bounded local qualification for python-vision-inference."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import platform
import re
import signal
import subprocess
import sys
import time
from contextlib import suppress
from datetime import datetime, timezone
from pathlib import Path

from render_report import render

QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
REPOSITORY = SAMPLE.parent
WORKER = SAMPLE / "worker"
WORKER_PYTHON = WORKER / ".venv" / "bin" / "python"
DEVICE_PYTHON = SAMPLE / "device" / ".venv" / "bin" / "python"
ACTIVE_PROCESS: subprocess.Popen[str] | None = None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--media", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--runs", type=int, default=20)
    parser.add_argument("--concurrency", type=int, default=8)
    parser.add_argument("--stress-repetitions", type=int, default=100)
    parser.add_argument("--max-p95-ms", type=float, default=0)
    parser.add_argument("--min-throughput-fps", type=float, default=0)
    parser.add_argument("--command-timeout", type=int, default=900)
    parser.add_argument(
        "--live",
        action="store_true",
        help="exercise discovery, transit, saturation, and failover through rstream",
    )
    parser.add_argument(
        "--regional-routing",
        action="store_true",
        help="with --live, compare equally-loaded workers in two regions",
    )
    parser.add_argument("--local-region", default="eu-west-3")
    parser.add_argument("--remote-region", default="us-east-1")
    parser.add_argument("--live-frames", type=int, default=5)
    parser.add_argument("--transport-repetitions", type=int, default=20)
    parser.add_argument("--max-live-p95-ms", type=float, default=0)
    parser.add_argument("--max-failover-ms", type=float, default=0)
    parser.add_argument("--max-transport-overhead-p95-ms", type=float, default=0)
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
    if not 1 <= args.stress_repetitions <= 1_000:
        raise ValueError("stress-repetitions must be in [1,1000]")
    if args.command_timeout < 1:
        raise ValueError("command-timeout must be positive")
    if args.regional_routing and not args.live:
        raise ValueError("regional-routing requires --live")
    if not 1 <= args.live_frames <= 1_000:
        raise ValueError("live-frames must be in [1,1000]")
    if not 1 <= args.transport_repetitions <= 1_000:
        raise ValueError("transport-repetitions must be in [1,1000]")
    if not args.local_region or not args.remote_region:
        raise ValueError("local-region and remote-region must be non-empty")
    if args.local_region == args.remote_region:
        raise ValueError("local-region and remote-region must differ")
    for name in (
        "max_p95_ms",
        "min_throughput_fps",
        "max_live_p95_ms",
        "max_failover_ms",
        "max_transport_overhead_p95_ms",
    ):
        value = getattr(args, name)
        if not math.isfinite(value) or value < 0:
            raise ValueError(
                f"{name.replace('_', '-')} must be finite and non-negative"
            )


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(8 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def capture(command: list[str], cwd: Path = REPOSITORY) -> str:
    try:
        result = subprocess.run(
            command,
            cwd=cwd,
            text=True,
            capture_output=True,
            timeout=15,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        return f"unavailable ({error})"
    output = (result.stdout or result.stderr).strip().splitlines()
    return output[0] if output else "unavailable"


def git_output(command: list[str]) -> bytes:
    try:
        result = subprocess.run(
            ["git", *command],
            cwd=REPOSITORY,
            capture_output=True,
            timeout=15,
            check=True,
        )
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        raise RuntimeError(f"git {' '.join(command)} failed") from error
    return result.stdout


def untracked_fingerprint(paths: bytes) -> bytes:
    digest = hashlib.sha256()
    for raw_path in sorted(filter(None, paths.split(b"\0"))):
        path = Path(os.fsdecode(raw_path))
        if path.is_absolute() or ".." in path.parts:
            raise RuntimeError("git returned an unsafe untracked path")
        candidate = REPOSITORY / path
        digest.update(raw_path)
        digest.update(b"\0")
        if candidate.is_symlink():
            digest.update(b"symlink\0")
            digest.update(os.fsencode(os.readlink(candidate)))
        elif candidate.is_file():
            digest.update(b"file\0")
            with candidate.open("rb") as source:
                for chunk in iter(lambda: source.read(8 << 20), b""):
                    digest.update(chunk)
        else:
            raise RuntimeError("git returned an unsupported untracked entry")
        digest.update(b"\0")
    return digest.digest()


def repository_state() -> dict[str, object]:
    revision = git_output(["rev-parse", "--verify", "HEAD"]).decode().strip()
    status = git_output(["status", "--porcelain=v1", "-z"])
    diff = git_output(["diff", "--binary", "HEAD"])
    untracked = git_output(["ls-files", "--others", "--exclude-standard", "-z"])
    fingerprint = hashlib.sha256()
    fingerprint.update(b"tracked-diff\0")
    fingerprint.update(diff)
    fingerprint.update(b"\0untracked\0")
    fingerprint.update(untracked_fingerprint(untracked))
    return {
        "revision": revision,
        "dirty": bool(status),
        "diffSha256": fingerprint.hexdigest(),
    }


def terminate_process(process: subprocess.Popen[str]) -> None:
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=5)
    except (ProcessLookupError, subprocess.TimeoutExpired):
        with suppress(ProcessLookupError):
            os.killpg(process.pid, signal.SIGKILL)
        process.wait()


def interrupt(_signum: int, _frame: object) -> None:
    if ACTIVE_PROCESS is not None:
        terminate_process(ACTIVE_PROCESS)
    raise KeyboardInterrupt


def run_command(
    name: str,
    command: list[str],
    cwd: Path,
    output: Path,
    timeout: int,
    env: dict[str, str] | None = None,
) -> dict[str, object]:
    global ACTIVE_PROCESS
    log_path = output / f"{name}.log"
    started = time.monotonic()
    timed_out = False
    with log_path.open("w", encoding="utf-8") as log:
        log.write(f"phase={name}\n")
        log.flush()
        ACTIVE_PROCESS = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            text=True,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        try:
            exit_code = ACTIVE_PROCESS.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            terminate_process(ACTIVE_PROCESS)
            exit_code = 124
        finally:
            ACTIVE_PROCESS = None
    return {
        "name": name,
        "exitCode": exit_code,
        "timedOut": timed_out,
        "wallSeconds": round(time.monotonic() - started, 3),
        "log": log_path.name,
    }


SECRET_PATTERNS = (
    re.compile(
        r"eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}"
    ),
    re.compile(
        r"(?i)(client_secret|authentication_token|api[_-]?key)"
        r"\s*[=:]\s*[^\s\"']{12,}"
    ),
)
TEXT_ARTIFACT_SUFFIXES = {".json", ".log", ".md", ".txt", ".svg"}
PERSONAL_DATA_PATTERNS = (
    (re.compile(r"/Users/[^/\s\"'<>]+"), "<home>"),
    (re.compile(r"/home/[^/\s\"'<>]+"), "<home>"),
    (re.compile(r"[A-Za-z]:\\Users\\[^\\\s\"'<>]+"), "<home>"),
    (re.compile(r"Darwin\s+[^\s]+\.local"), "Darwin <host>"),
)


def text_artifacts(output: Path) -> list[Path]:
    return [
        path
        for path in output.rglob("*")
        if path.is_file() and path.suffix in TEXT_ARTIFACT_SUFFIXES
    ]


def redact_secrets(output: Path) -> int:
    findings = 0
    for path in text_artifacts(output):
        text = path.read_text(encoding="utf-8", errors="replace")
        for pattern in SECRET_PATTERNS:
            text, count = pattern.subn("[REDACTED]", text)
            findings += count
        path.write_text(text, encoding="utf-8")
    return findings


def redact_personal_data(output: Path) -> int:
    findings = 0
    for path in text_artifacts(output):
        text = path.read_text(encoding="utf-8", errors="replace")
        for pattern, replacement in PERSONAL_DATA_PATTERNS:
            text, count = pattern.subn(replacement, text)
            findings += count
        path.write_text(text, encoding="utf-8")
    return findings


def count_personal_data(output: Path) -> int:
    return sum(
        len(pattern.findall(path.read_text(encoding="utf-8", errors="replace")))
        for path in text_artifacts(output)
        for pattern, _ in PERSONAL_DATA_PATTERNS
    )


def file_manifest(path: Path) -> dict[str, object]:
    return {
        "name": path.name,
        "bytes": path.stat().st_size,
        "sha256": file_sha256(path),
    }


def main() -> int:
    args = parse_args()
    validate_args(args)
    if not WORKER_PYTHON.is_file():
        raise ValueError("run `make build` before qualification")
    if args.live and not DEVICE_PYTHON.is_file():
        raise ValueError("run `make build` before live qualification")
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    output = (args.output or QUALIFICATION / "results" / timestamp).resolve()
    output.mkdir(parents=True, exist_ok=False)
    os.chmod(output, 0o700)
    manifest = {
        "schemaVersion": 1,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "repository": repository_state(),
        "model": file_manifest(args.model),
        "media": file_manifest(args.media),
        "host": {
            "system": platform.system(),
            "release": platform.release(),
            "machine": platform.machine(),
            "python": platform.python_version(),
        },
        "tools": {
            "git": capture(["git", "--version"]),
            "node": capture(["node", "--version"]),
            "npm": capture(["npm", "--version"]),
        },
        "parameters": {
            "runs": args.runs,
            "concurrency": args.concurrency,
            "stressRepetitions": args.stress_repetitions,
            "maxP95MS": args.max_p95_ms,
            "minThroughputFPS": args.min_throughput_fps,
            "live": args.live,
            "regionalRouting": args.regional_routing,
            "localRegion": args.local_region,
            "remoteRegion": args.remote_region,
            "liveFrames": args.live_frames,
            "transportRepetitions": args.transport_repetitions,
            "maxLiveP95MS": args.max_live_p95_ms,
            "maxFailoverMS": args.max_failover_ms,
            "maxTransportOverheadP95MS": args.max_transport_overhead_p95_ms,
        },
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n",
        encoding="utf-8",
    )
    environment = os.environ.copy()
    environment["MODEL"] = str(args.model.resolve())
    environment["QUALIFICATION_MEDIA"] = str(args.media.resolve())
    commands = [
        run_command(
            "sample-verify",
            ["make", "verify"],
            SAMPLE,
            output,
            args.command_timeout,
        ),
        run_command(
            "qualification-tests",
            [
                str(WORKER_PYTHON),
                "-m",
                "unittest",
                "discover",
                "-s",
                "qualification",
                "-p",
                "test_*.py",
            ],
            SAMPLE,
            output,
            args.command_timeout,
        ),
        run_command(
            "worker-cancellation-stress",
            [
                str(WORKER_PYTHON),
                "-m",
                "unittest",
                "test_worker.WorkerAsyncTest."
                "test_cancel_keeps_model_locked_until_thread_stops",
            ],
            WORKER,
            output,
            args.command_timeout,
            {
                **environment,
                "PYTHONPATH": str(SAMPLE),
                "VISION_STRESS_REPETITIONS": str(args.stress_repetitions),
            },
        ),
        run_command(
            "worker-real-model",
            [
                str(WORKER_PYTHON),
                "-m",
                "unittest",
                "test_worker.RealModelTest",
            ],
            WORKER,
            output,
            args.command_timeout,
            {**environment, "PYTHONPATH": str(SAMPLE)},
        ),
        run_command(
            "model-benchmark",
            [
                str(WORKER_PYTHON),
                str(QUALIFICATION / "benchmark_model.py"),
                "--model",
                str(args.model.resolve()),
                "--media",
                str(args.media.resolve()),
                "--output",
                str(output / "model-benchmark.json"),
                "--runs",
                str(args.runs),
                "--concurrency",
                str(args.concurrency),
                "--max-p95-ms",
                str(args.max_p95_ms),
                "--min-throughput-fps",
                str(args.min_throughput_fps),
            ],
            SAMPLE,
            output,
            args.command_timeout,
            environment,
        ),
    ]
    if args.live:
        commands.extend(
            [
                run_command(
                    "live-mesh",
                    [
                        str(DEVICE_PYTHON),
                        str(QUALIFICATION / "live_mesh.py"),
                        "--model",
                        str(args.model.resolve()),
                        "--media",
                        str(args.media.resolve()),
                        "--output",
                        str(output / "live-mesh.json"),
                        "--worker-python",
                        str(WORKER_PYTHON),
                        "--frames-per-session",
                        str(args.live_frames),
                        "--max-rtt-p95-ms",
                        str(args.max_live_p95_ms),
                        "--max-failover-ms",
                        str(args.max_failover_ms),
                        "--max-transport-overhead-p95-ms",
                        str(args.max_transport_overhead_p95_ms),
                    ],
                    SAMPLE,
                    output,
                    args.command_timeout,
                    environment,
                ),
                run_command(
                    "transport-profile",
                    [
                        str(DEVICE_PYTHON),
                        str(QUALIFICATION / "transport_probe.py"),
                        "--output",
                        str(output / "transport-profile.json"),
                        "--repetitions",
                        str(args.transport_repetitions),
                    ],
                    SAMPLE,
                    output,
                    args.command_timeout,
                    environment,
                ),
            ]
        )
    if args.regional_routing:
        commands.append(
            run_command(
                "regional-routing",
                [
                    str(DEVICE_PYTHON),
                    str(QUALIFICATION / "routing_probe.py"),
                    "--model",
                    str(args.model.resolve()),
                    "--media",
                    str(args.media.resolve()),
                    "--output",
                    str(output / "regional-routing.json"),
                    "--worker-python",
                    str(WORKER_PYTHON),
                    "--local-region",
                    args.local_region,
                    "--remote-region",
                    args.remote_region,
                    "--frames",
                    str(args.live_frames),
                ],
                SAMPLE,
                output,
                args.command_timeout,
                environment,
            )
        )
    session = {
        "manifest": manifest,
        "commands": commands,
        "secretScanFindings": 0,
        "privacyRedactions": 0,
        "personalDataFindings": 0,
    }
    session_path = output / "session.json"
    session_path.write_text(json.dumps(session, indent=2) + "\n", encoding="utf-8")
    session["secretScanFindings"] = redact_secrets(output)
    session["privacyRedactions"] = redact_personal_data(output)
    session["personalDataFindings"] = count_personal_data(output)
    session_path.write_text(json.dumps(session, indent=2) + "\n", encoding="utf-8")
    passed = render(session_path)
    session["secretScanFindings"] += redact_secrets(output)
    session["privacyRedactions"] += redact_personal_data(output)
    session["personalDataFindings"] = count_personal_data(output)
    session_path.write_text(json.dumps(session, indent=2) + "\n", encoding="utf-8")
    passed = render(session_path) and passed
    passed = passed and session["secretScanFindings"] == 0
    passed = passed and session["personalDataFindings"] == 0
    print(output)
    return 0 if passed else 1


if __name__ == "__main__":
    signal.signal(signal.SIGINT, interrupt)
    signal.signal(signal.SIGTERM, interrupt)
    raise SystemExit(main())
