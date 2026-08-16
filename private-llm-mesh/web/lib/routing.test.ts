import assert from "node:assert/strict";
import { afterEach, test } from "node:test";

import type { MeshWorker } from "./discovery";
import {
  eligibleWorkers,
  reservationCount,
  reserveWorker,
  resetReservationsForTest,
  score,
} from "./routing.ts";

function worker(id: string, overrides: Partial<MeshWorker> = {}): MeshWorker {
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
    loadObservable: true,
    observedAt: Date.now(),
    parallel: 1,
    waiting: 0,
    rtt: 10,
    reachable: true,
    ...overrides,
  };
}

afterEach(resetReservationsForTest);

test("routes only to reachable workers that currently advertise the model", () => {
  const pool = [
    worker("qwen", { models: ["qwen2.5:7b"] }),
    worker("llama", { models: ["llama3.3"] }),
    worker("down", { models: ["qwen2.5:7b"], reachable: false }),
    worker("hostless", { models: ["qwen2.5:7b"], host: "" }),
  ];
  assert.deepEqual(
    eligibleWorkers(pool, "qwen2.5:7b", new Set()).map(
      (candidate) => candidate.id,
    ),
    ["qwen"],
  );
  assert.equal(reserveWorker(pool, "mistral-nemo", new Set()), null);
});

test("normalizes active and waiting work by decoding capacity", () => {
  const pool = [
    worker("single", { load: 1, parallel: 1, rtt: 5 }),
    worker("quad", { load: 2, parallel: 4, rtt: 20 }),
    worker("queued", { load: 0, parallel: 1, waiting: 2, rtt: 1 }),
  ];
  assert.deepEqual(
    eligibleWorkers(pool, "m", new Set()).map((candidate) => candidate.id),
    ["quad", "single", "queued"],
  );
  assert.ok(score(pool[1]).normalizedLoad < score(pool[0]).normalizedLoad);
  assert.ok(score(pool[0]).normalizedLoad < score(pool[2]).normalizedLoad);
});

test("uses RTT only after normalized load, never as a weighted substitute", () => {
  const pool = [
    worker("near-busy", { load: 1, parallel: 4, rtt: 1 }),
    worker("far-idle", { load: 0, parallel: 1, rtt: 900 }),
    worker("near-idle", { load: 0, parallel: 1, rtt: 10 }),
  ];
  assert.deepEqual(
    eligibleWorkers(pool, "m", new Set()).map((candidate) => candidate.id),
    ["near-idle", "far-idle", "near-busy"],
  );
});

test("uses the injected random source only for genuinely equal scores", () => {
  const pool = [worker("first"), worker("second")];
  const first = reserveWorker(pool, "m", new Set(), () => 0);
  assert.equal(first?.worker.id, "first");
  first?.release();
  const second = reserveWorker(pool, "m", new Set(), () => 0.999999);
  assert.equal(second?.worker.id, "second");
  second?.release();
});

test("honors exclusions used by pre-start liveness failover", () => {
  const lease = reserveWorker(
    [worker("excluded"), worker("selected")],
    "m",
    new Set(["excluded"]),
  );
  assert.equal(lease?.worker.id, "selected");
  lease?.release();
});

test("local reservations prevent concurrent requests from stampeding one replica", () => {
  const ids = ["w1", "w2", "w3", "w4"];
  const pool = ids.map((id) =>
    worker(id, { loadObservable: false, observedAt: 0 }),
  );
  const leases = Array.from({ length: 40 }, () => {
    const lease = reserveWorker(pool, "m", new Set(), () => 0);
    assert.ok(lease);
    return lease;
  });
  assert.deepEqual(
    ids.map((id) => reservationCount(id)),
    [10, 10, 10, 10],
  );
  for (const lease of leases) lease.release();
  assert.deepEqual(
    ids.map((id) => reservationCount(id)),
    [0, 0, 0, 0],
  );
});

test("reservations distribute in proportion to worker capacity", () => {
  const pool = [
    worker("one", { parallel: 1, loadObservable: false, observedAt: 0 }),
    worker("four", { parallel: 4, loadObservable: false, observedAt: 0 }),
  ];
  const leases = Array.from({ length: 50 }, () => {
    const lease = reserveWorker(pool, "m", new Set(), () => 0);
    assert.ok(lease);
    return lease;
  });
  assert.equal(reservationCount("one"), 10);
  assert.equal(reservationCount("four"), 40);
  for (const lease of leases) lease.release();
});

test("pending reservations remain visible across fresh health samples", () => {
  let now = 100;
  const initial = worker("a", { loadObservable: true, observedAt: now });
  const lease = reserveWorker(
    [initial],
    "m",
    new Set(),
    () => 0,
    () => now,
  );
  assert.ok(lease);
  now += 100;
  const refreshed = worker("a", {
    load: 0,
    loadObservable: true,
    observedAt: now,
  });
  assert.deepEqual(score(refreshed, now), {
    normalizedLoad: 1,
    roundTripMilliseconds: 10,
  });
  lease.release();
});

test("dispatch state avoids double counting work visible in health", () => {
  let now = 100;
  const initial = worker("a", { loadObservable: true, observedAt: now });
  const lease = reserveWorker(
    [initial],
    "m",
    new Set(),
    () => 0,
    () => now,
  );
  assert.ok(lease);
  now += 1;
  lease.markDispatched();
  const beforeHealth = worker("a", {
    load: 0,
    loadObservable: true,
    observedAt: 100,
  });
  const afterHealth = worker("a", {
    load: 1,
    loadObservable: true,
    observedAt: now + 1,
  });
  assert.equal(score(beforeHealth, now).normalizedLoad, 1);
  assert.equal(score(afterHealth, now + 1).normalizedLoad, 1);
  lease.release();
});

test("expired reservations cannot permanently remove worker capacity", () => {
  let now = 100;
  const candidate = worker("a", { loadObservable: false, observedAt: 0 });
  const lease = reserveWorker(
    [candidate],
    "m",
    new Set(),
    () => 0,
    () => now,
  );
  assert.ok(lease);
  assert.equal(reservationCount("a", now), 1);
  now += 10 * 60 * 1000;
  assert.equal(reservationCount("a", now), 0);
  assert.equal(score(candidate, now).normalizedLoad, 0);
  lease.release();
});

test("release is idempotent", () => {
  const lease = reserveWorker(
    [worker("a", { loadObservable: false, observedAt: 0 })],
    "m",
    new Set(),
  );
  assert.ok(lease);
  assert.equal(reservationCount("a"), 1);
  lease.release();
  lease.release();
  assert.equal(reservationCount("a"), 0);
});
