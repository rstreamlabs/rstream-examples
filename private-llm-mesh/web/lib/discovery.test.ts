import assert from "node:assert/strict";
import test from "node:test";

import {
  probeWorkerLiveness,
  probeWorkerTransport,
  type WorkerProbeDependencies,
} from "./worker-probe.ts";

function dependencies(
  fetcher: typeof fetch,
  now: () => number = () => 100,
): WorkerProbeDependencies {
  return {
    fetch: fetcher,
    now,
  };
}

test("probes models and optional load concurrently", async () => {
  const pending = new Map<string, (response: Response) => void>();
  const calls: string[] = [];
  const fetcher = ((input: URL | RequestInfo) => {
    const path = new URL(String(input)).pathname;
    calls.push(path);
    return new Promise<Response>((resolve) => pending.set(path, resolve));
  }) as typeof fetch;
  const probe = probeWorkerTransport(
    "worker.example",
    "test-token",
    dependencies(fetcher),
  );
  await Promise.resolve();
  assert.deepEqual(calls.sort(), ["/healthz", "/v1/models"]);
  pending.get("/v1/models")?.(Response.json({ data: [{ id: "qwen" }] }));
  pending.get("/healthz")?.(
    Response.json({ in_flight: 2, parallel: 4, waiting: 1 }),
  );
  const result = await probe;
  assert.deepEqual(result, {
    load: 2,
    loadObservable: true,
    models: ["qwen"],
    observedAt: 100,
    parallel: 4,
    reachable: true,
    rtt: 0,
    waiting: 1,
  });
});

test("treats health as optional and models as the liveness authority", async () => {
  const fetcher = (async (input: URL | RequestInfo) => {
    const path = new URL(String(input)).pathname;
    if (path === "/v1/models") {
      return Response.json({ data: [{ id: "qwen" }] });
    }
    throw new Error("stock OpenAI server has no health endpoint");
  }) as typeof fetch;
  const result = await probeWorkerTransport(
    "worker.example",
    "test-token",
    dependencies(fetcher),
  );
  assert.equal(result.reachable, true);
  assert.equal(result.loadObservable, false);
  assert.equal(result.parallel, 1);
});

test("fresh liveness performs one universal models request", async () => {
  const calls: string[] = [];
  const fetcher = (async (input: URL | RequestInfo) => {
    calls.push(new URL(String(input)).pathname);
    return Response.json({ data: [] });
  }) as typeof fetch;
  assert.equal(
    await probeWorkerLiveness(
      "worker.example",
      "test-token",
      dependencies(fetcher),
    ),
    true,
  );
  assert.deepEqual(calls, ["/v1/models"]);
});
