# Private LLM mesh qualification

This optional pack qualifies the claims made by the sample without adding any
steps to its normal quickstart. The default run uses a real local GGUF model and
checks the web application, native worker, routing policy, bounded admission,
mid-generation cancellation, concurrent shutdown, and race safety. A live mode
adds the complete browser-server-rstream-worker path against an already running
mesh.

## Local qualification

Use an exact GGUF file. The runner hashes it into the manifest, owns every child
process, applies a timeout to every phase, and writes raw logs plus Markdown,
JSON, and SVG evidence under `qualification/results/`.

```bash
cd private-llm-mesh
python3 qualification/run.py \
  --model "$HOME/Library/Caches/private-llm-mesh/models/<repo>/<model>.gguf"
```

Acceptance requires all of the following:

- web lint, type checking, unit tests, production build, and dependency audit;
- worker formatting, vet, unit tests, race tests, build, static analysis, and
  vulnerability scan;
- 100 repeated race-enabled admission/shutdown lifecycle tests;
- real-model text, structured tool-call, concurrent HTTP, active cancellation,
  saturation, and concurrent-close tests, both normally and with Go's race
  detector;
- no credential-shaped value in the resulting evidence.

The local profile proves application and worker semantics. It deliberately does
not claim that a deployed rstream route, a specific network, or a remote worker
fleet was exercised.

## Live mesh qualification

Run the app with `AUTH_DISABLED=true` only in a controlled qualification
environment, start at least two equivalent workers, then add:

```bash
python3 qualification/run.py \
  --model /absolute/path/model.gguf \
  --base-url http://127.0.0.1:3000 \
  --live-model qwen3:4b \
  --turns 60 \
  --concurrency 8 \
  --min-workers 2 \
  --max-worker-share 0.65
```

The live driver validates every UI stream rather than treating HTTP 200 as
success. A response must identify its worker, contain text or tool output, end
with the protocol terminator, and contain no error part. It records no prompt or
model output. The default failure allowance is zero; latency ceilings are
environment-specific and become mandatory when passed with
`--max-ttft-p95-ms` and `--max-total-p95-ms`.

Pre-start worker loss is safe to retry against another eligible worker. Once a
model request has started, the app intentionally does not replay it: an agent
turn may already have executed a tool, so automatic replay could duplicate an
external side effect. A managed kill/recovery phase will be considered complete
only when its controlling harness owns the worker processes; manually killing a
worker is useful exploration but is not publishable evidence.

## Reading the evidence

`report.md` leads with the verdict and exact scope. `commands.svg` makes slow or
failed gates visible at a glance. When live mode is enabled,
`live-latency.svg` and `live-workers.svg` show the latency percentiles and worker
distribution, while `live.json` retains every per-turn timing and error. The
manifest pins the repository revision, dirty state, model hash, host/runtime
versions, parameters, and thresholds.

A report from a dirty worktree is diagnostic only. Commit-worthy evidence must
be rerun on the clean revision it claims to qualify.

Compact results from clean runs live in [`evidence/`](evidence). They retain the
model hash, runtime manifest, thresholds, aggregate turn data, and charts while
keeping prompts, model output, credentials, and raw process logs local.
