# private-llm-mesh worker — composed variant

This joins the mesh from stock parts instead of the single Go binary in
[`../worker`](../worker). Ollama serves an OpenAI-compatible API, and the rstream
Docker reconciler publishes it as a private, token-gated tunnel derived from
container labels. There is no custom code here: the labels carry the same
discovery contract the app filters and routes on, so a composed worker appears in
the same pool as a standalone one and is reached in exactly the same way.

The standalone binary is the primary path — it embeds the engine and downloads
its own weights. This variant shows that the mesh contract is standard: anything
that speaks the OpenAI API behind a labelled rstream tunnel is a first-class
worker. Ollama loads several models on demand, so one machine offers a catalog
rather than a single model.

## Run

The reconciler authenticates to rstream with an agent token for your project.

```bash
export RSTREAM_ENGINE="<engine-host>:443"
export RSTREAM_AUTHENTICATION_TOKEN="<agent-token>"   # or copy .env.example to .env

docker compose up -d
docker compose exec ollama ollama pull qwen2.5:7b   # then any other models you want
```

Ollama's port is never published to the host — the reconciler reaches it over the
Docker network, so the machine exposes no inbound port, only the outbound tunnel.

If the host already has a working rstream CLI context, mount it read-only instead
of putting a token in the environment:

```yaml
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - $HOME/.rstream:/home/rstream/.rstream:ro
```

## Models

Ollama keeps a pool of models and loads them on demand, so one worker serves
many. The app reads a worker's catalog from its own `/v1/models`, so whatever you
pull appears in the picker on the next refresh — there is no label to keep in sync:

```bash
docker compose up -d
docker compose exec ollama ollama pull qwen2.5:7b
docker compose exec ollama ollama pull llama3.1     # shows up automatically
```

The model ids match the aliases the standalone worker uses, so the same name in
the app's picker can be served by either variant — that is the interchangeability
the mesh is built on. Use tool-capable models; Qwen2.5 and Mistral-Nemo select
tools reliably.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PLLM_CTX` | `8192` | context window advertised for the tool budget |
| `PLLM_ACCEL` | `cpu` | accelerator label shown in the pool (`cpu`, `gpu`) |
| `PLLM_HOST` | `ollama` | machine name shown in the pool |
| `RSTREAM_ENGINE`, `RSTREAM_AUTHENTICATION_TOKEN` | – | reconciler credentials |

## How it joins the mesh

The tunnel labels are the whole integration. `role=llm` and `app=private-llm-mesh`
put it in the pool the app watches; `ctx`, `engine`, `accelerator`, and `host`
describe it, while its model list comes from its own `/v1/models`;
`http.auth.token` makes the edge require the short-lived,
tunnel-scoped token the app mints per turn. Ollama does not serve the mesh's
`/healthz`, so the app falls back to the standard `/v1/models` endpoint for
liveness and treats load as neutral — the worker still routes, ordered by
round-trip time. Load is an enrichment, never a gate.

## GPU

Uncomment the `deploy.resources` block in `compose.yaml` to offload to an NVIDIA
GPU (requires the NVIDIA Container Toolkit). On Apple Silicon, run the standalone
worker instead — Docker cannot pass the Metal GPU through to a Linux container.

## Security

The reconciler mounts `/var/run/docker.sock` to inspect containers and receive
Docker events. Treat that as privileged access to the Docker host; on hardened
hosts, place a restricted Docker socket proxy in front of the daemon or run the
reconciler under the host's normal Docker access controls.
