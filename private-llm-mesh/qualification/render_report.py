#!/usr/bin/env python3
"""Render Markdown and dependency-free SVG evidence from one session."""

from __future__ import annotations

import json
import sys
from pathlib import Path


def display_metric(value: object) -> str:
    return "n/a" if not isinstance(value, (int, float)) else f"{value:.1f} ms"


def render(session_path: Path) -> bool:
    session = json.loads(session_path.read_text(encoding="utf-8"))
    output = session_path.parent
    commands = session["commands"]
    failures = [command for command in commands if command["exitCode"] != 0]
    live = None
    live_missing = False
    if session.get("liveEvidence"):
        live_path = output / session["liveEvidence"]
        if live_path.is_file():
            live = json.loads(live_path.read_text(encoding="utf-8"))
        else:
            live_missing = True
    live_violations = live.get("violations", []) if live else []
    degraded_path = output / "degraded.json"
    recovery_path = output / "recovery.json"
    degraded = json.loads(degraded_path.read_text(encoding="utf-8")) if degraded_path.is_file() else None
    recovery = json.loads(recovery_path.read_text(encoding="utf-8")) if recovery_path.is_file() else None
    lifecycle_violations = [
        violation
        for phase in (degraded, recovery)
        if phase
        for violation in phase.get("violations", [])
    ]
    secret_findings = session.get("secretScanFindings", 0)
    passed = not failures and not live_violations and not lifecycle_violations and not live_missing and secret_findings == 0
    repository = session["manifest"]["repository"]
    model = session["manifest"]["model"]
    lines = [
        "# Private LLM mesh qualification report",
        "",
        f"**Verdict: {'PASS' if passed else 'FAIL'}**",
        "",
        f"Scope: {'local code/model plus live mesh' if live else 'local code and real model only; live mesh not exercised'}.",
        f"Revision: `{repository['revision']}` ({'dirty diagnostic run' if repository['dirty'] else 'clean worktree'}).",
        f"Model: `{model['name']}` · {model['bytes']} bytes · SHA-256 `{model['sha256']}`.",
        "",
        "## Method",
        "",
        "The local profile exercises scheduler invariants, bounded admission, cancellation, concurrent shutdown, and real-model behavior with Go's race detector enabled. The live profile drives the complete UI stream through Next.js, scoped-token minting, rstream transit, and the selected llama.cpp worker. A turn passes only when the stream identifies its worker, contains text or tool output, terminates correctly, and contains no error part.",
        "",
    ]
    if live and degraded and recovery:
        live_concurrency = live.get("parameters", {}).get("concurrency")
        degraded_concurrency = degraded.get("parameters", {}).get("concurrency")
        live_profile = f"{live['summary']['turns']} turns"
        degraded_profile = f"{degraded['summary']['turns']}-turn degraded phase"
        if isinstance(live_concurrency, int):
            live_profile += f" at concurrency {live_concurrency}"
        if isinstance(degraded_concurrency, int):
            degraded_profile += f" at concurrency {degraded_concurrency}"
        lines.extend(
            [
                "The live baseline sends "
                f"{live_profile}. Worker A is then stopped before a "
                f"{degraded_profile} and "
                "restarted before the recovery phase. The record preserves "
                "the measured start and end time of all three workloads.",
                "",
            ]
        )
    lines.extend(
        [
            "## Acceptance gates",
            "",
            "- Every UI stream must terminate cleanly with worker attribution and usable model output.",
            "- The live baseline must use the configured minimum worker count and stay below the configured largest-worker share.",
            "- A lifecycle record must complete every degraded turn on the surviving worker and reuse both workers after recovery.",
            "- Admission, cancellation, shutdown, routing, web, dependency, and real-model race gates must all pass.",
            "",
            "## Code and model gates",
            "",
        "| Phase | Result | Wall time |",
        "| --- | --- | ---: |",
        ]
    )
    for command in commands:
        result = "PASS" if command["exitCode"] == 0 else f"FAIL ({command['exitCode']})"
        lines.append(f"| {command['name']} | {result} | {command['wallSeconds']:.3f} s |")
    lines.append("")
    if live:
        summary = live["summary"]
        lines.extend(
            [
                "## Baseline throughput and latency",
                "",
                f"{summary['successful']}/{summary['turns']} turns succeeded across {summary['workerCount']} workers at {summary['turnsPerSecond']:.2f} turns/s.",
                "",
                "| Signal | p50 | p95 |",
                "| --- | ---: | ---: |",
                f"| Time to first token/output | {display_metric(summary['ttftP50MS'])} | {display_metric(summary['ttftP95MS'])} |",
                f"| Total turn | {display_metric(summary['totalP50MS'])} | {display_metric(summary['totalP95MS'])} |",
                "",
            ]
        )
        if degraded and recovery:
            degraded_summary = degraded["summary"]
            recovery_summary = recovery["summary"]
            lines.extend(
                [
                    "## Routing through worker loss and recovery",
                    "",
                    "The lifecycle sequence starts with both workers registered, stops worker A before the degraded phase, then starts it again before the recovery phase. New turns must continue without a failed stream while one worker is absent, and both workers must serve traffic again after recovery.",
                    "",
                    "| Gate | Required | Observed |",
                    "| --- | --- | --- |",
                    f"| Baseline completion | zero failed turns across at least two workers | {summary['successful']}/{summary['turns']}; {summary['workerCount']} workers |",
                    f"| Baseline balance | largest worker share <= {live['parameters']['thresholds']['maxWorkerShare']:.0%} | {summary['maxWorkerShare']:.0%} |",
                    f"| Degraded completion | zero failed turns after worker A stops | {degraded_summary['successful']}/{degraded_summary['turns']} on worker B |",
                    f"| Recovery | at least two workers, largest share <= {recovery['parameters']['thresholds']['maxWorkerShare']:.0%} | {recovery_summary['workerCount']} workers; {recovery_summary['maxWorkerShare']:.0%} |",
                    "",
                    "The checked-in lifecycle boundaries were operator-controlled. The record therefore qualifies request routing within the three phases, including complete service by worker B while A is absent and balanced reuse after A returns. It does not measure automatic failure-detection or registration-removal time.",
                    "",
                ]
            )
    else:
        lines.extend(
            [
                "## Live mesh",
                "",
                "Not run. This report does not qualify deployed discovery, scoped-token authorization, rstream transit, worker churn, or network-specific latency.",
                "",
            ]
        )
    lines.extend(
        [
            "## Integrity and interpretation",
            "",
            f"Credential-shaped findings: **{secret_findings}**. Any finding fails the run and is redacted in place.",
            "The manifest fixes the source revision, model hash, runtime versions, parameters, and thresholds. `session.json` retains each command result and duration; the per-turn JSON retains every published routing and latency aggregate.",
            "A dirty run is useful while developing but must not be presented as qualification of a released revision.",
            "",
        ]
    )
    if failures or live_violations or lifecycle_violations or live_missing:
        lines.extend(["## Failures", ""])
        for command in failures:
            lines.append(f"- `{command['name']}` exited {command['exitCode']}; see `{command['log']}`.")
        for violation in live_violations:
            lines.append(f"- Live threshold: {violation}.")
        for violation in lifecycle_violations:
            lines.append(f"- Lifecycle threshold: {violation}.")
        if live_missing:
            lines.append("- The live driver did not produce its machine-readable artifact.")
        lines.append("")
    (output / "report.md").write_text("\n".join(lines), encoding="utf-8")
    return passed


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: render_report.py SESSION.json")
    raise SystemExit(0 if render(Path(sys.argv[1]).resolve()) else 1)
