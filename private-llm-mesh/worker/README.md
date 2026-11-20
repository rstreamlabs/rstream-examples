# Standalone worker for the private LLM mesh

A single Go binary serves one local model over a published, token-gated
rstream tunnel. It embeds llama.cpp and uses the same `common_chat` layer as
`llama-server` for model templates, grammar-driven generation, and structured
tool calls.

## Build and run

Install Git, Go 1.27.4, Make, CMake, a C/C++ toolchain, and the
[rstream CLI](https://rstream.io/download). From `private-llm-mesh/worker`,
select the same hosted project as the web app and build the worker.

```bash
rstream login
rstream project use YOUR_PROJECT_ENDPOINT
make build
./bin/worker --model qwen3:4b --tunnel-name laptop-qwen
```

The first run downloads the model. Wait for `worker online` with the
published tunnel URL, then check the [chat app](../README.md#3-verify-a-turn).
The app discovers the worker by label. A direct OpenAI client needs a token
scoped to the tunnel and request path. Stop the worker with Ctrl+C.

## rstream credentials

| Source                        | Setup                                                                                                                                                                                                                |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Local CLI context             | Select the project's context as above. Use `RSTREAM_CONTEXT` to select a named context.                                                                                                                              |
| Provisioned agent credentials | Set both `PLLM_RSTREAM_ENGINE` and `PLLM_RSTREAM_TOKEN`, or provide the matching flags below. Use the engine address and a project-scoped agent token that can create and delete the worker's published HTTP tunnel. |

The application client ID and secret belong to the web app. Worker
credentials authorize tunnel registration. The app mints separate credentials
for inference and discovery. See [rstream tokens](https://rstream.io/docs/security/tokens).

## Models

`--model` accepts a local GGUF path, a short alias, or a Hugging Face repository:

- a **local GGUF path** — `--model /models/Qwen_Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
- an **alias** — one of the entries in the table below
- a **Hugging Face repo** with optional quant — `bartowski/Qwen_Qwen3-4B-Instruct-2507-GGUF:Q4_K_M`

The aliases resolve to instruct models with tool support. To choose a model,
budget for the quantized weights plus the context and KV cache, and check
structured tool calls with the model and template you plan to use.

| Hardware                                             | Model size     | Aliases                                        |
| ---------------------------------------------------- | -------------- | ---------------------------------------------- |
| **Laptop** — Apple Silicon or a modern CPU, 16–32 GB | 4B–8B          | `qwen3:4b`, `qwen3:8b`, `llama3.1`             |
| **Homelab** — Mac mini or a small server, 32–64 GB   | 14B–30B        | `qwen3:14b`, `qwen3:30b`, `gpt-oss`, `mistral` |
| **GPU server** with memory sized for the model       | 32B and beyond | `qwen3:32b`, `gpt-oss:120b`, `llama3.3`        |

`qwen3:4b` and `qwen3:30b` use the dedicated `-2507` instruct models. The
other Qwen3 sizes listed use hybrid reasoning models, which can emit a
`<think>` block before the answer. `qwen3:30b` has roughly 3B active parameters
per token and still needs room for the full quantized weights.

The previous generation stays available for existing deployments: `qwen2.5`,
`qwen2.5:7b`, `qwen2.5:14b`, `qwen2.5:32b`, `mistral:7b`, `mistral-nemo`,
`mistral-small`, `llama3.2`.

Aliases and repository references resolve through Hugging Face and cache the
weights under the user cache directory. For an offline model load, supply
the local GGUF path. With no `--model`, the worker uses `qwen3:4b`.

## Endpoints

Served on the tunnel, OpenAI-compatible:

- `POST /v1/chat/completions` — blocking or streaming (`"stream": true`); content
  tokens flush as they generate, tool calls arrive as a final structured delta.
- `GET /v1/models` — the model this worker serves.
- `GET /healthz` — liveness, model id, uptime, and load (`parallel`, `in_flight`,
  `waiting`) for the app's least-loaded routing.

Chat bodies are bounded to 4 MiB. Messages and tools must retain their OpenAI
array shape, and `temperature` stays within 0–2. A request can ask for up to
1,048,576 completion tokens, subject to the model context and generation
deadline. Malformed explicit environment values fail startup.

## Concurrency

A loaded model shares its weights across `--parallel` decoding contexts. Each
context has its own KV cache, which resets between requests. Up to
`--max-queue` additional requests wait for a context. The worker returns HTTP
429 for excess work and aborts a request if the client disconnects, the
worker shuts down, or the `--max-gen-time` deadline passes.

## Flags

| flag               | env                   | default            | meaning                                                                                                     |
| ------------------ | --------------------- | ------------------ | ----------------------------------------------------------------------------------------------------------- |
| `--model`          | `PLLM_MODEL`          | `qwen3:4b`         | path, alias, or HF repo `owner/name[:quant]`                                                                |
| `--model-id`       | `PLLM_MODEL_ID`       | derived            | id advertised on `/v1/models`                                                                               |
| `--ctx`            | `PLLM_CTX`            | `8192`             | context window                                                                                              |
| `--max-tokens`     | `PLLM_MAX_TOKENS`     | `0`                | Response cap. `0` generates until the end-of-sequence token or the context limit. Requests can override it. |
| `--temp`           | `PLLM_TEMP`           | `0`                | default temperature (0 = greedy)                                                                            |
| `--parallel`       | `PLLM_PARALLEL`       | `1`                | concurrent decoding contexts (each holds its own KV cache)                                                  |
| `--max-queue`      | `PLLM_MAX_QUEUE`      | `4`                | maximum requests waiting for a decoding context before HTTP 429                                             |
| `--max-gen-time`   | `PLLM_MAX_GEN_TIME`   | `5m`               | max wall-clock time per response                                                                            |
| `--tunnel-name`    | `PLLM_TUNNEL_NAME`    | `private-llm-mesh` | published tunnel name                                                                                       |
| `--labels`         | `PLLM_LABELS`         | –                  | `key=value,…` tunnel labels for discovery                                                                   |
| `--token-auth`     | `PLLM_TOKEN_AUTH`     | `true`             | require a scoped token on the tunnel                                                                        |
| `--rstream-engine` | `PLLM_RSTREAM_ENGINE` | CLI context        | Engine address for provisioned credentials. Set with the token.                                             |
| `--rstream-token`  | `PLLM_RSTREAM_TOKEN`  | CLI context        | Agent token for the specified engine.                                                                       |

## Acceleration

On Apple Silicon the build uses Metal and embeds the shaders. Elsewhere it
defaults to a portable CPU build. To enable CUDA on a supported host:

```bash
make build LLAMA_CMAKE_FLAGS=-DGGML_CUDA=ON
```

The CUDA build requires a supported NVIDIA toolchain. Start the resulting
binary directly, or pass the same `LLAMA_CMAKE_FLAGS` to any later Make
target that builds or tests the worker. Changing the flags rebuilds
llama.cpp. Set `--labels accelerator=metal` or `--labels accelerator=cuda` when
appropriate so the app's pool badge reflects the actual build. The label
defaults to `cpu`.

## How it works

The build fetches the pinned llama.cpp revision into `third_party/` and
links its static libraries through a cgo shim. The worker serves the
OpenAI-compatible API with `http.Serve` on the Go SDK's tunnel listener. The
worker keeps its model loaded while it reconnects with backoff after a
tunnel drop.

## Layout

```
cmd/worker/        entry point for flags and signals
internal/engine/   cgo shim around llama.cpp common_chat (shim.cpp/.h, trampoline.c)
internal/model/    model reference resolution + Hugging Face download/cache
internal/openai/   OpenAI HTTP surface (pure Go, no cgo)
internal/tunnel/   publishes the rstream tunnel as a net.Listener
internal/app/      orchestration + reconnect supervisor
internal/config/   flags and environment
internal/llm/      engine-agnostic chat types shared by engine and openai
```

## Verification

```bash
make verify
MODEL=/absolute/path/to/model.gguf make test
```

`make verify` runs formatting, vet, tests, race tests, a build, static
analysis, and the vulnerability scan. Set `MODEL` to include the real-model
engine tests. See the sample's [qualification tools](../qualification/README.md)
for repeatable model and routing checks.
