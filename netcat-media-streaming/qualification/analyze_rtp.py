#!/usr/bin/env python3

"""Compare sender and receiver RTP probe reports."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys
from datetime import datetime, timezone


def range_count(ranges: list[list[int]]) -> int:
    return sum(end - start + 1 for start, end in ranges)


def intersection_count(left: list[list[int]], right: list[list[int]]) -> int:
    left_index = 0
    right_index = 0
    count = 0
    while left_index < len(left) and right_index < len(right):
        left_start, left_end = left[left_index]
        right_start, right_end = right[right_index]
        count += max(0, min(left_end, right_end) - max(left_start, right_start) + 1)
        if left_end < right_end:
            left_index += 1
        else:
            right_index += 1
    return count


def align_ranges(reference: list[list[int]], candidate: list[list[int]]) -> list[list[int]]:
    if not reference or not candidate:
        return candidate
    modulus = 1 << 16
    approximate_cycles = round((reference[0][0] - candidate[0][0]) / modulus)
    offsets = range(approximate_cycles - 2, approximate_cycles + 3)
    aligned = [
        [[start + offset * modulus, end + offset * modulus] for start, end in candidate]
        for offset in offsets
    ]
    return max(aligned, key=lambda ranges: intersection_count(reference, ranges))


def only_stream(report: dict[str, object], label: str) -> dict[str, object]:
    streams = report.get("streams")
    if not isinstance(streams, list) or len(streams) != 1:
        raise ValueError(f"{label} must contain exactly one RTP stream")
    stream = streams[0]
    if not isinstance(stream, dict):
        raise ValueError(f"{label} RTP stream is invalid")
    return stream


def analyze(
    sender_report: dict[str, object],
    receiver_report: dict[str, object],
    minimum_delivery_percent: float,
) -> dict[str, object]:
    sender = only_stream(sender_report, "sender")
    receiver = only_stream(receiver_report, "receiver")
    if sender.get("ssrc") != receiver.get("ssrc"):
        raise ValueError("sender and receiver SSRC values differ")
    sender_ranges = sender.get("sequenceRanges")
    receiver_ranges = receiver.get("sequenceRanges")
    if not isinstance(sender_ranges, list) or not isinstance(receiver_ranges, list):
        raise ValueError("RTP sequence ranges are missing")
    receiver_ranges = align_ranges(sender_ranges, receiver_ranges)
    sent = range_count(sender_ranges)
    received = range_count(receiver_ranges)
    delivered = intersection_count(sender_ranges, receiver_ranges)
    delivery_percent = delivered * 100 / sent if sent else 0.0
    malformed = int(receiver_report.get("malformedRtpPackets", 0))
    passed = (
        sent > 0
        and malformed == 0
        and delivery_percent >= minimum_delivery_percent
        and delivered == received
    )
    return {
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "passed": passed,
        "thresholds": {"minimumPacketDeliveryPercent": minimum_delivery_percent},
        "sender": {"uniquePackets": sent, "ssrc": sender.get("ssrc")},
        "receiver": {
            "uniquePackets": received,
            "deliveredSenderPackets": delivered,
            "missingSenderPackets": sent - delivered,
            "unexpectedPackets": received - delivered,
            "deliveryPercent": delivery_percent,
            "duplicates": receiver.get("duplicates"),
            "outOfOrderArrivals": receiver.get("outOfOrderArrivals"),
            "maxReorderDistancePackets": receiver.get("maxReorderDistancePackets"),
            "timestampRegressions": receiver.get("timestampRegressions"),
            "malformedRtpPackets": malformed,
        },
    }


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--sender", required=True, type=pathlib.Path)
    parser.add_argument("--receiver", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--summary", required=True, type=pathlib.Path)
    parser.add_argument("--minimum-delivery-percent", type=float, default=99.0)
    return parser.parse_args()


def main() -> int:
    arguments = parse_arguments()
    try:
        sender = json.loads(arguments.sender.read_text(encoding="utf-8"))
        receiver = json.loads(arguments.receiver.read_text(encoding="utf-8"))
        report = analyze(sender, receiver, arguments.minimum_delivery_percent)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"RTP analysis failed: {error}", file=sys.stderr)
        return 2
    arguments.output.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    receiver_report = report["receiver"]
    verdict = "PASS" if report["passed"] else "FAIL"
    summary = (
        f"{verdict} RTP packets={receiver_report['deliveredSenderPackets']}/"
        f"{report['sender']['uniquePackets']} "
        f"delivery={receiver_report['deliveryPercent']:.3f}% "
        f"missing={receiver_report['missingSenderPackets']} "
        f"duplicates={receiver_report['duplicates']} "
        f"out_of_order={receiver_report['outOfOrderArrivals']}\n"
    )
    arguments.summary.write_text(summary, encoding="utf-8")
    print(summary, end="")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
