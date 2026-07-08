import assert from "node:assert/strict";
import { test } from "node:test";

import type { MeshWorker } from "./discovery";
// Explicit .ts extension so `node --test` resolves this on Node's native TS
// runner; the production imports stay extensionless.
import { eligibleWorkers, pickWorker, score } from "./routing.ts";

/** Build a synthetic worker; every field has a sane default, override as needed. */
function worker(id: string, over: Partial<MeshWorker> = {}): MeshWorker {
  return {
    name: id,
    id,
    host: `${id}.host`,
    machine: id,
    models: ["m"],
    accelerator: "cpu",
    engine: "llama.cpp",
    ctx: 8192,
    load: 0,
    rtt: 10,
    reachable: true,
    ...over,
  };
}

/** Deterministic PRNG (mulberry32) so distribution assertions never flake. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const none = new Set<string>();
const shareOf = (count: number, total: number) => count / total;

test("routes only to workers that advertise the model", () => {
  const pool = [
    worker("a", { models: ["qwen2.5:7b"] }),
    worker("b", { models: ["llama3.3"] }),
  ];
  assert.deepEqual(
    eligibleWorkers(pool, "qwen2.5:7b", none).map((w) => w.id),
    ["a"],
  );
  assert.equal(pickWorker(pool, "mistral-nemo", none), null);
});

test("a single worker can serve several models", () => {
  const multi = worker("box", {
    models: ["qwen2.5:7b", "llama3.1", "mistral-nemo"],
  });
  const pool = [multi, worker("other", { models: ["qwen2.5:7b"] })];
  for (const model of multi.models) {
    assert.ok(
      eligibleWorkers(pool, model, none).some((w) => w.id === "box"),
      `box should be eligible for ${model}`,
    );
  }
});

test("ranks by load, with latency as the tiebreak", () => {
  const pool = [
    worker("busy", { load: 2, rtt: 5 }),
    worker("far", { load: 0, rtt: 800 }),
    worker("near", { load: 0, rtt: 20 }),
  ];
  // near (0 + .02) < far (0 + .8) < busy (2 + .005)
  assert.deepEqual(
    eligibleWorkers(pool, "m", none).map((w) => w.id),
    ["near", "far", "busy"],
  );
  assert.ok(score(pool[2]) < score(pool[1]));
  assert.ok(score(pool[1]) < score(pool[0]));
});

test("skips unreachable or hostless workers", () => {
  const pool = [
    worker("down", { reachable: false }),
    worker("nohost", { host: "" }),
    worker("ok"),
  ];
  assert.deepEqual(
    eligibleWorkers(pool, "m", none).map((w) => w.id),
    ["ok"],
  );
});

test("failover: excluded workers are passed over", () => {
  const pool = [worker("a"), worker("b")];
  const chosen = pickWorker(pool, "m", new Set(["a"]));
  assert.equal(chosen?.id, "b");
});

test("returns null when the pool is empty or nothing matches", () => {
  assert.equal(pickWorker([], "m", none), null);
  assert.equal(
    pickWorker([worker("a", { reachable: false })], "m", none),
    null,
  );
});

test("bounds traffic to the two least-loaded (top-2), never the tail", () => {
  const pool = [
    worker("l0", { load: 0 }),
    worker("l1", { load: 1 }),
    worker("l5", { load: 5 }),
  ];
  const rand = mulberry32(1);
  const picks = new Map<string, number>();
  const N = 10_000;
  for (let i = 0; i < N; i++) {
    const w = pickWorker(pool, "m", none, rand);
    assert.ok(w);
    picks.set(w.id, (picks.get(w.id) ?? 0) + 1);
  }
  // The most-loaded worker is outside the top-2 and never receives a turn.
  assert.equal(picks.get("l5") ?? 0, 0);
  // The two least-loaded split the traffic roughly evenly.
  assert.ok(shareOf(picks.get("l0") ?? 0, N) > 0.45);
  assert.ok(shareOf(picks.get("l1") ?? 0, N) > 0.45);
});

test("balances evenly across equal replicas", () => {
  const pool = [worker("a"), worker("b")];
  const rand = mulberry32(7);
  const picks = new Map<string, number>();
  const N = 20_000;
  for (let i = 0; i < N; i++) {
    const w = pickWorker(pool, "m", none, rand);
    assert.ok(w);
    picks.set(w.id, (picks.get(w.id) ?? 0) + 1);
  }
  for (const id of ["a", "b"]) {
    const share = shareOf(picks.get(id) ?? 0, N);
    assert.ok(share > 0.47 && share < 0.53, `${id} share ${share.toFixed(3)}`);
  }
});

test("scales: with live load feedback, a 4-replica pool is used fairly", () => {
  // Closed loop: each pick raises that worker's in-flight load; older turns
  // complete and release load, keeping concurrency roughly constant. This is
  // how the real mesh rotates which two workers are the top-2 over time.
  const ids = ["w1", "w2", "w3", "w4"];
  const load = new Map(ids.map((id) => [id, 0]));
  const picks = new Map(ids.map((id) => [id, 0]));
  const rand = mulberry32(99);
  const inflight: string[] = [];
  const STEPS = 8000;
  const CONCURRENCY = 8;
  for (let i = 0; i < STEPS; i++) {
    const snapshot = ids.map((id) => worker(id, { load: load.get(id) ?? 0 }));
    const chosen = pickWorker(snapshot, "m", none, rand);
    assert.ok(chosen);
    picks.set(chosen.id, (picks.get(chosen.id) ?? 0) + 1);
    load.set(chosen.id, (load.get(chosen.id) ?? 0) + 1);
    inflight.push(chosen.id);
    if (inflight.length > CONCURRENCY) {
      const done = inflight.shift();
      if (done) load.set(done, Math.max(0, (load.get(done) ?? 0) - 1));
    }
  }
  const shares = ids.map((id) => shareOf(picks.get(id) ?? 0, STEPS));
  // Ideal is 25% each; assert every replica carries a real share of the load.
  for (const [i, share] of shares.entries()) {
    assert.ok(share > 0.15, `${ids[i]} share ${share.toFixed(3)} too low`);
  }
  console.log(
    "  4-replica load spread:",
    ids.map((id, i) => `${id} ${(shares[i] * 100).toFixed(1)}%`).join("  "),
  );
});
