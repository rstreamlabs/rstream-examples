# Private LLM mesh qualification report

**Verdict: PASS**

Scope: local code/model plus live mesh.
Revision: `d7a9dc47ee3474ed4594dbd818e758ed7e02e7a1` (clean worktree).
Model: `Qwen_Qwen3-4B-Instruct-2507-Q4_K_M.gguf` · 2497280736 bytes · SHA-256 `2fde00ce69dd4899c70d020845e2638353015bba0fdf161b3eb965f2bca4464e`.

## Method

The local profile exercises scheduler invariants, bounded admission, cancellation, concurrent shutdown, and real-model behavior with Go's race detector enabled. The live profile drives the complete UI stream through Next.js, scoped-token minting, rstream transit, and the selected llama.cpp worker. A turn passes only when the stream identifies its worker, contains text or tool output, terminates correctly, and contains no error part.

The live baseline sends 60 turns at concurrency 8. Worker A is then stopped before a 20-turn degraded phase at concurrency 4 and restarted before the recovery phase. The record preserves the measured start and end time of all three workloads.

## Acceptance gates

- Every UI stream must terminate cleanly with worker attribution and usable model output.
- The live baseline must use the configured minimum worker count and stay below the configured largest-worker share.
- A lifecycle record must complete every degraded turn on the surviving worker and reuse both workers after recovery.
- Admission, cancellation, shutdown, routing, web, dependency, and real-model race gates must all pass.

## Code and model gates

| Phase | Result | Wall time |
| --- | --- | ---: |
| web-verify | PASS | 16.877 s |
| worker-verify | PASS | 4.258 s |
| worker-lifecycle-stress | PASS | 4.636 s |
| worker-real-model | PASS | 15.666 s |
| worker-real-model-race | PASS | 16.618 s |
| live-mesh | PASS | 55.818 s |

## Baseline throughput and latency

60/60 turns succeeded across 2 workers at 1.08 turns/s.

| Signal | p50 | p95 |
| --- | ---: | ---: |
| Time to first token/output | 6890.6 ms | 9189.2 ms |
| Total turn | 6959.8 ms | 9200.2 ms |

## Routing through worker loss and recovery

The lifecycle sequence starts with both workers registered, stops worker A before the degraded phase, then starts it again before the recovery phase. New turns must continue without a failed stream while one worker is absent, and both workers must serve traffic again after recovery.

| Gate | Required | Observed |
| --- | --- | --- |
| Baseline completion | zero failed turns across at least two workers | 60/60; 2 workers |
| Baseline balance | largest worker share <= 65% | 50% |
| Degraded completion | zero failed turns after worker A stops | 20/20 on worker B |
| Recovery | at least two workers, largest share <= 75% | 2 workers; 50% |

The checked-in lifecycle boundaries were operator-controlled. The record therefore qualifies request routing within the three phases, including complete service by worker B while A is absent and balanced reuse after A returns. It does not measure automatic failure-detection or registration-removal time.

## Integrity and interpretation

Credential-shaped findings: **0**. Any finding fails the run and is redacted in place.
The manifest fixes the source revision, model hash, runtime versions, parameters, and thresholds. `session.json` retains each command result and duration; the per-turn JSON retains every published routing and latency aggregate.
A dirty run is useful while developing but must not be presented as qualification of a released revision.
