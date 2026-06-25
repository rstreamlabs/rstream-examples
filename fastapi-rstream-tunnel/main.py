# See LICENSE file in the project root for license information.

"""Serve a FastAPI application through a published rstream HTTP tunnel.

The application keeps its normal FastAPI shape. The rstream SDK provides the
network surface: a published tunnel for standard HTTP clients, or a private
tunnel reachable only through an rstream dial. No local HTTP server, reverse
proxy, or open port is involved.
"""

from __future__ import annotations

import argparse
import asyncio
from contextlib import suppress

from fastapi import FastAPI

import rstream

app = FastAPI(title="fastapi-rstream-tunnel")


@app.get("/")
async def root() -> dict[str, str]:
    return {"service": "fastapi-rstream-tunnel", "status": "ok"}


@app.get("/items/{item_id}")
async def read_item(item_id: int) -> dict[str, int | str]:
    return {"item_id": item_id, "source": "rstream tunnel"}


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "healthy"}


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--name", default="fastapi-demo", help="tunnel name")
    parser.add_argument(
        "--private",
        action="store_true",
        help="create an unpublished tunnel reachable only through an rstream dial",
    )
    parser.add_argument(
        "--token-auth",
        action="store_true",
        help="require an rstream token at the edge of the published endpoint",
    )
    args = parser.parse_args()
    if args.private and args.token_auth:
        parser.error("--token-auth applies to published endpoints only")
    async with (
        rstream.Client.from_env() as client,
        await client.connect() as control,
    ):
        if args.private:
            # Private tunnels reject public exposure options such as the
            # protocol and HTTP version: the engine never terminates them.
            tunnel = await control.create_tunnel(
                name=args.name,
                publish=False,
                labels={"app": "fastapi-demo"},
            )
            print(f"Private tunnel ready: rstrm://{args.name}", flush=True)
        else:
            tunnel = await control.create_tunnel(
                name=args.name,
                protocol="http",
                http_version="http/1.1",
                publish=True,
                auth=rstream.TunnelAuth(token=True) if args.token_auth else None,
                labels={"app": "fastapi-demo"},
            )
            print("Forwarding address:", tunnel.forwarding_address, flush=True)
        await rstream.asgi.serve(app, tunnel)


if __name__ == "__main__":
    with suppress(KeyboardInterrupt):
        asyncio.run(main())
