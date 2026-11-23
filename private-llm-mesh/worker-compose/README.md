# Ollama worker for the private LLM mesh

Ollama serves models through its OpenAI-compatible API. The rstream Docker reconciler publishes that API through a token-gated HTTPS tunnel and registers it in the same pool as the [standalone llama.cpp worker](../worker/README.md). One Ollama worker can advertise several models and load them on demand.

## Start the worker

Use a Linux host with Docker Engine and Docker Compose, enough memory for your models, and an engine address and [project-scoped agent token](https://rstream.io/docs/security/tokens) for the same hosted project as the web app. The reconciler needs permission to create and delete the worker tunnel. It mounts the Docker socket to inspect containers and watch events, which grants privileged access to the host.

From `private-llm-mesh/worker-compose`, create your local `.env` from the template if you do not already have one.

```bash
cp -n .env.example .env
```

Set `RSTREAM_ENGINE` and `RSTREAM_AUTHENTICATION_TOKEN` in `.env`. Then start
Ollama and pull the first model.

```bash
docker compose config --quiet
docker compose up -d
docker compose exec ollama ollama pull qwen2.5:7b
```

The first pull downloads the weights into the Compose volume. Check that the worker appears in the app's pool and that `qwen2.5:7b` is available in the model picker. Send a prompt to check the streamed reply and worker attribution. Pull more models with `docker compose exec ollama ollama pull MODEL_NAME`. The app reads the catalog from `/v1/models` on its next refresh.

Run `docker compose down` when finished. Compose retains the model volume for the next run.

## Configuration

| Variable                       | Default                  | Purpose                                                                                      |
| ------------------------------ | ------------------------ | -------------------------------------------------------------------------------------------- |
| `RSTREAM_ENGINE`               | Required                 | Project engine address, such as `engine.example.com:443`.                                    |
| `RSTREAM_AUTHENTICATION_TOKEN` | Required                 | Agent token for the reconciler.                                                              |
| `PLLM_CTX`                     | `8192`                   | Sets Ollama's context window and the matching label the app uses when budgeting tool output. |
| `PLLM_ACCEL`                   | `cpu`                    | Accelerator label displayed in the app.                                                      |
| `PLLM_HOST`                    | `ollama`                 | Machine label displayed in the app.                                                          |
| `OLLAMA_IMAGE`                 | `ollama/ollama:latest`   | Ollama image override.                                                                       |
| `RSTREAM_IMAGE`                | `rstream/rstream:latest` | Reconciler image override.                                                                   |

To use an existing CLI context, mount an engine configuration that is readable and whose credential storage works inside the container. See [Docker installation](https://rstream.io/docs/getting-started/installation/docker).

For an NVIDIA GPU, install the NVIDIA Container Toolkit and uncomment the GPU reservation in `compose.yaml`. Set `PLLM_ACCEL=gpu` so the label reflects the build. For native Apple Silicon acceleration, use the [standalone worker](../worker/README.md#acceleration).

## Discovery and access

The worker tunnel carries `role=llm` and `app=private-llm-mesh` for discovery, plus labels describing the runtime, context, machine, and accelerator. Ollama stays on the Docker network without a published host port. Its edge endpoint accepts the same scoped tokens as the standalone worker. Ollama supplies the model list and liveness probe at `/v1/models`. The app uses neutral load when `/healthz` is unavailable. See the [web guide](../web/README.md#worker-discovery-and-routing).
