# Python FastAPI rstream tunnel

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
pip install -r requirements.txt
```

Or use the Makefile:

```bash
make verify
```

## Run

Select a project with the rstream CLI, then run the server.

```bash
rstream login
rstream project use <project-endpoint>
make run
```

The process prints the forwarding address once the tunnel is created. Add
`--token-auth` to require an rstream token at the edge of the published
endpoint, or `--private` to keep the tunnel unpublished and reachable only
through an rstream dial.

## Dial the private tunnel

With the server running in `--private` mode, the second script consumes the
API over a raw rstream dial. Run the server in one terminal:

```bash
make run-private
```

Then dial it from another:

```bash
make dial ARGS="--path /items/42"
```

## Recognize success and own the lifecycle

Open the printed published address to receive
`{"service":"python-fastapi-rstream-tunnel","status":"ok"}`. The private dial
example prints the HTTP response for `/items/42`. Stop the process with an
interrupt; its nested contexts close the tunnel/client before leaving FastAPI's
`app.router.lifespan_context(app)`. Startup therefore runs before connection setup,
and shutdown still runs after setup failure or cancellation. Put process resources
on `app.state`.

The direct adapter dispatches HTTP/1.1 only. It does not itself serve ASGI lifespan
or WebSocket scopes and does not inject yielded lifespan state into request
`scope["state"]`. For a full ASGI server's features, keep that server on loopback
and use local forwarding as described in the [Python SDK](https://github.com/rstreamlabs/rstream-python/blob/main/docs/002-tunnels.md).
Reconnection is also application policy: this small service exits when its serving
operation ends. A long-lived product should supervise that lifecycle with bounded
backoff and explicit shutdown, rather than recreate a tunnel per request.

`make verify` installs the application requirements and runs its lifecycle tests
without a rstream account. Those tests prove startup/shutdown on success,
connection failure, serving failure and cancellation with synthetic controls.
