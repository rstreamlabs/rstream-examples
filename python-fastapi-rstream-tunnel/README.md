# Serve FastAPI through an rstream tunnel

FastAPI owns the routes. The Python SDK accepts HTTP streams from rstream
and passes them to the application. Run the same app as published HTTPS,
behind edge token authentication, or as a private service.

## Install and run

Use Python 3.10 or newer. Start at the root of the [examples checkout](../README.md) and enter the FastAPI sample.

```sh
cd python-fastapi-rstream-tunnel
```

Create the virtual environment and install the sample dependencies.

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
```

In PowerShell, use `py -m venv .venv` and
`.\.venv\Scripts\Activate.ps1`. Run the same pip command there.

For a hosted project, install the [rstream CLI](https://rstream.io/docs/getting-started/installation)
and sign in if needed. Create a project in the Dashboard if you need one. For the first unauthenticated request, use a project with published access set to `allowed`. If the project requires caller authentication, follow the token path below.

```sh
rstream login
```

List your projects to find the endpoint and project ID for this sample.

```sh
rstream project list
```

Replace `my-project-endpoint` with the endpoint for your project.

```sh
rstream project use my-project-endpoint
```

For a self-hosted Engine, configure a [direct context](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context)
that permits the operation. Set `SSL_CERT_FILE` to the private CA bundle
when needed. Configure trust for your HTTPS client as well.

Once your hosted or self-hosted context is ready, start the application.

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

Leave the server running. In a second terminal, set `TUNNEL_URL` to the new `Forwarding address`. In a POSIX shell, replace the example origin below. Check the route without a token and expect HTTP 401.

```sh
TUNNEL_URL='https://your-forwarding-host'
curl -i "${TUNNEL_URL%/}/items/42"
```

To issue a caller token on hosted rstream, your signed-in CLI credential needs `account.tokens.create` and permission to delegate `tunnels.streams.create-delete` for this project and route. See [issuer credentials](https://rstream.io/docs/security/tokens#create-and-manage-tokens). Save the following resource policy as `fastapi-caller.json` in the caller’s working directory. Replace `PROJECT_ID` with the **ID** from `rstream project list` for the project you selected earlier. The sample's default tunnel name is `python-fastapi-demo`.

```json
{
  "tunnels": {
    "projects": ["PROJECT_ID"],
    "scopes": {
      "tunnels": {
        "connect": {
          "filters": { "name": { "exact": "python-fastapi-demo" } },
          "params": { "path": { "regex": "^/items/42$" } }
        }
      }
    }
  }
}
```

Mint the token from the directory containing that file. It is valid for 60 seconds.

```sh
CALLER_TOKEN=$(rstream token create --expires-in 60 --permission tunnels.streams.create-delete --resources-file fastapi-caller.json)
```

Send it with the request. Expect HTTP 200 and `{"item_id":42,"source":"rstream tunnel"}`.

```sh
curl -i -H "Authorization: Bearer $CALLER_TOKEN" "${TUNNEL_URL%/}/items/42"
```

In PowerShell, set `$TUNNEL_URL` to the printed address without a trailing slash. Use `curl.exe -i "$TUNNEL_URL/items/42"` for the first check, assign the same `rstream token create` command to `$CALLER_TOKEN`, then add `-H "Authorization: Bearer $CALLER_TOKEN"` to the request. Stop the server with Ctrl+C when finished.

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
