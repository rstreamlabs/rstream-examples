#!/usr/bin/env python3

"""Compare decoded video frames while tolerating omitted frame positions."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import sys


def frame_digests(path: pathlib.Path, frame_bytes: int) -> list[bytes]:
    data = path.read_bytes()
    if len(data) % frame_bytes:
        raise ValueError(
            f"{path} byte count {len(data)} is not a multiple of frame size {frame_bytes}"
        )
    return [
        hashlib.sha256(data[offset : offset + frame_bytes]).digest()
        for offset in range(0, len(data), frame_bytes)
    ]


def longest_common_subsequence(left: list[bytes], right: list[bytes]) -> int:
    previous = [0] * (len(right) + 1)
    for left_value in left:
        current = [0]
        for index, right_value in enumerate(right, start=1):
            if left_value == right_value:
                current.append(previous[index - 1] + 1)
            else:
                current.append(max(previous[index], current[-1]))
        previous = current
    return previous[-1]


def analyze(
    reference_path: pathlib.Path,
    candidate_path: pathlib.Path,
    frame_bytes: int,
    expected_frames: int,
    minimum_identical_percent: float,
) -> dict[str, object]:
    reference = frame_digests(reference_path, frame_bytes)
    candidate = frame_digests(candidate_path, frame_bytes)
    if len(reference) != expected_frames:
        raise ValueError(
            f"reference contains {len(reference)} frames, expected {expected_frames}"
        )
    if len(candidate) > expected_frames:
        raise ValueError(
            f"candidate contains {len(candidate)} frames, expected at most {expected_frames}"
        )
    identical = longest_common_subsequence(reference, candidate)
    percent = identical * 100 / expected_frames
    return {
        "passed": percent >= minimum_identical_percent,
        "referenceFrames": len(reference),
        "candidateFrames": len(candidate),
        "identicalFramesInOrder": identical,
        "missingOrAlteredReferenceFrames": len(reference) - identical,
        "alteredCandidateFrames": len(candidate) - identical,
        "identicalPercent": percent,
        "minimumIdenticalPercent": minimum_identical_percent,
    }


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--reference", required=True, type=pathlib.Path)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--frame-bytes", required=True, type=int)
    parser.add_argument("--expected-frames", required=True, type=int)
    parser.add_argument("--minimum-identical-percent", required=True, type=float)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--summary", required=True, type=pathlib.Path)
    arguments = parser.parse_args()
    if arguments.frame_bytes <= 0 or arguments.expected_frames <= 0:
        parser.error("frame size and expected frame count must be positive")
    if not 0 < arguments.minimum_identical_percent <= 100:
        parser.error("minimum identical percent must be in (0, 100]")
    return arguments


def main() -> int:
    arguments = parse_arguments()
    try:
        report = analyze(
            arguments.reference,
            arguments.candidate,
            arguments.frame_bytes,
            arguments.expected_frames,
            arguments.minimum_identical_percent,
        )
    except (OSError, ValueError) as error:
        print(f"frame comparison failed: {error}", file=sys.stderr)
        return 2
    arguments.output.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    verdict = "PASS" if report["passed"] else "FAIL"
    summary = (
        f"{verdict} video quality identical_frames="
        f"{report['identicalFramesInOrder']}/{report['referenceFrames']} "
        f"identical={report['identicalPercent']:.3f}% "
        f"candidate_frames={report['candidateFrames']} "
        f"altered_candidate_frames={report['alteredCandidateFrames']}\n"
    )
    arguments.summary.write_text(summary, encoding="utf-8")
    print(summary, end="")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
