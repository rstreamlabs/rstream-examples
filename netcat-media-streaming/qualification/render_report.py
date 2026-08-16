#!/usr/bin/env python3

"""Render compact, dependency-free qualification evidence."""

from __future__ import annotations

import argparse
import html
import json
import pathlib


def load_json(path: pathlib.Path) -> dict[str, object]:
    return json.loads(path.read_text(encoding="utf-8"))


def collect(root: pathlib.Path) -> list[dict[str, object]]:
    results: list[dict[str, object]] = []
    for directory in sorted(path for path in root.iterdir() if path.is_dir()):
        manifest_path = directory / "manifest.json"
        if not manifest_path.exists():
            continue
        manifest = load_json(manifest_path)
        result: dict[str, object] = {
            "scenario": manifest.get("scenario", directory.name),
            "revision": manifest.get("git", {}).get("revision", "unknown"),
            "dirty": manifest.get("git", {}).get("dirty", True),
        }
        rtp_path = directory / "rtp-analysis.json"
        if rtp_path.exists():
            rtp = load_json(rtp_path)
            result["packetDeliveryPercent"] = rtp["receiver"]["deliveryPercent"]
            result["packetThresholdPercent"] = rtp["thresholds"][
                "minimumPacketDeliveryPercent"
            ]
        quality_path = directory / "frame-comparison.json"
        if quality_path.exists():
            quality = load_json(quality_path)
            result["identicalFramesPercent"] = quality["identicalPercent"]
            result["identicalFramesThresholdPercent"] = quality[
                "minimumIdenticalPercent"
            ]
        decoded_path = directory / "frames.json"
        if decoded_path.exists():
            decoded = load_json(decoded_path)
            result["decodedFramesPercent"] = decoded["deliveryPercent"]
        results.append(result)
    return results


def render_bars(
    title: str,
    rows: list[tuple[str, float, float | None]],
    output: pathlib.Path,
) -> None:
    width = 900
    left = 260
    right = 120
    top = 80
    row_height = 72
    chart_width = width - left - right
    height = top + max(1, len(rows)) * row_height + 50
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="#111827" rx="16"/>',
        f'<text x="32" y="42" fill="#f9fafb" font-family="system-ui,sans-serif" font-size="24" font-weight="700">{html.escape(title)}</text>',
    ]
    for index, (label, value, threshold) in enumerate(rows):
        y = top + index * row_height
        bar_width = max(0.0, min(100.0, value)) * chart_width / 100
        passed = threshold is None or value >= threshold
        color = "#22c55e" if passed else "#ef4444"
        parts.extend(
            [
                f'<text x="32" y="{y + 23}" fill="#e5e7eb" font-family="system-ui,sans-serif" font-size="17">{html.escape(label)}</text>',
                f'<rect x="{left}" y="{y}" width="{chart_width}" height="28" fill="#374151" rx="6"/>',
                f'<rect x="{left}" y="{y}" width="{bar_width:.2f}" height="28" fill="{color}" rx="6"/>',
                f'<text x="{left + chart_width + 12}" y="{y + 21}" fill="#f9fafb" font-family="ui-monospace,monospace" font-size="15">{value:.3f}%</text>',
            ]
        )
        if threshold is not None:
            threshold_x = left + threshold * chart_width / 100
            parts.append(
                f'<line x1="{threshold_x:.2f}" y1="{y - 5}" x2="{threshold_x:.2f}" y2="{y + 33}" stroke="#fbbf24" stroke-width="3"/>'
            )
    if not rows:
        parts.append(
            '<text x="32" y="100" fill="#9ca3af" font-family="system-ui,sans-serif" font-size="17">No applicable measurements</text>'
        )
    parts.append("</svg>\n")
    output.write_text("\n".join(parts), encoding="utf-8")


def render_markdown(results: list[dict[str, object]], output: pathlib.Path) -> None:
    lines = [
        "# Netcat media qualification evidence",
        "",
        "![RTP packet delivery](packet-delivery.svg)",
        "",
        "![Reference-identical decoded frames](video-quality.svg)",
        "",
        "| Scenario | RTP delivery | Identical video frames | Revision | Working tree |",
        "| --- | ---: | ---: | --- | --- |",
    ]
    for result in results:
        packet = result.get("packetDeliveryPercent")
        quality = result.get("identicalFramesPercent")
        lines.append(
            "| {scenario} | {packet} | {quality} | `{revision}` | {dirty} |".format(
                scenario=str(result["scenario"]),
                packet=f"{packet:.3f}%" if isinstance(packet, (float, int)) else "—",
                quality=f"{quality:.3f}%" if isinstance(quality, (float, int)) else "—",
                revision=str(result["revision"])[:12],
                dirty="dirty" if result["dirty"] else "clean",
            )
        )
    lines.extend(
        [
            "",
            "The amber marker is the declared acceptance threshold; red bars are below it. Best-effort, guaranteed-delivery and RTCP profiles intentionally remain separate because they make different latency and delivery trade-offs.",
            "",
            "Regenerate with `./qualification/run.sh`. Only evidence produced from a clean, pinned working tree is suitable for publication.",
            "",
        ]
    )
    output.write_text("\n".join(lines), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    arguments = parser.parse_args()
    results = collect(arguments.root)
    packet_rows = [
        (
            str(result["scenario"]),
            float(result["packetDeliveryPercent"]),
            float(result["packetThresholdPercent"]),
        )
        for result in results
        if "packetDeliveryPercent" in result
    ]
    quality_rows = [
        (
            str(result["scenario"]),
            float(result["identicalFramesPercent"]),
            float(result["identicalFramesThresholdPercent"]),
        )
        for result in results
        if "identicalFramesPercent" in result
    ]
    render_bars("RTP packet delivery", packet_rows, arguments.root / "packet-delivery.svg")
    render_bars(
        "Reference-identical decoded frames",
        quality_rows,
        arguments.root / "video-quality.svg",
    )
    render_markdown(results, arguments.root / "report.md")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
