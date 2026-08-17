#!/usr/bin/env python3

"""Render compact, dependency-free qualification evidence."""

from __future__ import annotations

import argparse
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
            result["deliveredPackets"] = rtp["receiver"]["deliveredSenderPackets"]
            result["sentPackets"] = rtp["sender"]["uniquePackets"]
            result["missingPackets"] = rtp["receiver"]["missingSenderPackets"]
            result["duplicatePackets"] = rtp["receiver"]["duplicates"]
            result["outOfOrderPackets"] = rtp["receiver"]["outOfOrderArrivals"]
            result["packetThresholdPercent"] = rtp["thresholds"][
                "minimumPacketDeliveryPercent"
            ]
        quality_path = directory / "frame-comparison.json"
        if quality_path.exists():
            quality = load_json(quality_path)
            result["identicalFramesPercent"] = quality["identicalPercent"]
            result["identicalFrames"] = quality["identicalFramesInOrder"]
            result["referenceFrames"] = quality["referenceFrames"]
            result["identicalFramesThresholdPercent"] = quality[
                "minimumIdenticalPercent"
            ]
        decoded_path = directory / "frames.json"
        if decoded_path.exists():
            decoded = load_json(decoded_path)
            result["decodedFramesPercent"] = decoded["deliveryPercent"]
        summary_path = directory / "summary.txt"
        if summary_path.exists():
            result["summary"] = "<br />".join(
                line.strip()
                for line in summary_path.read_text(encoding="utf-8").splitlines()
                if line.strip()
            )
        results.append(result)
    return results


def render_markdown(results: list[dict[str, object]], output: pathlib.Path) -> None:
    lines = [
        "# Netcat media qualification record",
        "",
        "## Method",
        "",
        "Every scenario starts from a finite 300-frame source. Reliable and RTSP streams decode to raw I420 and are checked frame by frame against the source. Datagram scenarios also parse the RFC 4571-framed RTP stream, account for every sequence number, and compare decoded frames with the reference in order. The repair scenario removes one percent of RTP packets before rstream and checks every RTCP/NACK lookup together with the decoded output.",
        "",
        "## Acceptance gates",
        "",
        "- Reliable and RTSP paths decode all 300 frames.",
        "- Clean best-effort RTP delivers at least 99% of packets and 90% of reference-identical frames; guaranteed datagrams require 100% of both.",
        "- The injected-loss path decodes all 300 frames and satisfies every NACK lookup inside the repair window.",
        "- RTP analysis rejects malformed packets, timestamp regressions, and values outside the profile's duplicate or reordering budget.",
        "- Unknown warning or error lines and incomplete process teardown fail the run.",
        "",
        "## Recorded results",
        "",
        "| Scenario | Observed result | Revision | Working tree |",
        "| --- | --- | --- | --- |",
    ]
    for result in results:
        lines.append(
            "| {scenario} | {summary} | `{revision}` | {dirty} |".format(
                scenario=str(result["scenario"]),
                summary=str(result.get("summary", "No analyzer summary")),
                revision=str(result["revision"])[:12],
                dirty="dirty" if result["dirty"] else "clean",
            )
        )
    lines.extend(
        [
            "",
            "Best-effort RTP, guaranteed datagrams, reliable byte streams, RTCP repair, and RTSP bridging remain separate verdicts because they exercise different delivery semantics. A clean best-effort run establishes fidelity on the recorded path; the injected-loss scenario establishes application-level repair.",
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
    render_markdown(results, arguments.root / "report.md")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
