#!/usr/bin/env python3
"""Render Markdown and dependency-free SVG evidence from one session."""

from __future__ import annotations

import json
import sys
from datetime import datetime
from pathlib import Path


def svg_worker_lifecycle(
    path: Path,
    baseline: dict[str, object],
    degraded: dict[str, object],
    recovery: dict[str, object],
) -> None:
    phases = [
        ("A + B", baseline),
        ("B only", degraded),
        ("A + B", recovery),
    ]
    width = 960
    height = 400
    left = 150
    right = 28
    top = 94
    bottom = 70
    plot_width = width - left - right
    phase_times: list[tuple[float, float]] = []
    for _, phase in phases:
        ended_at = datetime.fromisoformat(str(phase["generatedAt"]).replace("Z", "+00:00")).timestamp()
        started_at = ended_at - float(phase["wallSeconds"])
        phase_times.append((started_at, ended_at))
    timeline_start = min(start for start, _ in phase_times)
    timeline_end = max(end for _, end in phase_times)
    timeline_duration = max(1.0, timeline_end - timeline_start)
    lane_y = {"qualification-worker-a": 180, "qualification-worker-b": 280}
    colors = {"qualification-worker-a": "#2563eb", "qualification-worker-b": "#059669"}
    lines = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">',
        '<title id="title">Worker routing through loss and recovery</title>',
        '<desc id="description">Measured workload phases show both workers serving traffic, worker B serving alone after worker A stops, and both workers serving again after worker A returns.</desc>',
        '<rect width="100%" height="100%" fill="#ffffff"/>',
        '<text x="28" y="42" fill="#111827" font-size="32" font-weight="750">Worker routing through loss and recovery</text>',
        '<text x="28" y="76" fill="#475569" font-size="18">Traffic movement during controlled worker loss and recovery</text>',
        f'<line x1="{left}" y1="{lane_y["qualification-worker-a"]}" x2="{width - right}" y2="{lane_y["qualification-worker-a"]}" stroke="#cbd5e1"/>',
        f'<line x1="{left}" y1="{lane_y["qualification-worker-b"]}" x2="{width - right}" y2="{lane_y["qualification-worker-b"]}" stroke="#cbd5e1"/>',
        f'<text x="{left - 18}" y="{lane_y["qualification-worker-a"] + 6}" text-anchor="end" fill="#111827" font-family="sans-serif" font-size="18">worker A</text>',
        f'<text x="{left - 18}" y="{lane_y["qualification-worker-b"] + 6}" text-anchor="end" fill="#111827" font-family="sans-serif" font-size="18">worker B</text>',
    ]
    for phase_index, (label, phase) in enumerate(phases):
        phase_start, phase_end = phase_times[phase_index]
        start_x = left + ((phase_start - timeline_start) / timeline_duration) * plot_width
        end_x = left + ((phase_end - timeline_start) / timeline_duration) * plot_width
        fill = "#f8fafc" if phase_index % 2 == 0 else "#eef2ff"
        lines.append(
            f'<rect x="{start_x:.1f}" y="{top}" width="{max(1, end_x - start_x):.1f}" height="{height - top - bottom}" fill="{fill}"/>'
        )
        lines.append(
            f'<text x="{start_x + 8:.1f}" y="{top + 24}" fill="#111827" font-family="sans-serif" font-size="16" font-weight="600">{label}</text>'
        )
        if phase_index > 0:
            lines.append(
                f'<line x1="{start_x:.1f}" y1="{top}" x2="{start_x:.1f}" y2="{height - bottom}" stroke="#d97706" stroke-width="2" stroke-dasharray="5 5"/>'
            )
            event = "A stopped" if phase_index == 1 else "A returned"
            lines.append(
                f'<text x="{start_x - 6:.1f}" y="{top - 10}" text-anchor="end" fill="#b45309" font-family="sans-serif" font-size="14" font-weight="600">{event}</text>'
            )
        workers = phase.get("summary", {}).get("workers", {})
        for worker, count in workers.items():
            if not count or worker not in lane_y:
                continue
            bar_y = lane_y[worker] - 12
            lines.append(
                f'<rect x="{start_x:.1f}" y="{bar_y}" width="{max(2, end_x - start_x):.1f}" height="24" rx="8" fill="{colors[worker]}"/>'
            )
            lines.append(
                f'<text x="{(start_x + end_x) / 2:.1f}" y="{bar_y + 17}" text-anchor="middle" fill="#ffffff" font-family="sans-serif" font-size="14" font-weight="700">{count} {"turn" if count == 1 else "turns"}</text>'
            )
    lines.extend(
        [
            f'<line x1="{left}" y1="{height - bottom}" x2="{width - right}" y2="{height - bottom}" stroke="#94a3b8"/>',
            f'<text x="{(left + width - right) / 2:.1f}" y="{height - 26}" text-anchor="middle" fill="#475569" font-family="sans-serif" font-size="16">Elapsed time across the controlled worker lifecycle</text>',
        ]
    )
    for tick in range(0, int(timeline_duration) + 1, 20):
        x = left + (tick / timeline_duration) * plot_width
        lines.append(
            f'<text x="{x:.1f}" y="{height - bottom + 24}" text-anchor="middle" fill="#64748b" font-family="sans-serif" font-size="13">{tick}s</text>'
        )
    lines.append("</svg>")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


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
                "restarted before the recovery phase. The chart preserves "
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
            svg_worker_lifecycle(output / "worker-lifecycle.svg", live, degraded, recovery)
            degraded_summary = degraded["summary"]
            recovery_summary = recovery["summary"]
            lines.extend(
                [
                    "## Routing through worker loss and recovery",
                    "",
                    "The lifecycle sequence starts with both workers registered, stops worker A before the degraded phase, then starts it again before the recovery phase. New turns must continue without a failed stream while one worker is absent, and both workers must serve traffic again after recovery.",
                    "",
                    "![Worker routing through loss and recovery](worker-lifecycle.svg)",
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
