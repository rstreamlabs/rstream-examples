#!/usr/bin/env python3
"""Render Markdown and dependency-free SVG evidence from one session."""

from __future__ import annotations

import html
import json
import sys
from pathlib import Path


def svg_bars(path: Path, title: str, rows: list[tuple[str, float, str]], unit: str) -> None:
    width = 900
    left = 240
    right = 150
    row_height = 38
    height = 100 + row_height * max(1, len(rows))
    maximum = max((value for _, value, _ in rows), default=1) or 1
    scale = (width - left - right) / maximum
    lines = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="#111827"/>',
        f'<text x="24" y="38" fill="#f9fafb" font-family="sans-serif" font-size="22" font-weight="700">{html.escape(title)}</text>',
    ]
    for index, (label, value, color) in enumerate(rows):
        y = 70 + index * row_height
        bar_width = max(1, value * scale)
        lines.extend(
            [
                f'<text x="24" y="{y + 18}" fill="#d1d5db" font-family="sans-serif" font-size="14">{html.escape(label)}</text>',
                f'<rect x="{left}" y="{y}" width="{bar_width:.1f}" height="22" rx="4" fill="{color}"/>',
                f'<text x="{left + bar_width + 10:.1f}" y="{y + 17}" fill="#f9fafb" font-family="sans-serif" font-size="14">{value:.2f} {html.escape(unit)}</text>',
            ]
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
    secret_findings = session.get("secretScanFindings", 0)
    passed = not failures and not live_violations and not live_missing and secret_findings == 0
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
        "## Automated gates",
        "",
        "| Phase | Result | Wall time | Raw log |",
        "| --- | --- | ---: | --- |",
    ]
    for command in commands:
        result = "PASS" if command["exitCode"] == 0 else f"FAIL ({command['exitCode']})"
        lines.append(f"| {command['name']} | {result} | {command['wallSeconds']:.3f} s | [{command['log']}]({command['log']}) |")
    lines.extend(["", "![Command duration](commands.svg)", ""])
    command_rows = [
        (command["name"], float(command["wallSeconds"]), "#22c55e" if command["exitCode"] == 0 else "#ef4444")
        for command in commands
    ]
    svg_bars(output / "commands.svg", "Qualification phase duration", command_rows, "s")
    if live:
        summary = live["summary"]
        lines.extend(
            [
                "## Live mesh",
                "",
                f"{summary['successful']}/{summary['turns']} turns succeeded across {summary['workerCount']} workers at {summary['turnsPerSecond']:.2f} turns/s.",
                "",
                "| Signal | p50 | p95 |",
                "| --- | ---: | ---: |",
                f"| Time to first token/output | {display_metric(summary['ttftP50MS'])} | {display_metric(summary['ttftP95MS'])} |",
                f"| Total turn | {display_metric(summary['totalP50MS'])} | {display_metric(summary['totalP95MS'])} |",
                "",
                "![Live latency](live-latency.svg)",
                "",
                "![Worker distribution](live-workers.svg)",
                "",
            ]
        )
        latency_rows = [
            (label, float(summary[key]), color)
            for label, key, color in (
                ("TTFT p50", "ttftP50MS", "#38bdf8"),
                ("TTFT p95", "ttftP95MS", "#0ea5e9"),
                ("total p50", "totalP50MS", "#a78bfa"),
                ("total p95", "totalP95MS", "#8b5cf6"),
            )
            if isinstance(summary.get(key), (int, float))
        ]
        svg_bars(
            output / "live-latency.svg",
            "Live mesh latency",
            latency_rows,
            "ms",
        )
        worker_rows = [(name, float(count), "#22c55e") for name, count in summary["workers"].items()]
        svg_bars(output / "live-workers.svg", "Successful turns by worker", worker_rows, "turns")
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
            "The scheduler tests cover eligibility, capacity-normalized distribution, concurrent reservation, stale reservation cleanup, and idempotent release. Real-model tests cover text/tool semantics, concurrency, bounded saturation, cancellation, and shutdown ordering. A live run additionally validates every UI stream and its worker attribution.",
            "A dirty run is useful while developing but must not be presented as qualification of a released revision.",
            "",
        ]
    )
    if failures or live_violations or live_missing:
        lines.extend(["## Failures", ""])
        for command in failures:
            lines.append(f"- `{command['name']}` exited {command['exitCode']}; see `{command['log']}`.")
        for violation in live_violations:
            lines.append(f"- Live threshold: {violation}.")
        if live_missing:
            lines.append("- The live driver did not produce its machine-readable artifact.")
        lines.append("")
    (output / "report.md").write_text("\n".join(lines), encoding="utf-8")
    return passed


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: render_report.py SESSION.json")
    raise SystemExit(0 if render(Path(sys.argv[1]).resolve()) else 1)
