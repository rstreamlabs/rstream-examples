#!/usr/bin/env python3
"""Run bounded local and optional live qualification for private-llm-mesh."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

QUALIFICATION = Path(__file__).resolve().parent
SAMPLE = QUALIFICATION.parent
REPOSITORY = SAMPLE.parent
WEB = SAMPLE / "web"
WORKER = SAMPLE / "worker"
ACTIVE_PROCESS: subprocess.Popen[str] | None = None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path, help="exact local GGUF file")
    parser.add_argument("--output", type=Path, help="new evidence directory")
    parser.add_argument("--base-url", help="running web app for live qualification")
    parser.add_argument("--live-model", help="model id advertised by live workers")
    parser.add_argument("--turns", type=int, default=60)
    parser.add_argument("--concurrency", type=int, default=8)
    parser.add_argument("--min-workers", type=int, default=2)
    parser.add_argument("--max-worker-share", type=float, default=0.65)
    parser.add_argument("--max-failure-rate", type=float, default=0.0)
    parser.add_argument("--max-ttft-p95-ms", type=int, default=0)
    parser.add_argument("--max-total-p95-ms", type=int, default=0)
    parser.add_argument("--command-timeout", type=int, default=900)
    return parser.parse_args()


def require_arguments(args: argparse.Namespace) -> None:
    if not args.model.is_file():
        raise ValueError(f"model is not a file: {args.model}")
    if args.base_url and not args.live_model:
        raise ValueError("--live-model is required with --base-url")
    if args.live_model and not args.base_url:
        raise ValueError("--base-url is required with --live-model")
    if args.turns < 1 or args.concurrency < 1 or args.min_workers < 1:
        raise ValueError("turns, concurrency, and min-workers must be positive")
    if args.command_timeout < 1:
        raise ValueError("command-timeout must be positive")
    for name in ("max_worker_share", "max_failure_rate"):
        value = getattr(args, name)
        if not 0 <= value <= 1:
            raise ValueError(f"{name.replace('_', '-')} must be between 0 and 1")
    if args.max_ttft_p95_ms < 0 or args.max_total_p95_ms < 0:
        raise ValueError("latency thresholds cannot be negative")


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(8 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def capture(command: list[str], cwd: Path = REPOSITORY) -> str:
    try:
        result = subprocess.run(command, cwd=cwd, text=True, capture_output=True, timeout=15, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        return f"unavailable ({error})"
    return (result.stdout or result.stderr).strip().splitlines()[0] if result.stdout or result.stderr else "unavailable"


def repository_state() -> dict[str, object]:
    revision = capture(["git", "rev-parse", "HEAD"])
    status = capture(["git", "status", "--porcelain=v1"])
    diff = subprocess.run(["git", "diff", "--binary", "HEAD"], cwd=REPOSITORY, capture_output=True, check=False).stdout
    return {
        "revision": revision,
        "dirty": bool(status and not status.startswith("unavailable")),
        "diffSha256": hashlib.sha256(diff).hexdigest(),
    }


def terminate_process(process: subprocess.Popen[str]) -> None:
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=5)
    except (ProcessLookupError, subprocess.TimeoutExpired):
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()


def interrupt(_signum: int, _frame: object) -> None:
    if ACTIVE_PROCESS is not None:
        terminate_process(ACTIVE_PROCESS)
    raise KeyboardInterrupt


def run_command(name: str, command: list[str], cwd: Path, output: Path, timeout: int, env: dict[str, str] | None = None) -> dict[str, object]:
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
    re.compile(r"eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}"),
    re.compile(r"(?i)(client_secret|authentication_token|api[_-]?key)\s*[=:]\s*[^\s\"']{12,}"),
)


def redact_secrets(output: Path) -> int:
    findings = 0
    for path in output.iterdir():
        if not path.is_file() or path.suffix not in {".json", ".log", ".md", ".txt", ".svg"}:
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        for pattern in SECRET_PATTERNS:
            text, count = pattern.subn("[REDACTED]", text)
            findings += count
        path.write_text(text, encoding="utf-8")
    return findings


def create_manifest(args: argparse.Namespace) -> dict[str, object]:
    model_stat = args.model.stat()
    return {
        "schemaVersion": 1,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "repository": repository_state(),
        "model": {
            "name": args.model.name,
            "bytes": model_stat.st_size,
            "sha256": file_sha256(args.model),
        },
        "host": {
            "system": platform.system(),
            "release": platform.release(),
            "machine": platform.machine(),
            "python": platform.python_version(),
        },
        "tools": {
            "go": capture(["go", "version"]),
            "node": capture(["node", "--version"]),
            "npm": capture(["npm", "--version"]),
            "cmake": capture(["cmake", "--version"]),
        },
        "live": bool(args.base_url),
        "parameters": {
            "turns": args.turns,
            "concurrency": args.concurrency,
            "minWorkers": args.min_workers,
            "maxWorkerShare": args.max_worker_share,
            "maxFailureRate": args.max_failure_rate,
            "maxTTFTP95MS": args.max_ttft_p95_ms,
            "maxTotalP95MS": args.max_total_p95_ms,
        },
    }


def main() -> int:
    args = parse_args()
    require_arguments(args)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    output = (args.output or QUALIFICATION / "results" / timestamp).resolve()
    output.mkdir(parents=True, exist_ok=False)
    os.chmod(output, 0o700)
    manifest = create_manifest(args)
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    model_env = os.environ.copy()
    model_env["MODEL"] = str(args.model.resolve())
    commands = [
        run_command("web-verify", ["npm", "run", "verify"], WEB, output, args.command_timeout),
        run_command("worker-verify", ["make", "verify"], WORKER, output, args.command_timeout),
        run_command("worker-lifecycle-stress", ["make", "test-lifecycle-stress"], WORKER, output, args.command_timeout),
        run_command(
            "worker-real-model",
            ["make", "test", "GO_TEST_FLAGS=-count=1"],
            WORKER,
            output,
            args.command_timeout,
            model_env,
        ),
        run_command(
            "worker-real-model-race",
            ["make", "test-race", "GO_TEST_FLAGS=-count=1"],
            WORKER,
            output,
            args.command_timeout,
            model_env,
        ),
    ]
    live_path = None
    if args.base_url:
        live_path = output / "live.json"
        live_command = [
            "node",
            "scripts/meshload.mjs",
            "--base-url",
            args.base_url,
            "--model",
            args.live_model,
            "--total",
            str(args.turns),
            "--concurrency",
            str(args.concurrency),
            "--min-workers",
            str(args.min_workers),
            "--max-worker-share",
            str(args.max_worker_share),
            "--max-failure-rate",
            str(args.max_failure_rate),
            "--max-ttft-p95-ms",
            str(args.max_ttft_p95_ms),
            "--max-total-p95-ms",
            str(args.max_total_p95_ms),
            "--output",
            str(live_path),
        ]
        commands.append(run_command("live-mesh", live_command, WEB, output, args.command_timeout))
    session = {"manifest": manifest, "commands": commands, "liveEvidence": live_path.name if live_path else None}
    session_path = output / "session.json"
    session_path.write_text(json.dumps(session, indent=2) + "\n", encoding="utf-8")
    findings = redact_secrets(output)
    session["secretScanFindings"] = findings
    session_path.write_text(json.dumps(session, indent=2) + "\n", encoding="utf-8")
    render = subprocess.run([sys.executable, str(QUALIFICATION / "render_report.py"), str(session_path)], cwd=REPOSITORY, check=False)
    failed = findings > 0 or render.returncode != 0 or any(command["exitCode"] != 0 for command in commands)
    print(f"evidence: {output}")
    print("qualification: FAIL" if failed else "qualification: PASS")
    return 1 if failed else 0


if __name__ == "__main__":
    signal.signal(signal.SIGINT, interrupt)
    signal.signal(signal.SIGTERM, interrupt)
    try:
        raise SystemExit(main())
    except (KeyboardInterrupt, ValueError) as error:
        print(f"qualification error: {error}", file=sys.stderr)
        raise SystemExit(2)
