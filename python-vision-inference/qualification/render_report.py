#!/usr/bin/env python3
"""Render Markdown and dependency-free SVG for a Vision evidence session."""

from __future__ import annotations

import html
import json
import math
import sys
from pathlib import Path


def svg_bars(
    path: Path,
    title: str,
    rows: list[tuple[str, float, str]],
    unit: str,
) -> None:
    width = 900
    left = 260
    right = 170
    row_height = 38
    height = 100 + row_height * max(1, len(rows))
    maximum = max((value for _, value, _ in rows), default=1) or 1
    scale = (width - left - right) / maximum
    lines = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" '
        f'height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="#111827"/>',
        f'<text x="24" y="38" fill="#f9fafb" font-family="sans-serif" '
        f'font-size="22" font-weight="700">{html.escape(title)}</text>',
    ]
    for index, (label, value, color) in enumerate(rows):
        y = 70 + index * row_height
        bar_width = max(1, value * scale)
        lines.extend(
            [
                f'<text x="24" y="{y + 18}" fill="#d1d5db" '
                f'font-family="sans-serif" font-size="14">'
                f'{html.escape(label)}</text>',
                f'<rect x="{left}" y="{y}" width="{bar_width:.1f}" '
                f'height="22" rx="4" fill="{color}"/>',
                f'<text x="{left + bar_width + 10:.1f}" y="{y + 17}" '
                f'fill="#f9fafb" font-family="sans-serif" font-size="14">'
                f'{value:.2f} {html.escape(unit)}</text>',
            ]
        )
    lines.append("</svg>")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def render(session_path: Path) -> bool:
    session = json.loads(session_path.read_text(encoding="utf-8"))
    output = session_path.parent
    commands = session["commands"]
    failures = [command for command in commands if command["exitCode"] != 0]
    benchmark = None
    benchmark_path = output / "model-benchmark.json"
    if benchmark_path.is_file():
        benchmark = json.loads(benchmark_path.read_text(encoding="utf-8"))
    live = load_optional_json(output / "live-mesh.json")
    transport = load_optional_json(output / "transport-profile.json")
    regional = load_optional_json(output / "regional-routing.json")
    artifacts = [
        artifact
        for artifact in (benchmark, live, transport, regional)
        if artifact
    ]
    violations = [
        str(violation)
        for artifact in artifacts
        for violation in artifact.get("violations", [])
    ]
    secret_findings = session.get("secretScanFindings", 0)
    personal_findings = session.get("personalDataFindings", 0)
    passed = not failures and benchmark is not None and not violations
    passed = passed and secret_findings == 0 and personal_findings == 0
    repository = session["manifest"]["repository"]
    model = session["manifest"]["model"]
    media = session["manifest"]["media"]
    parameters = session["manifest"].get("parameters", {})
    performance_gates = configured_performance_gates(parameters)
    verdict_scope = (
        "configured functional and performance gates"
        if performance_gates
        else "functional gates; performance observational"
    )
    lines = [
        "# Distributed Vision qualification report",
        "",
        f"**Verdict: {'PASS' if passed else 'FAIL'} — {verdict_scope}**",
        "",
        "Scope: local protocol, lifecycle, real model, and reference media"
        + (
            ", plus deployed rstream mesh transit and failure handling."
            if live
            else "."
        ),
        f"Revision: `{repository['revision']}` "
        f"({'dirty diagnostic run' if repository['dirty'] else 'clean worktree'}).",
        f"Model: `{model['name']}` · SHA-256 `{model['sha256']}`.",
        f"Media: `{media['name']}` · SHA-256 `{media['sha256']}`.",
        "",
        "Performance gates: "
        + (
            "; ".join(performance_gates) + "."
            if performance_gates
            else "none configured. Latency and throughput are measurements, "
            "not an SLO pass."
        ),
        "",
        "## Automated gates",
        "",
        "| Phase | Result | Wall time | Raw log |",
        "| --- | --- | ---: | --- |",
    ]
    for command in commands:
        result = "PASS" if command["exitCode"] == 0 else f"FAIL ({command['exitCode']})"
        lines.append(
            f"| {command['name']} | {result} | "
            f"{command['wallSeconds']:.3f} s | "
            f"[{command['log']}]({command['log']}) |"
        )
    command_rows = [
        (
            command["name"],
            float(command["wallSeconds"]),
            "#22c55e" if command["exitCode"] == 0 else "#ef4444",
        )
        for command in commands
    ]
    svg_bars(
        output / "commands.svg",
        "Qualification phase duration",
        command_rows,
        "s",
    )
    lines.extend(["", "![Command duration](commands.svg)", ""])
    if benchmark:
        inference = benchmark["inferenceMS"]
        lines.extend(
            [
                "## Model and media baseline",
                "",
                f"Accelerator: **{benchmark['accelerator']}** "
                f"(`{benchmark['device']}`). Reference payload: "
                f"{benchmark['payloadBytes']} bytes.",
                "",
                "| Signal | Result |",
                "| --- | ---: |",
                f"| Detections on reference frame | {benchmark['detectionCount']} |",
                f"| Inference p50 | {inference['p50']:.2f} ms |",
                f"| Inference p95 | {inference['p95']:.2f} ms |",
                f"| Sequential throughput | "
                f"{benchmark['sequentialThroughputFPS']:.2f} fps |",
                f"| Serialized concurrent throughput | "
                f"{benchmark['concurrentThroughputFPS']:.2f} fps |",
                "",
            ]
        )
        latency_rows = [
            ("inference min", float(inference["min"]), "#38bdf8"),
            ("inference p50", float(inference["p50"]), "#0ea5e9"),
            ("inference p95", float(inference["p95"]), "#a78bfa"),
            ("inference max", float(inference["max"]), "#8b5cf6"),
        ]
        svg_bars(
            output / "model-latency.svg",
            "Reference model inference latency",
            latency_rows,
            "ms",
        )
        lines.extend(["![Model latency](model-latency.svg)", ""])
    if live:
        round_trip = live["roundTripMS"]
        loopback = live["loopback"]
        lines.extend(
            [
                "## Live mesh and failure handling",
                "",
                "Workers registered in: "
                f"`{', '.join(sorted(set(live['workerEngineRegions'].values())))}`. "
                f"Reference payload: {live['referencePayloadBytes']} bytes.",
                "",
                "| Signal | Result |",
                "| --- | ---: |",
                f"| Frames checked | {live['frames']} |",
                f"| Loopback p95 | {loopback['roundTripP95MS']:.2f} ms |",
                f"| rstream p50 | {round_trip['p50']:.2f} ms |",
                f"| rstream p95 | {round_trip['p95']:.2f} ms |",
                f"| Capacity rejection | {live['capacityRejectionMS']:.2f} ms |",
                f"| Capacity recovery | {live['capacityRecoveryMS']:.2f} ms |",
                f"| Abrupt failure detection | {live['failureDetectionMS']:.2f} ms |",
                f"| End-to-end failover | {live['failoverMS']:.2f} ms |",
                "",
            ]
        )
        svg_bars(
            output / "live-latency.svg",
            "Live Vision latency and failover",
            [
                ("loopback RTT p95", float(loopback["roundTripP95MS"]), "#38bdf8"),
                ("rstream RTT p50", float(round_trip["p50"]), "#22c55e"),
                ("rstream RTT p95", float(round_trip["p95"]), "#a78bfa"),
                ("failure detection", float(live["failureDetectionMS"]), "#f59e0b"),
                ("failover", float(live["failoverMS"]), "#ef4444"),
            ],
            "ms",
        )
        lines.extend(["![Live latency](live-latency.svg)", ""])
    if transport:
        lines.extend(
            [
                "## Transport scaling",
                "",
                "The framed echo verifies byte equality and shows how payload size "
                "changes round-trip latency on the same path.",
                "",
                "| Payload | Loopback p95 | rstream p50 | rstream p95 |",
                "| ---: | ---: | ---: | ---: |",
            ]
        )
        for row in transport["rows"]:
            lines.append(
                f"| {row['bytes']} B | {row['loopbackP95MS']:.2f} ms | "
                f"{row['rstreamP50MS']:.2f} ms | {row['rstreamP95MS']:.2f} ms |"
            )
        lines.append("")
    if regional:
        observations = regional["observations"]
        lines.extend(
            [
                "## Regional worker selection",
                "",
                "Both candidates reported equal capacity. The legacy order would "
                f"have selected `{regional['legacyChoice']}`; the measured "
                f"tie-break selected `{regional['latencyAwareChoice']}` and saved "
                f"{regional['measuredMedianRTTGainMS']:.2f} ms of median round-trip "
                "latency in this run.",
                "",
                "| Worker | Engine region | Establishment | RTT p50 | RTT p95 |",
                "| --- | --- | ---: | ---: | ---: |",
            ]
        )
        region_rows = []
        for name, observation in observations.items():
            lines.append(
                f"| `{name}` | {observation['engineRegion']} | "
                f"{observation['establishmentMS']:.2f} ms | "
                f"{observation['roundTripP50MS']:.2f} ms | "
                f"{observation['roundTripP95MS']:.2f} ms |"
            )
            region_rows.append(
                (
                    str(observation["engineRegion"]),
                    float(observation["roundTripP50MS"]),
                    "#22c55e"
                    if name == regional["latencyAwareChoice"]
                    else "#ef4444",
                )
            )
        lines.append("")
        svg_bars(
            output / "regional-routing.svg",
            "Equal-capacity worker RTT by region",
            region_rows,
            "ms",
        )
        lines.extend(["![Regional routing](regional-routing.svg)", ""])
    lines.extend(
        [
            "## Integrity and interpretation",
            "",
            "Cancellation stress verifies that a cancelled executor call cannot "
            "release the model mutex while YOLO still owns the model. Admission, "
            "protocol framing, malformed results, capture teardown, routing, and "
            "registry recovery have independent regression tests.",
            (
                "The configured performance budgets are enforced in this verdict."
                if performance_gates
                else "This verdict proves functional integrity only; choose and "
                "supply environment-specific budgets before making an SLO claim."
            ),
            "A dirty run is diagnostic and must not be presented as qualification "
            "of a released revision.",
            "",
        ]
    )
    if (
        failures
        or violations
        or benchmark is None
        or secret_findings
        or personal_findings
    ):
        lines.extend(["## Failures", ""])
        for command in failures:
            lines.append(
                f"- `{command['name']}` exited {command['exitCode']}; "
                f"see `{command['log']}`."
            )
        for violation in violations:
            lines.append(f"- Benchmark threshold: {violation}.")
        if benchmark is None:
            lines.append("- The model benchmark did not produce its JSON evidence.")
        if secret_findings:
            lines.append("- Credential-shaped evidence was redacted.")
        if personal_findings:
            lines.append("- Local-profile data remains in the evidence.")
        lines.append("")
    (output / "report.md").write_text("\n".join(lines), encoding="utf-8")
    return passed


def configured_performance_gates(parameters: dict[str, object]) -> list[str]:
    definitions = (
        ("maxP95MS", "model p95 <= {value} ms"),
        ("minThroughputFPS", "model throughput >= {value} fps"),
        ("maxLiveP95MS", "live RTT p95 <= {value} ms"),
        ("maxFailoverMS", "failover <= {value} ms"),
        (
            "maxTransportOverheadP95MS",
            "transport overhead p95 <= {value} ms",
        ),
    )
    result = []
    for key, label in definitions:
        value = parameters.get(key)
        if (
            isinstance(value, (int, float))
            and not isinstance(value, bool)
            and math.isfinite(value)
            and value > 0
        ):
            result.append(label.format(value=value))
    return result


def load_optional_json(path: Path) -> dict[str, object] | None:
    if not path.is_file():
        return None
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected an object in {path}")
    return value


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: render_report.py SESSION.json")
    raise SystemExit(0 if render(Path(sys.argv[1]).resolve()) else 1)
