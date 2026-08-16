#!/usr/bin/env python3

import argparse
import json
import pathlib
import re
import sys


SEVERITY = re.compile(r"\b(?:WARN|ERROR|CRITICAL|FATAL|PANIC)\b")
RULES = (
    (
        "gstreamer_fd_teardown",
        re.compile(r"GST_POLL .*couldn't find fd"),
        16,
    ),
    (
        "mpegts_latency_probe",
        re.compile(r"aggregator .*Latency query failed"),
        8,
    ),
    (
        "rtp_sender_running_time",
        re.compile(r"rtpsession .*Can't determine running time"),
        4,
    ),
    (
        "rtp_expired_nack",
        re.compile(r"rtpsession .*Removing [0-9]+ expired NACKS"),
        16,
    ),
)


def analyze(root):
    counts = {name: 0 for name, _, _ in RULES}
    unknown = []
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.suffix not in {".log", ".stderr"}:
            continue
        for line_number, line in enumerate(
            path.read_text(encoding="utf-8", errors="replace").splitlines(), 1
        ):
            if not SEVERITY.search(line):
                continue
            for name, pattern, _ in RULES:
                if pattern.search(line):
                    counts[name] += 1
                    break
            else:
                unknown.append(
                    {
                        "path": str(path.relative_to(root)),
                        "line": line_number,
                        "message": line[:500],
                    }
                )
    exceeded = [
        {"name": name, "count": counts[name], "maximum": maximum}
        for name, _, maximum in RULES
        if counts[name] > maximum
    ]
    return {
        "passed": not unknown and not exceeded,
        "acceptedWarnings": counts,
        "exceededWarningCeilings": exceeded,
        "unknownSeverities": unknown,
    }


def markdown(result):
    verdict = "PASS" if result["passed"] else "FAIL"
    lines = [f"# Qualification log quality — {verdict}", "", "| Class | Count |", "| --- | ---: |"]
    lines.extend(
        f"| {name} | {count} |"
        for name, count in result["acceptedWarnings"].items()
    )
    lines.extend(
        [
            "",
            f"Unknown warning or error lines: {len(result['unknownSeverities'])}.",
            f"Exceeded warning ceilings: {len(result['exceededWarningCeilings'])}.",
            "",
        ]
    )
    return "\n".join(lines)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--summary", type=pathlib.Path, required=True)
    return parser.parse_args()


def main():
    args = parse_args()
    result = analyze(args.root)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    args.summary.write_text(markdown(result), encoding="utf-8")
    if result["passed"]:
        print("PASS qualification logs contain only bounded known warnings")
        return 0
    print(
        "FAIL qualification logs contain unknown or excessive warnings",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
