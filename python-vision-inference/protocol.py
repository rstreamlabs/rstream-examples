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


async def send_message(
    stream: rstream.RstreamStream,
    header: Mapping[str, object],
    payload: bytes = b"",
) -> None:
    header_bytes = json.dumps(header).encode()
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
        total = int.from_bytes(await stream.readexactly(4), "big")
        if total > MAX_MESSAGE_SIZE:
            raise ValueError(f"message too large: {total} bytes")
        body = await stream.readexactly(total)
    except asyncio.IncompleteReadError:
        return None
    header_length = int.from_bytes(body[:4], "big")
    header = json.loads(body[4 : 4 + header_length])
    if not isinstance(header, dict):
        raise ValueError("message header must be a JSON object")
    return header, body[4 + header_length :]
