# Serve FastAPI through an rstream tunnel

FastAPI owns the routes. The Python SDK accepts HTTP streams from rstream
and passes them to the application. Run the same app as published HTTPS,
behind edge token authentication, or as a private service.

## Install and run

Use Python 3.10 or newer. From this directory, create an environment.

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
```

In PowerShell, use `py -m venv .venv` and
`.\.venv\Scripts\Activate.ps1`. Run the same pip command there.

For a hosted project, install the [rstream CLI](https://rstream.io/docs/getting-started/installation)
and select the endpoint listed for the project. Create a project in the Dashboard if you need one. For the first unauthenticated request, use a project with published access set to `allowed`. If the project requires caller authentication, follow the token path below.

```sh
rstream login
```

List the projects available to your account.

```sh
rstream project list
```

Select the endpoint from the list.

```sh
rstream project use my-project-endpoint
```

For a self-hosted Engine, configure a [direct context](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context)
that permits the operation. Set `SSL_CERT_FILE` to the private CA bundle
when needed. Configure trust for your HTTPS client as well.

Start the application from the activated virtual environment.

```sh
python main.py
```

Open the printed `Forwarding address` and try `/items/42`. A successful
request returns HTTP 200 and `{"item_id":42,"source":"rstream tunnel"}`.
Keep the printed hostname for TLS verification. Stop the program with
Ctrl+C before switching modes.

## Require caller authentication

On a project that supports
[HTTP token authentication](https://rstream.io/docs/tunnels/http-tunnels/authentication),
start the sample with:

```sh
python main.py --token-auth
```

Use the new forwarding address. Expect 401 without a credential. Send a
Bearer token allowed to reach the tunnel and `/items/42` to get the same
JSON response. The credential that started the tunnel and the credential
used by this HTTP caller serve different purposes.

## Dial the private service

Use a hosted project with private tunnels (Pro or Enterprise), or a direct Engine that supports private bytestreams. The owner creates a private tunnel named `python-fastapi-demo`. Start it
in one terminal with a context allowed to create private tunnels:

```sh
python main.py --private
```

In a second terminal, activate the same Python environment and use a
context allowed to dial that tunnel. The client sends an HTTP/1.1 request
through the private stream and prints its response.

```sh
python dial_client.py --path /items/42
```

The response starts with HTTP 200 and contains the item JSON. Stop the
owner when finished. If you change `--name`, use the same value in both
commands.

## Integrate your application

The runner enters the FastAPI lifespan before opening the tunnel and closes
the SDK resources before leaving the lifespan. Put shared resources on
`app.state`. The direct adapter handles HTTP/1.1; run a regular ASGI server
with [local forwarding](https://rstream.io/docs/integrations/python-sdk#managed-local-forwarding)
for WebSockets or server-managed lifespan state.

Use `python -m unittest -v test_main` to check startup and cleanup locally.
On a machine with Make, `make verify` also prepares the environment and
checks both commands. Follow the [product tutorial](https://rstream.io/guides/serve-a-fastapi-app-through-rstream-tunnels)
for the three access paths and caller credentials.
