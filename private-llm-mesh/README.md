# private-llm-mesh

private-llm-mesh is a chat application whose inference stays on machines you control. Open models run on your own hardware, whether a workstation, a laptop, or a GPU server, and a Next.js application reaches them over rstream. No machine needs a public address or an inbound port, and the weights never leave your infrastructure while the front end deploys to Vercel.

Each worker is a single program you run on a machine that holds a model. It loads the model in-process, serves an OpenAI-compatible API, and registers a private tunnel that serves as its entry in the mesh. The application discovers the pool from the tunnel registry, routes each message to the nearest worker with the lightest load, runs the agent turn against it over rstream, and streams the response back to the browser. Adding capacity is a matter of starting another worker, which joins the pool as soon as it connects.

A companion guide walks through the architecture and the rstream integration in detail: [Build a Private LLM Mesh with rstream](https://rstream.io/guides/build-a-private-llm-mesh-with-rstream).

## Architecture

The application hosts the chat interface and the agent runtime, and rstream provides the private connectivity to the workers.

A worker publishes an HTTP tunnel with token authentication. The rstream edge gives that tunnel a public HTTPS host, but every request must carry a token scoped to it — there is no inbound port on the machine, only the worker's outbound tunnel.

When a message arrives (`POST /api/chat`), the application authenticates the request, selects a worker that serves the chosen model, preferring the lightest load with round-trip time as a tiebreak, and mints a short-lived token (300 seconds by default) scoped to that one tunnel and the single path `/v1/chat/completions`. The lifetime covers the whole turn, since an agent turn makes several calls to the worker. It then runs the turn on the server. The model, reached over rstream with that token, either answers in text or calls a tool, and the application streams the result back to the browser. The agent loop runs on the server because its tools do: web search and the rstream MCP hold server-only credentials, so the worker token is minted and used on the server and never reaches the browser.

Discovery and presence come from the tunnel registry. The pool panel uses `@rstreamlabs/react` to watch tunnel state from the browser in real time, so a worker that joins or leaves appears at once without polling. Each worker is then probed on its OpenAI `/v1/models` endpoint for liveness, its current model list, and round-trip time, with in-flight load read from an optional `/healthz`; every answer is tagged with the machine that produced it.

Reaching a worker is a server-to-worker call over a short-lived, narrowly scoped tunnel token. The same primitive lets the agent open a shell on one of the user's machines through the rstream MCP, with no change to the connectivity or authorization model.

## Stack

- Next.js App Router, Vercel AI SDK, and AI Elements for the chat UI
- `@rstreamlabs/tunnels` — application client credentials, tunnel discovery, and fine-grained, capability-scoped auth tokens
- `@rstreamlabs/react` — live tunnel-state watch in the browser
- NextAuth (GitHub) with an environment allowlist — JWT sessions, no database
- A single Go binary worker that embeds llama.cpp in-process — it runs the model itself and serves an OpenAI-compatible API with native, grammar-driven tool-calling

## Setup

### The app

The application authenticates to rstream with application client credentials and mints its own short-lived, scoped tokens. This is the production model, and it requires no `rstream login` on the server. Create a project and an application client in the rstream dashboard, then set `web/.env.local`:

```bash
RSTREAM_CLIENT_ID=...
RSTREAM_CLIENT_SECRET=...
RSTREAM_PROJECT_ENDPOINT=...           # the project's 8-hex endpoint id

# Auth: GitHub SSO restricted by an allowlist, or disabled for a local quickstart.
NEXTAUTH_SECRET=...                    # openssl rand -hex 32
NEXTAUTH_URL=http://localhost:3000
GITHUB_CLIENT_ID=...
GITHUB_CLIENT_SECRET=...
# ALLOWED_EMAIL_DOMAINS=your-org.com   # and/or ALLOWED_EMAILS, ALLOWED_GITHUB_LOGINS
# AUTH_DISABLED=true                   # quickstart: no sign-in at all
```

```bash
cd web
npm install
npm run dev
```

### A worker

A worker is a single Go binary that embeds llama.cpp and runs the model in-process, so there is no separate inference server to install or proxy. It authenticates with the local rstream configuration on the machine, which lets each machine join the organization's pool directly, and no application secret travels to the worker.

```bash
# 1. The rstream CLI (from rstream.io), pointed at your project.
rstream login
rstream project use <project-endpoint>

# 2. Build the worker. `make deps` clones and builds llama.cpp (pinned) on the
#    first run; you need Go, CMake, and a C/C++ compiler.
git clone https://github.com/rstreamlabs/rstream-examples.git
cd rstream-examples/private-llm-mesh/worker
make build

# 3. Run it. The model downloads on first run — pass an alias, a HF repo, or a
#    local path (any tool-capable model: Mistral, Llama 3.1, Qwen2.5, …).
make run ARGS="--model qwen2.5:7b"
```

Set `--ctx` to the model's context window and `--model-id` to the name the application shows. On Apple Silicon or NVIDIA hardware, rebuild llama.cpp with acceleration using `make distclean && make deps LLAMA_CMAKE_FLAGS=-DGGML_METAL=ON` (or `-DGGML_CUDA=ON`). A single machine can run several workers for different models.

The worker-pool panel includes an Add worker dialog that carries these exact steps, pre-filled with the project endpoint.

### Worker variants

The standalone binary is the primary way to run a worker, and the one the Add worker dialog installs. It is one of two interchangeable, fully sovereign variants that publish the same discovery labels and the same OpenAI API, so they share one pool and can be mixed machine by machine.

- **[`worker/`](worker)** — the standalone Go binary here. It embeds llama.cpp and runs the model in-process, so the weights stay on your hardware. Maximum control over the build and native GPU acceleration; one model per worker.
- **[`worker-compose/`](worker-compose)** — Ollama and the rstream reconciler, composed from stock parts with no custom code. Turnkey and multi-model, and a natural fit on a Linux host with a GPU.

Both keep inference on your own hardware. Because the pool is defined only by labels and the OpenAI contract, any other OpenAI-compatible server — including a bridge to a hosted model — could join the same way later.

## The engine

The worker embeds llama.cpp through a small cgo shim and serves the OpenAI API from llama.cpp's own `common_chat` layer, the same machinery `llama-server` uses. Tool-calling is grammar-driven and parsed in-process: the model either answers in text or emits a structured tool call, with no client-side `<tool_call>` scraping. The model runs in a single process, and nothing needs to bind a port for it.

- **Models.** Any GGUF file works; point `--model` at it. The worker ships aliases for recognized, tool-capable models grouped by hardware tier (laptop, homelab, and GPU server), listed in the [worker README](worker/README.md#models).
- **Acceleration.** CPU by default for portability; rebuild with `-DGGML_METAL=ON` (Apple Silicon) or `-DGGML_CUDA=ON` (NVIDIA) for GPU offload.
- **Context.** `--ctx` sets the window, and `--max-tokens` and `--temp` set generation defaults that a request can override.

## Security postures

Authorization is least-privilege by construction. The token that reaches a worker is scoped to a single path on a single worker and is short-lived, and the edge rejects anything else. It is minted and used on the server, so the browser never holds it. The browser's only rstream credential is a separate short-lived, read-only token for watching tunnel presence, and the broad client credentials never leave the server.

Workers are reached from the server over a token-gated edge host, so no machine exposes an inbound port and only the outbound tunnel remains. If a policy forbids any public ingress to compute, the same SDK primitives keep workers fully private, dialed over rstream without a public edge host. Both postures keep authorization on the application and differ only in the worker's ingress shape; neither is inherently more secure than the other.

Access to the application itself is gated separately by NextAuth, through GitHub single sign-on restricted by an organization, domain, or login allowlist, or disabled entirely for a local quickstart. The worker pool is global to the application, so it is the front door that this gates.

## Self-hosting (Community Edition)

Nothing here depends on the managed rstream cloud. Point the application and the workers at a self-hosted engine by setting `RSTREAM_API_URL` for the application (and `RSTREAM_ENGINE` if you address the engine directly) and selecting the project with the CLI for the workers. The same control-plane and data-plane split then runs end to end on your own infrastructure.

## Deploy

The application is stateless. Client credentials and the authentication configuration are the only server state, so it deploys to Vercel as it stands, and chat history lives in the browser session with no database to provision. Set the same environment variables in the Vercel project, point the GitHub OAuth callback at the deployed URL, and the workers connect as soon as they start.
