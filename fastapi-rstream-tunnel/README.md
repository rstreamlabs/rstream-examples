# FastAPI rstream tunnel

This sample serves a FastAPI application through a published rstream HTTP
tunnel using the Python SDK.

It demonstrates the Python-native path where the application keeps its normal
ASGI shape and the SDK serves accepted rstream streams in-process. There is no
Uvicorn behind a reverse proxy, no loopback port, and no public listener owned
by the process.

## Install

The sample needs Python 3.10+ and two packages, ideally in a virtual
environment.

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install "rstreamlabs-rstream[asgi,api]" fastapi
```

## Run

Select a project with the rstream CLI, then run the server.

```bash
rstream login
rstream project use <project-endpoint> --default
python main.py
```

The process prints the forwarding address once the tunnel is created. Add
`--token-auth` to require an rstream token at the edge of the published
endpoint, or `--private` to keep the tunnel unpublished and reachable only
through an rstream dial.

## Dial the private tunnel

With the server running in `--private` mode, the second script consumes the
API over a raw rstream dial.

```bash
python dial_client.py --path /items/42
```
