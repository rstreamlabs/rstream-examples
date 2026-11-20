# Qualify the private LLM mesh

The local profile exercises the web app and the embedded model runtime
without a hosted account. The optional live profile sends turns through the
same Next.js streaming route as the chat UI.

## Local model and lifecycle checks

Install the [web](../web/README.md) and [worker](../worker/README.md)
prerequisites plus Python 3. Install the web dependencies with
`npx --yes npm@11.19.1 ci` in `web/` before launching the runner. From
`private-llm-mesh`, pass an exact GGUF file for a model with structured tool
support, such as the `qwen3:4b` build in the worker's model guide.

```bash
python3 qualification/run.py --model /absolute/path/to/model.gguf
```

The local profile runs the web and worker verification commands, repeats
race-enabled admission and shutdown tests 100 times, and tests real-model
text, structured tool calls, concurrent HTTP, active cancellation,
saturation, and concurrent close with and without Go's race detector.

## Live worker pool checks

Start a loopback Next.js app with `AUTH_DISABLED=true` and at least two
equivalent workers that advertise the same model ID. Use an isolated hosted
project for the run. From `private-llm-mesh`:

```bash
python3 qualification/run.py \
  --model /absolute/path/to/model.gguf \
  --base-url http://127.0.0.1:3000 \
  --live-model qwen3:4b \
  --turns 60 \
  --concurrency 8 \
  --min-workers 2 \
  --max-worker-share 0.65
```

Each turn must identify its worker, contain text or structured tool output,
reach the stream terminator, and contain no error part. The defaults allow
zero failed turns, require at least two workers, and cap the largest worker
share at 65%. Use `--max-ttft-p95-ms` and `--max-total-p95-ms` for latency
budgets chosen for this model, workload, and environment.

The driver uses the workers you started. To observe loss and recovery, run
separate controlled phases with the online pool and pause or restart workers
between them. Set the worker count and distribution thresholds for each
phase. Those runs verify routing within each phase. Measuring failure
detection requires recording the worker lifecycle and the app's observations
on a shared timeline. The app can retry auto-selection before dispatch, and
after dispatch it avoids replaying turns that may already have executed tools.

## Output

Use `--output` to select a new local output directory. By default the runner
uses `qualification/results/`. Retain the revision, model hash, runtime
versions, workload, and thresholds with any result. See the shared
[testing guide](../../docs/001-testing.md).
