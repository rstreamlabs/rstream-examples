import assert from "node:assert/strict";
import { afterEach, test } from "node:test";

import type { MeshWorker } from "./discovery";
import { reservationCount, resetReservationsForTest } from "./routing.ts";
import { selectTurn, type TurnDependencies } from "./turn-selection.ts";

function worker(id: string, rtt: number): MeshWorker {
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
    loadObservable: false,
    observedAt: 0,
    parallel: 1,
    waiting: 0,
    rtt,
    reachable: true,
  };
}

function dependencies(
  workers: MeshWorker[],
  alive: (host: string) => boolean = () => true,
  mint: (id: string) => Promise<string> = async (id) => `token-${id}`,
): TurnDependencies {
  return {
    listWorkers: async () => workers,
    isWorkerAlive: async (host) => alive(host),
    mintConnectToken: async (id, path) => {
      assert.equal(path, "^/v1/chat/completions$");
      return mint(id);
    },
  };
}

afterEach(resetReservationsForTest);

test("pre-start liveness failure releases capacity and tries another worker", async () => {
  const workers = [worker("first", 1), worker("second", 20)];
  const probes: string[] = [];
  const turn = await selectTurn(
    "m",
    { maxAttempts: 2 },
    dependencies(workers, (host) => {
      probes.push(host);
      return host !== "first.host";
    }),
  );
  assert.equal(turn?.worker, "second");
  assert.deepEqual(probes, ["first.host", "second.host"]);
  assert.equal(reservationCount("first"), 0);
  assert.equal(reservationCount("second"), 1);
  turn?.release();
});

test("token mint failure never leaks its reservation", async () => {
  const selected = worker("worker", 1);
  await assert.rejects(
    selectTurn(
      "m",
      {},
      dependencies([selected], undefined, async () => {
        throw new Error("mint failed");
      }),
    ),
    /mint failed/,
  );
  assert.equal(reservationCount(selected.id), 0);
});

test("probe infrastructure failure never leaks its reservation", async () => {
  const selected = worker("worker", 1);
  const failingDependencies = dependencies([selected]);
  failingDependencies.isWorkerAlive = async () => {
    throw new Error("probe token failed");
  };
  await assert.rejects(
    selectTurn("m", {}, failingDependencies),
    /probe token failed/,
  );
  assert.equal(reservationCount(selected.id), 0);
});

test("successful turn holds capacity until its idempotent release", async () => {
  const selected = worker("worker", 1);
  const turn = await selectTurn("m", {}, dependencies([selected]));
  assert.ok(turn);
  assert.equal(turn.token, "token-worker");
  assert.equal(reservationCount(selected.id), 1);
  turn.markDispatched();
  turn.release();
  turn.release();
  assert.equal(reservationCount(selected.id), 0);
});

test("pinned selection fails closed instead of silently routing elsewhere", async () => {
  const workers = [worker("pinned", 1), worker("other", 2)];
  const turn = await selectTurn(
    "m",
    { workerId: "pinned" },
    dependencies(workers, (host) => host !== "pinned.host"),
  );
  assert.equal(turn, null);
  assert.equal(reservationCount("pinned"), 0);
  assert.equal(reservationCount("other"), 0);
});
