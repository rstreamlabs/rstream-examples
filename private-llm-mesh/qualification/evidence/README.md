# Published qualification evidence

Each directory contains a compact evidence pack produced from a clean, pinned
repository revision. The pack retains the model hash, runtime versions,
machine-readable turn results, thresholds, and the worker lifecycle view while
leaving prompts, model output, and transient process logs outside the
repository.

- [`d7a9dc4`](./d7a9dc4/report.md) — real-model race and lifecycle gates, a
  60-turn two-worker live mesh, and controlled degraded/recovery checks.
