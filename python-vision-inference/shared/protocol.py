# See LICENSE file in the project root for license information.

"""Message framing shared by the device and the worker.

Each message carries a JSON header and an optional binary payload:
a 4-byte big-endian total length, a 4-byte big-endian header length,
the JSON header, then the payload bytes.

Three header types flow over a session. The worker opens with ``hello``,
advertising its model, input size, and supported codecs, so the device can
choose its encoding policy. The device then sends ``frame`` headers with the
encoded image as payload, and the worker answers with ``result`` headers
echoing the ``frame_id`` and reporting the inference duration as an interval,
never as an absolute timestamp, so no clock comparison ever happens across
machines.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Mapping

import rstream

MAX_MESSAGE_SIZE = 16 * 1024 * 1024
MAX_HEADER_SIZE = 4 * 1024 * 1024


def _reject_json_constant(value: str) -> None:
    raise ValueError(f"non-finite JSON value {value!r} is not allowed")


def _reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key {key!r} is not allowed")
        result[key] = value
    return result


async def send_message(
    stream: rstream.RstreamStream,
    header: Mapping[str, object],
    payload: bytes = b"",
) -> None:
    header_bytes = json.dumps(
        header,
        allow_nan=False,
        separators=(",", ":"),
    ).encode()
    if len(header_bytes) > MAX_HEADER_SIZE:
        raise ValueError(f"message header too large: {len(header_bytes)} bytes")
    total = 4 + len(header_bytes) + len(payload)
    if total > MAX_MESSAGE_SIZE:
        raise ValueError(f"message too large: {total} bytes")
    stream.write(total.to_bytes(4, "big"))
    stream.write(len(header_bytes).to_bytes(4, "big"))
    stream.write(header_bytes)
    if payload:
        stream.write(payload)
    await stream.drain()


async def read_message(
    stream: rstream.RstreamStream,
) -> tuple[dict[str, object], bytes] | None:
    try:
        prefix = await stream.readexactly(4)
    except asyncio.IncompleteReadError as error:
        if not error.partial:
            return None
        raise ValueError("truncated message length prefix") from error
    total = int.from_bytes(prefix, "big")
    if total < 4:
        raise ValueError(f"message too small: {total} bytes")
    if total > MAX_MESSAGE_SIZE:
        raise ValueError(f"message too large: {total} bytes")
    try:
        body = await stream.readexactly(total)
    except asyncio.IncompleteReadError as error:
        raise ValueError(
            f"truncated message body: expected {total} bytes, got {len(error.partial)}"
        ) from error
    header_length = int.from_bytes(body[:4], "big")
    if header_length > MAX_HEADER_SIZE:
        raise ValueError(f"message header too large: {header_length} bytes")
    if header_length > total - 4:
        raise ValueError(
            f"header length {header_length} exceeds message body {total - 4}"
        )
    header = json.loads(
        body[4 : 4 + header_length],
        parse_constant=_reject_json_constant,
        object_pairs_hook=_reject_duplicate_keys,
    )
    if not isinstance(header, dict):
        raise ValueError("message header must be a JSON object")
    return header, body[4 + header_length :]
