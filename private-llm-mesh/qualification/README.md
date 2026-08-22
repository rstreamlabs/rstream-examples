# Private LLM mesh qualification

This pack tests the worker pool at two boundaries. The local profile exercises
the application and worker with a pinned GGUF model. The live profile drives
the same UI stream used by the browser through Next.js, scoped-token minting,
rstream, and multiple independent workers.

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

The local profile qualifies application and worker semantics. Deployed routing
and worker-pool behavior belong to the live profile.

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

The live driver parses every UI stream. A response passes only when it identifies
its worker, contains text or structured tool output, ends with the protocol
terminator, and contains no error part. The default profile allows no failed
turn, requires at least two workers, and caps the largest worker share at 65%.
Latency ceilings become verdict gates when supplied with `--max-ttft-p95-ms`
and `--max-total-p95-ms`; otherwise the report records latency without treating
it as a portable SLO.

Pre-start worker loss is safe to retry against another eligible worker. Once a
model request has started, the app does not replay it because an agent turn may
already have executed a tool. A fully automated loss-and-recovery gate requires
the harness to own the worker processes and timestamp their lifecycle. The
current committed record uses controlled stop and restart boundaries and is
explicitly limited to routing behavior within those phases.

## Reading the evidence

`report.md` states the scope, method, thresholds, and result. `live.json`,
`degraded.json`, and `recovery.json` retain the worker attribution and timing for
each turn. The manifest pins the repository revision, model hash, runtime
versions, parameters, and thresholds.

A report from a dirty worktree is diagnostic only. Commit-worthy evidence must
be rerun on the clean revision it claims to qualify.

Compact results from clean runs live in [`evidence/`](evidence). They retain the
model hash, runtime manifest, thresholds, per-turn measurements, and the useful
lifecycle view while keeping prompts and model output outside the repository.
