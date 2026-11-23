# Private LLM mesh

Run a Next.js chat app on your laptop and stream a response from a model running on the same laptop or another machine you control. Workers join a pool as they start. The app discovers them through rstream, routes chat turns, and shows which worker answered.

The browser talks to Next.js. The model runs on the worker, which opens an outbound connection to rstream and serves an OpenAI-compatible API through a token-protected HTTPS endpoint at the edge. The worker machine needs no inbound port. Model weights stay on the worker. Prompts travel through the app and rstream to the selected worker.

## Before you start

| Where   | What you need                                                                                                                                         |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| Web app | Git, Node.js 24, npm 11, and OpenSSL. The lockfile uses npm 11.19.1.                                                                                  |
| Worker  | Git, Go 1.27.4, Make, CMake, and a C/C++ toolchain. Apple Silicon uses Metal by default.                                                              |
| rstream | A hosted project and its endpoint, application client credentials for the web app, and the [rstream CLI](https://rstream.io/download) for the worker. |

The same laptop can run both roles. The default worker model, `qwen3:4b`, downloads roughly 2.5 GB of weights on first run. Allow room for the download, the native build, and the model in memory. See the [worker model and hardware guide](worker/README.md#models).

## 1. Start the chat app

In the rstream Dashboard, open the workspace's Tokens page and create an Application key (APP) for the selected project. Grant `account.projects.read-only` for project resolution, `tunnels.resources.read-only` for inventory and watches, and `tunnels.streams.create-delete` for worker requests. The Next.js server keeps the client ID and secret and signs the scoped tokens it needs. See [Tokens](https://rstream.io/docs/security/tokens).

```bash
git clone https://github.com/rstreamlabs/rstream-examples.git
cd rstream-examples/private-llm-mesh/web
cp .env.example .env.local
openssl rand -hex 32
```

Set these values in `.env.local` using the generated secret.

| Variable                                     | Value for the first run                                                   |
| -------------------------------------------- | ------------------------------------------------------------------------- |
| `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET` | The application credential from the Dashboard.                            |
| `RSTREAM_PROJECT_ENDPOINT`                   | The same eight-character project endpoint you will select for the worker. |
| `NEXTAUTH_SECRET`                            | The value printed by OpenSSL.                                             |
| `NEXTAUTH_URL`                               | `http://127.0.0.1:3000`.                                                  |
| `AUTH_DISABLED`                              | `true` for this loopback-only run.                                        |

```bash
npx --yes npm@11.19.1 ci
npm run dev -- --hostname 127.0.0.1
```

Open [the chat app](http://127.0.0.1:3000). The pool starts empty.

## 2. Start a worker

In a second terminal, select the same project for the worker's CLI context. Replace `YOUR_PROJECT_ENDPOINT` with your actual endpoint.

```bash
rstream login
```

Select the project for this example.

```bash
rstream project use YOUR_PROJECT_ENDPOINT
```

From the repository root, build and start the worker.

```bash
cd private-llm-mesh/worker
make build
./bin/worker --model qwen3:4b --tunnel-name laptop-qwen
```

If the worker runs on another machine, install the worker prerequisites and clone the repository there first. The worker uses its CLI context or [provisioned agent credentials](worker/README.md#rstream-credentials). The first build compiles the pinned llama.cpp revision, and the worker reports `worker online` with its tunnel URL when the model and tunnel are ready.

## 3. Verify a turn

1. Wait for `laptop-qwen` to appear as reachable in the worker pool.
2. Select `qwen3:4b` and send a short prompt, such as “In one sentence, explain what a tunnel does.”
3. Confirm that the reply streams into the chat and that the response identifies `laptop-qwen`.

If the pool stays empty, confirm that both roles use the same endpoint and that the worker is online. If a worker is visible but unreachable, check the application's permissions to probe `/v1/models` on that tunnel. A visit to the worker URL without a scoped token is rejected by the edge.

Stop each process with Ctrl+C when finished. The app stays open if only the worker stops, and the worker disappears from the pool.

## Add capacity or deploy

| Goal                                              | Where to go                                                                                                                                                                                                       |
| ------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Replicate a model or serve a different one        | Start another [standalone worker](worker/README.md) in the same project with a distinct tunnel name. Each process loads one GGUF model. Set `--model-id` if you need two runtimes to advertise the same model ID. |
| Run a multi-model catalog on a Docker host        | Use the [Ollama and rstream Compose worker](worker-compose/README.md). It joins the same pool and loads models on demand.                                                                                         |
| Bring your own model server                       | Implement the worker discovery and serving contract in the [web guide](web/README.md#worker-discovery-and-routing).                                                                                               |
| Enable GitHub sign-in or deploy the app           | Follow the [web guide](web/README.md#github-sign-in-and-deployment) for a Next.js server or Vercel.                                                                                                               |
| Exercise routing, streaming, and worker lifecycle | Run the [web checks](web/README.md#verification), [worker checks](worker/README.md#verification), or [qualification tools](qualification/README.md).                                                              |

The complete sample uses hosted project resolution and managed token minting. The web app can target an explicit engine for the same hosted project, and workers can use provisioned credentials for that engine. The complete mesh still needs the hosted APIs. See the [rstream architecture guide](https://rstream.io/guides/build-a-private-llm-mesh-with-rstream).
