# See LICENSE file in the project root for license information.

"""Call the private FastAPI tunnel from Python with a raw rstream dial.

The dialed stream is a plain bidirectional byte stream, so a minimal HTTP/1.1
exchange is enough to consume the private API without any public endpoint.
"""

from __future__ import annotations

import argparse
import asyncio

import rstream


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--name", default="python-fastapi-demo", help="tunnel name")
    parser.add_argument("--path", default="/", help="request path")
    args = parser.parse_args()
    async with (
        rstream.Client.from_env() as client,
        await client.dial(args.name) as stream,
    ):
        request = (
            f"GET {args.path} HTTP/1.1\r\n"
            f"Host: {args.name}\r\n"
            "Connection: close\r\n"
            "\r\n"
        )
        stream.write(request.encode())
        await stream.drain()
        response = bytearray()
        while chunk := await stream.read(4096):
            response.extend(chunk)
        print(response.decode(errors="replace"))


if __name__ == "__main__":
    asyncio.run(main())
