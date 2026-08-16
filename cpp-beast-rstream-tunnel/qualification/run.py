#!/usr/bin/env python3
"""Produce reproducible qualification evidence for the Beast sample."""

import argparse
import hashlib
import html
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path


SAMPLE_ROOT = Path(__file__).resolve().parents[1]
REPOSITORY_ROOT = SAMPLE_ROOT.parent
sys.path.insert(0, str(SAMPLE_ROOT / "test"))

from runtime import (  # noqa: E402
    MAX_LATENCY_BUDGET_MS,
    P95_LATENCY_BUDGET_MS,
    REQUEST_COUNT,
    WORKER_COUNT,
    latency_summary,
    validate_latency_budget,
    validate_shutdown,
)


def command(*arguments):
    return subprocess.run(
        arguments,
        cwd=REPOSITORY_ROOT,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def render_svg(campaigns):
    width = 960
    height = 480
    left = 90
    top = 55
    chart_width = 820
    chart_height = 330
    ratios = [
        value / budget
        for campaign in campaigns
        for value, budget in (
            (campaign["p95_ms"], P95_LATENCY_BUDGET_MS),
            (campaign["max_ms"], MAX_LATENCY_BUDGET_MS),
        )
    ]
    ceiling = max(
        1.1,
        max(ratios) * 1.1,
    )
    scale = chart_height / ceiling
    group_width = chart_width / len(campaigns)
    bars = []
    labels = []
    for index, campaign in enumerate(campaigns):
        center = left + group_width * (index + 0.5)
        for offset, key, budget, color in (
            (-18, "p95_ms", P95_LATENCY_BUDGET_MS, "#1f77b4"),
            (18, "max_ms", MAX_LATENCY_BUDGET_MS, "#ff7f0e"),
        ):
            value = campaign[key]
            bar_height = value / budget * scale
            bars.append(
                f'<rect x="{center + offset - 14:.1f}" '
                f'y="{top + chart_height - bar_height:.1f}" width="28" '
                f'height="{bar_height:.1f}" rx="4" fill="{color}" />'
            )
            bars.append(
                f'<text x="{center + offset:.1f}" '
                f'y="{top + chart_height - bar_height - 8:.1f}" '
                f'text-anchor="middle" class="value">{value:.0f}</text>'
            )
        labels.append(
            f'<text x="{center:.1f}" y="{top + chart_height + 28}" '
            f'text-anchor="middle">{index + 1}</text>'
        )
    budget_y = top + chart_height - scale
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">
<style>text {{ font: 16px system-ui, sans-serif; fill: #1f2937; }} .title {{ font-size: 24px; font-weight: 700; }} .value {{ font-size: 13px; font-weight: 600; }}</style>
<rect width="100%" height="100%" fill="#ffffff" />
<text x="{left}" y="32" class="title">C++ Beast staging latency budget utilization</text>
<line x1="{left}" y1="{top + chart_height}" x2="{left + chart_width}" y2="{top + chart_height}" stroke="#9ca3af" />
<line x1="{left}" y1="{budget_y:.1f}" x2="{left + chart_width}" y2="{budget_y:.1f}" stroke="#dc2626" stroke-width="2" stroke-dasharray="8 6" />
<text x="{left + chart_width}" y="{budget_y - 8:.1f}" text-anchor="end" fill="#dc2626">100% of budget</text>
{''.join(bars)}
{''.join(labels)}
<rect x="{left}" y="435" width="16" height="16" rx="3" fill="#1f77b4" /><text x="{left + 24}" y="449">p95 / {P95_LATENCY_BUDGET_MS:.0f} ms</text>
<rect x="{left + 190}" y="435" width="16" height="16" rx="3" fill="#ff7f0e" /><text x="{left + 214}" y="449">maximum / {MAX_LATENCY_BUDGET_MS:.0f} ms</text>
<text x="{left + chart_width}" y="449" text-anchor="end">{REQUEST_COUNT} requests · {WORKER_COUNT} workers per campaign</text>
</svg>
"""


def render_report(manifest):
    rows = "\n".join(
        "| {index} | {p95_ms:.1f} | {max_ms:.1f} | {mean_ms:.1f} |".format(
            index=index,
            **campaign,
        )
        for index, campaign in enumerate(manifest["campaigns"], start=1)
    )
    aggregate = manifest["aggregate"]
    campaign_count = manifest["campaign_count"]
    campaign_label = "campaign" if campaign_count == 1 else "campaigns"
    return f"""# C++ Beast reference qualification

**Verdict: PASS** for commit `{html.escape(manifest['revision'])}` on the recorded staging path.

![Latency by campaign](latency.svg)

The gate built the native Boost.Beast server, verified idle shutdown, reused one external HTTP keep-alive connection across two requests, completed {campaign_count} {campaign_label} of {manifest['requests_per_campaign']} requests with {manifest['concurrency']} workers, and cancelled a deliberately incomplete HTTP read during `SIGINT`. Every response was byte-exact and every process exited without a runtime error.

| Campaign | p95 (ms) | maximum (ms) | mean (ms) |
| ---: | ---: | ---: | ---: |
{rows}

Across all {manifest['request_count']} measured requests, p95 was {aggregate['p95_ms']:.1f} ms, maximum was {aggregate['max_ms']:.1f} ms, and mean was {aggregate['mean_ms']:.1f} ms. The remote-staging gates are p95 ≤ {manifest['budgets']['p95_ms']:.0f} ms and maximum ≤ {manifest['budgets']['max_ms']:.0f} ms.

The chart expresses each measurement as utilization of its own budget so p95 and maximum remain visually comparable; bar labels are the measured milliseconds. This is reproducible regression evidence for this commit and network path, not a universal public SLO. The wide budgets are designed to detect serialization, stalled handshakes, and cancellation failures while tolerating normal WAN variance.

Machine-readable inputs, hashes, individual campaign values, and thresholds are in [`manifest.json`](manifest.json).
"""


def assert_private_data_absent(paths):
    hostname = os.uname().nodename
    home = Path.home()
    forbidden = {
        str(home),
        home.name,
        os.environ.get("USER", ""),
        hostname,
        hostname.split(".", maxsplit=1)[0],
    }
    forbidden.discard("")
    for path in paths:
        content = path.read_text(encoding="utf-8")
        for value in forbidden:
            if value in content:
                raise RuntimeError(f"private host data detected in {path.name}")


def require_clean_worktree(dirty, allow_dirty):
    if dirty and not allow_dirty:
        raise ValueError(
            "worktree is dirty; commit first or use --allow-dirty for diagnostics"
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--context",
        default=os.environ.get("RSTREAM_RUNTIME_CONTEXT"),
        help="non-production rstream context (or RSTREAM_RUNTIME_CONTEXT)",
    )
    parser.add_argument("--environment-label", default="staging")
    parser.add_argument("--campaigns", type=int, default=5)
    parser.add_argument("--cooldown-seconds", type=float, default=3.0)
    parser.add_argument(
        "--allow-dirty",
        action="store_true",
        help="allow diagnostic evidence from a dirty worktree",
    )
    parser.add_argument(
        "--binary",
        type=Path,
        default=SAMPLE_ROOT / "out/bin/cpp_beast_rstream_tunnel",
    )
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if not args.context:
        parser.error("--context or RSTREAM_RUNTIME_CONTEXT is required")
    if args.campaigns < 1:
        parser.error("--campaigns must be positive")
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error(f"binary not found: {binary}; run make build first")
    revision = command("git", "rev-parse", "HEAD")
    dirty = bool(command("git", "status", "--porcelain", "--untracked-files=all"))
    try:
        require_clean_worktree(dirty, args.allow_dirty)
    except ValueError as error:
        parser.error(str(error))
    output = args.output or (
        SAMPLE_ROOT
        / "qualification/results"
        / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    )
    output.mkdir(parents=True, exist_ok=False)
    validate_shutdown(str(binary), args.context, False)
    campaign_summaries = []
    all_latencies = []
    for index in range(args.campaigns):
        latencies = validate_shutdown(str(binary), args.context, True)
        all_latencies.extend(latencies)
        campaign_summaries.append(latency_summary(latencies))
        if index + 1 < args.campaigns:
            time.sleep(args.cooldown_seconds)
    aggregate = latency_summary(all_latencies)
    validate_latency_budget(aggregate)
    manifest = {
        "schema_version": 1,
        "verdict": "PASS",
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "revision": revision,
        "dirty": dirty,
        "environment": args.environment_label,
        "binary_sha256": sha256(binary),
        "campaign_count": args.campaigns,
        "requests_per_campaign": REQUEST_COUNT,
        "concurrency": WORKER_COUNT,
        "request_count": len(all_latencies),
        "cooldown_seconds": args.cooldown_seconds,
        "budgets": {
            "p95_ms": P95_LATENCY_BUDGET_MS,
            "max_ms": MAX_LATENCY_BUDGET_MS,
        },
        "campaigns": campaign_summaries,
        "aggregate": aggregate,
    }
    manifest_path = output / "manifest.json"
    svg_path = output / "latency.svg"
    report_path = output / "report.md"
    manifest_path.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    svg_path.write_text(render_svg(campaign_summaries), encoding="utf-8")
    report_path.write_text(render_report(manifest), encoding="utf-8")
    assert_private_data_absent((manifest_path, svg_path, report_path))
    print(f"PASS: qualification evidence written to {output}")


if __name__ == "__main__":
    main()
