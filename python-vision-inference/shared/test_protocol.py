from __future__ import annotations

import asyncio
import json
import math
import unittest

from shared.protocol import (
    MAX_HEADER_SIZE,
    MAX_MESSAGE_SIZE,
    read_message,
    send_message,
)


class MemoryStream:
    def __init__(self, incoming: bytes = b"") -> None:
        self.reader = asyncio.StreamReader()
        self.reader.feed_data(incoming)
        self.reader.feed_eof()
        self.written = bytearray()
        self.drains = 0

    async def readexactly(self, count: int) -> bytes:
        return await self.reader.readexactly(count)

    def write(self, data: bytes) -> None:
        self.written.extend(data)

    async def drain(self) -> None:
        self.drains += 1


def framed(header: object, payload: bytes = b"") -> bytes:
    header_bytes = json.dumps(header).encode()
    total = 4 + len(header_bytes) + len(payload)
    return (
        total.to_bytes(4, "big")
        + len(header_bytes).to_bytes(4, "big")
        + header_bytes
        + payload
    )


class ProtocolTest(unittest.IsolatedAsyncioTestCase):
    async def test_round_trip_preserves_header_and_binary_payload(self) -> None:
        writer = MemoryStream()
        await send_message(writer, {"type": "frame", "frame_id": 42}, b"\x00\xffdata")
        self.assertEqual(writer.drains, 1)
        result = await read_message(MemoryStream(bytes(writer.written)))
        self.assertEqual(result, ({"type": "frame", "frame_id": 42}, b"\x00\xffdata"))

    async def test_clean_eof_returns_none_but_truncation_is_an_error(self) -> None:
        self.assertIsNone(await read_message(MemoryStream()))
        for payload in (b"\x00", framed({"type": "frame"})[:-1]):
            with self.subTest(length=len(payload)):
                with self.assertRaisesRegex(ValueError, "truncated message"):
                    await read_message(MemoryStream(payload))

    async def test_rejects_impossible_lengths_before_json_decode(self) -> None:
        for payload in (
            (3).to_bytes(4, "big") + b"abc",
            (4).to_bytes(4, "big") + (1).to_bytes(4, "big"),
            (MAX_MESSAGE_SIZE + 1).to_bytes(4, "big"),
        ):
            with self.subTest(payload=payload[:8].hex()):
                with self.assertRaises(ValueError):
                    await read_message(MemoryStream(payload))

    async def test_rejects_non_object_header(self) -> None:
        with self.assertRaisesRegex(ValueError, "JSON object"):
            await read_message(MemoryStream(framed(["not", "an", "object"])))

    async def test_rejects_non_finite_json_on_send_and_receive(self) -> None:
        stream = MemoryStream()
        with self.assertRaises(ValueError):
            await send_message(stream, {"infer_ms": math.nan})
        self.assertEqual(stream.written, b"")
        raw_header = b'{"infer_ms":NaN}'
        payload = (
            (4 + len(raw_header)).to_bytes(4, "big")
            + len(raw_header).to_bytes(4, "big")
            + raw_header
        )
        with self.assertRaisesRegex(ValueError, "non-finite JSON"):
            await read_message(MemoryStream(payload))

    async def test_rejects_duplicate_keys(self) -> None:
        raw_header = b'{"type":"frame","type":"result"}'
        payload = (
            (4 + len(raw_header)).to_bytes(4, "big")
            + len(raw_header).to_bytes(4, "big")
            + raw_header
        )
        with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
            await read_message(MemoryStream(payload))

    async def test_rejects_oversized_header_before_json_decode(self) -> None:
        total = MAX_HEADER_SIZE + 5
        payload = (
            total.to_bytes(4, "big")
            + (MAX_HEADER_SIZE + 1).to_bytes(4, "big")
            + b"x" * (MAX_HEADER_SIZE + 1)
        )
        with self.assertRaisesRegex(ValueError, "header too large"):
            await read_message(MemoryStream(payload))

    async def test_send_rejects_oversized_message_without_writing(self) -> None:
        stream = MemoryStream()
        with self.assertRaisesRegex(ValueError, "message too large"):
            await send_message(stream, {"type": "frame"}, b"x" * MAX_MESSAGE_SIZE)
        self.assertEqual(stream.written, b"")
        self.assertEqual(stream.drains, 0)


if __name__ == "__main__":
    unittest.main()
