#!/usr/bin/env python3

import argparse
import hashlib
import json
import math
import pathlib
import sys


def analyze(path, frame_bytes, expected_frames, minimum_percent):
    data = path.read_bytes()
    if len(data) % frame_bytes:
        raise ValueError(
            f"decoded byte count {len(data)} is not a multiple of frame size {frame_bytes}"
        )
    frame_count = len(data) // frame_bytes
    if frame_count > expected_frames:
        raise ValueError(
            f"decoded {frame_count} frames, expected at most {expected_frames}"
        )
    digests = [
        hashlib.sha256(data[offset : offset + frame_bytes]).digest()
        for offset in range(0, len(data), frame_bytes)
    ]
    minimum_frames = math.ceil(expected_frames * minimum_percent / 100)
    consecutive_duplicates = sum(
        previous == current for previous, current in zip(digests, digests[1:])
    )
    return {
        "passed": frame_count >= minimum_frames and consecutive_duplicates == 0,
        "decodedFrames": frame_count,
        "expectedFrames": expected_frames,
        "minimumFrames": minimum_frames,
        "deliveryPercent": frame_count / expected_frames * 100,
        "uniqueFrames": len(set(digests)),
        "consecutiveDuplicates": consecutive_duplicates,
        "decodedBytes": len(data),
    }


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("path", type=pathlib.Path)
    parser.add_argument("--frame-bytes", type=int, required=True)
    parser.add_argument("--expected-frames", type=int, required=True)
    parser.add_argument("--minimum-percent", type=int, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.frame_bytes <= 0 or args.expected_frames <= 0:
        parser.error("frame size and expected frame count must be positive")
    if args.minimum_percent <= 0 or args.minimum_percent > 100:
        parser.error("minimum percent must be between 1 and 100")
    return args


def main():
    args = parse_args()
    try:
        result = analyze(
            args.path,
            args.frame_bytes,
            args.expected_frames,
            args.minimum_percent,
        )
    except (OSError, ValueError) as error:
        print(f"frame analysis failed: {error}", file=sys.stderr)
        return 2
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, sort_keys=True))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
