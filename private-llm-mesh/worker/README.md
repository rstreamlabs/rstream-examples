# private-llm-mesh worker

A single Go binary that serves a local model to the mesh over rstream. It embeds
llama.cpp in-process, so the model runs inside this process with no separate
inference server to install or proxy, and it exposes an OpenAI-compatible API on
a published, token-gated rstream tunnel. Tool-calling is text-or-tool: the model
either answers in text or emits a structured tool call, decided and parsed by
llama.cpp's own `common_chat` layer, the same machinery `llama-server` uses,
rather than by any hand-rolled scraping.

## Quickstart

```bash
# Prerequisites: Go, CMake, a C/C++ compiler, and a selected rstream context
#   (rstream login && rstream project use <project-endpoint>).
make build                     # compiles llama.cpp (pinned) once, then the worker
./bin/worker --model qwen3:4b  # downloads the model on first run, then serves
```

The worker prints the public tunnel URL it is serving on. Point the app (or any
OpenAI client, through a scoped token) at that URL.

## Models

`--model` accepts a local GGUF path, a short alias, or a Hugging Face repository:

- a **local GGUF path** — `--model /models/Qwen_Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
- an **alias** — one of the entries in the table below
- a **Hugging Face repo** with optional quant — `bartowski/Qwen_Qwen3-4B-Instruct-2507-GGUF:Q4_K_M`

The aliases resolve to recognized, tool-capable instruct models, grouped by the
hardware they suit. Tool selection is what matters for this mesh, so the aliases
favour models that reliably emit a structured call rather than describing the
command in prose.

| Hardware | Model size | Aliases |
| --- | --- | --- |
| **Laptop** — Apple Silicon or a modern CPU, 16–32 GB | 4B–8B | `qwen3:4b`, `qwen3:8b`, `llama3.1` |
| **Homelab** — Mac mini or a small server, 32–64 GB | 14B–30B | `qwen3:14b`, `qwen3:30b`, `gpt-oss`, `mistral` |
| **GPU server** — 24 GB or more of VRAM | 32B and beyond | `qwen3:32b`, `gpt-oss:120b`, `llama3.3` |

Two properties are worth knowing when choosing. Qwen3 builds ending in `-2507`
(`qwen3:4b`, `qwen3:30b`) are dedicated instruct models that answer directly,
whereas the other Qwen3 sizes are hybrid reasoning models that emit a `<think>`
block first: the application renders it as reasoning, but it costs noticeable
latency on CPU. And `qwen3:30b` is a mixture-of-experts build — 30B of weights
with roughly 3B active per token, so it runs far faster than its size suggests.

The previous generation stays available for existing deployments: `qwen2.5`,
`qwen2.5:7b`, `qwen2.5:14b`, `qwen2.5:32b`, `mistral:7b`, `mistral-nemo`,
`mistral-small`, `llama3.2`.

Non-local references are downloaded from Hugging Face and cached under the user
cache directory, and subsequent runs reuse the cached file. With no `--model`,
the worker pulls `qwen3:4b`.

## Endpoints

Served on the tunnel, OpenAI-compatible:

- `POST /v1/chat/completions` — blocking or streaming (`"stream": true`); content
  tokens flush as they generate, tool calls arrive as a final structured delta.
- `GET /v1/models` — the model this worker serves.
- `GET /healthz` — liveness, model id, uptime, and load (`parallel`, `in_flight`,
  `waiting`) for the app's least-loaded routing.

Chat bodies are bounded to 4 MiB. Messages and tools must retain their OpenAI
array shape, `temperature` stays within 0–2, and one request may ask for at most
1,048,576 completion tokens; the model context and generation deadline remain
the practical lower limits. Malformed explicit environment values fail startup
instead of silently falling back to a different runtime configuration.

## Concurrency

A loaded model shares its weights across a pool of `--parallel` decoding contexts,
so that many requests run at once (each context has its own KV cache). At most
`--max-queue` additional requests wait for a context; excess work fails fast with
HTTP 429 so memory, sockets, and latency remain bounded. Each turn is stateless —
the context's KV cache is reset — and a request is aborted if the client
disconnects, the worker shuts down, or the `--max-gen-time` deadline passes.

## Flags

| flag | env | default | meaning |
| --- | --- | --- | --- |
| `--model` | `PLLM_MODEL` | `qwen3:4b` | path, alias, or HF repo `owner/name[:quant]` |
| `--model-id` | `PLLM_MODEL_ID` | derived | id advertised on `/v1/models` |
| `--ctx` | `PLLM_CTX` | `8192` | context window |
| `--max-tokens` | `PLLM_MAX_TOKENS` | `0` | default response cap; `0` = until EOS or the context limit (per-request overridable) |
| `--temp` | `PLLM_TEMP` | `0` | default temperature (0 = greedy) |
| `--parallel` | `PLLM_PARALLEL` | `1` | concurrent decoding contexts (each holds its own KV cache) |
| `--max-queue` | `PLLM_MAX_QUEUE` | `4` | maximum requests waiting for a decoding context before HTTP 429 |
| `--max-gen-time` | `PLLM_MAX_GEN_TIME` | `5m` | max wall-clock time per response |
| `--tunnel-name` | `PLLM_TUNNEL_NAME` | `private-llm-mesh` | published tunnel name |
| `--labels` | `PLLM_LABELS` | – | `key=value,…` tunnel labels for discovery |
| `--token-auth` | `PLLM_TOKEN_AUTH` | `true` | require a scoped token on the tunnel |
| `--rstream-engine`, `--rstream-token` | `PLLM_RSTREAM_*` | – | provisioned credentials; otherwise the local rstream config is used |

## Acceleration

On Apple Silicon the build already uses Metal, with the shaders embedded so the
binary stays self-contained. Elsewhere the default is a portable CPU build; point
it at a GPU backend on the host:

```bash
make deps LLAMA_CMAKE_FLAGS=-DGGML_CUDA=ON    # NVIDIA
make build
```

Changing `LLAMA_CMAKE_FLAGS` (or `LLAMA_CPP_REF`) re-runs the llama.cpp build on
its own, so there is no need to `make distclean` first.

## How it works

```
model (GGUF) ─► llama.cpp (pinned, static)          rstream tunnel
                 common_chat: templates and tools   (BytestreamTunnel
                 → grammar; PEG parse → tool_calls     == net.Listener)
                        ▲  cgo shim (extern "C")              ▲
                  internal/engine ─► internal/openai ─► http.Serve(tunnel, handler)
```

llama.cpp is fetched into `third_party/` at a pinned revision and built as static
libraries by `make deps` (it is not part of this repository), then linked through
a small cgo shim. The published tunnel is a `net.Listener`, so serving is plain
`http.Serve`. A supervisor reconnects with backoff if the tunnel drops.

## Layout

```
cmd/worker/        entry point (flags → app.Run, signals)
internal/engine/   cgo shim around llama.cpp common_chat (shim.cpp/.h, trampoline.c)
internal/model/    model reference resolution + Hugging Face download/cache
internal/openai/   OpenAI HTTP surface (pure Go, no cgo)
internal/tunnel/   publishes the rstream tunnel as a net.Listener
internal/app/      orchestration + reconnect supervisor
internal/config/   flags and environment
internal/llm/      engine-agnostic chat types shared by engine and openai
```

## Make targets

`make build` · `make run ARGS="--model …"` · `make test` (set `MODEL=/path/gguf`
to include the engine tests) · `make vet` · `make check-format` · `make check`
/ `make verify` (all gates) · `make clean` · `make distclean`.
