#!/usr/bin/env python3

"""Measure an RFC 4571 framed RTP stream without changing its bytes."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import struct
import sys
import tempfile
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import BinaryIO


def extend_counter(value: int, reference: int | None, bits: int) -> int:
    if reference is None:
        return value
    modulus = 1 << bits
    base = reference & ~(modulus - 1)
    candidates = (base + value - modulus, base + value, base + value + modulus)
    return min(candidates, key=lambda candidate: abs(candidate - reference))


def compress_ranges(values: set[int]) -> list[list[int]]:
    if not values:
        return []
    ordered = sorted(values)
    ranges: list[list[int]] = []
    start = ordered[0]
    end = start
    for value in ordered[1:]:
        if value == end + 1:
            end = value
            continue
        ranges.append([start, end])
        start = value
        end = value
    ranges.append([start, end])
    return ranges


@dataclass
class RTPStream:
    ssrc: int
    packets: int = 0
    bytes: int = 0
    duplicates: int = 0
    out_of_order: int = 0
    max_reorder_distance: int = 0
    timestamp_regressions: int = 0
    highest_sequence: int | None = None
    highest_timestamp: int | None = None
    sequences: set[int] = field(default_factory=set)
    payload_types: set[int] = field(default_factory=set)

    def observe(self, packet: bytes, sequence: int, timestamp: int, payload_type: int) -> None:
        extended_sequence = extend_counter(sequence, self.highest_sequence, 16)
        extended_timestamp = extend_counter(timestamp, self.highest_timestamp, 32)
        self.packets += 1
        self.bytes += len(packet)
        self.payload_types.add(payload_type)
        if extended_sequence in self.sequences:
            self.duplicates += 1
        else:
            self.sequences.add(extended_sequence)
        if self.highest_sequence is not None and extended_sequence < self.highest_sequence:
            self.out_of_order += 1
            self.max_reorder_distance = max(
                self.max_reorder_distance, self.highest_sequence - extended_sequence
            )
        if self.highest_timestamp is not None and extended_timestamp < self.highest_timestamp:
            self.timestamp_regressions += 1
        if self.highest_sequence is None or extended_sequence > self.highest_sequence:
            self.highest_sequence = extended_sequence
        if self.highest_timestamp is None or extended_timestamp > self.highest_timestamp:
            self.highest_timestamp = extended_timestamp

    def report(self) -> dict[str, object]:
        minimum = min(self.sequences) if self.sequences else None
        maximum = max(self.sequences) if self.sequences else None
        span = maximum - minimum + 1 if minimum is not None and maximum is not None else 0
        return {
            "ssrc": self.ssrc,
            "packets": self.packets,
            "bytes": self.bytes,
            "uniquePackets": len(self.sequences),
            "duplicates": self.duplicates,
            "outOfOrderArrivals": self.out_of_order,
            "maxReorderDistancePackets": self.max_reorder_distance,
            "timestampRegressions": self.timestamp_regressions,
            "minimumExtendedSequence": minimum,
            "maximumExtendedSequence": maximum,
            "missingWithinObservedRange": span - len(self.sequences),
            "sequenceRanges": compress_ranges(self.sequences),
            "payloadTypes": sorted(self.payload_types),
        }


class RTPProbe:
    def __init__(self) -> None:
        self.framed_packets = 0
        self.framed_bytes = 0
        self.malformed_packets = 0
        self.streams: dict[int, RTPStream] = {}

    def observe(self, packet: bytes) -> None:
        self.framed_packets += 1
        self.framed_bytes += len(packet)
        if len(packet) < 12 or packet[0] >> 6 != 2:
            self.malformed_packets += 1
            return
        sequence = struct.unpack_from("!H", packet, 2)[0]
        timestamp = struct.unpack_from("!I", packet, 4)[0]
        ssrc = struct.unpack_from("!I", packet, 8)[0]
        payload_type = packet[1] & 0x7F
        stream = self.streams.setdefault(ssrc, RTPStream(ssrc))
        stream.observe(packet, sequence, timestamp, payload_type)

    def report(self) -> dict[str, object]:
        return {
            "generatedAt": datetime.now(timezone.utc).isoformat(),
            "framing": "RFC4571",
            "framedPackets": self.framed_packets,
            "framedPayloadBytes": self.framed_bytes,
            "malformedRtpPackets": self.malformed_packets,
            "streams": [self.streams[key].report() for key in sorted(self.streams)],
        }


def read_exact(source: BinaryIO, size: int) -> bytes:
    chunks: list[bytes] = []
    remaining = size
    while remaining:
        chunk = source.read(remaining)
        if not chunk:
            raise EOFError(f"truncated RFC 4571 frame: missing {remaining} bytes")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def copy_and_probe(source: BinaryIO, destination: BinaryIO, probe: RTPProbe) -> None:
    while True:
        header = source.read(2)
        if not header:
            return
        if len(header) != 2:
            raise EOFError("truncated RFC 4571 length prefix")
        size = struct.unpack("!H", header)[0]
        packet = read_exact(source, size)
        probe.observe(packet)
        destination.write(header)
        destination.write(packet)
        destination.flush()


def write_json_atomic(path: pathlib.Path, report: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(report, output, indent=2, sort_keys=True)
            output.write("\n")
        os.replace(temporary_name, path)
    except BaseException:
        try:
            os.unlink(temporary_name)
        except FileNotFoundError:
            pass
        raise


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=pathlib.Path)
    return parser.parse_args()


def main() -> int:
    arguments = parse_arguments()
    probe = RTPProbe()
    try:
        copy_and_probe(sys.stdin.buffer, sys.stdout.buffer, probe)
    except (BrokenPipeError, EOFError) as error:
        try:
            descriptor = os.open(os.devnull, os.O_WRONLY)
            os.dup2(descriptor, sys.stdout.fileno())
            os.close(descriptor)
        except OSError:
            pass
        print(f"RTP probe failed: {error}", file=sys.stderr)
        return 1
    finally:
        write_json_atomic(arguments.output, probe.report())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
