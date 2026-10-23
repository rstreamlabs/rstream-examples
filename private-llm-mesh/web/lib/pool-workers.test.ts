import assert from "node:assert/strict";
import test from "node:test";
import { livePoolWorkers } from "./pool-workers.ts";

test("projected inventory cannot introduce a worker without an identity", () => {
  assert.deepEqual(
    livePoolWorkers(
      [
        { status: "online", name: "worker" },
        { status: "online", id: "", name: "worker" },
        { status: "online", id: "worker" },
        { id: "worker", name: "worker" },
        { status: "offline", id: "worker", name: "worker" },
      ],
      [],
    ),
    [],
  );
});

test("visible worker identity remains usable when optional fields are projected out", () => {
  assert.deepEqual(
    livePoolWorkers([{ status: "online", id: "worker", name: "Worker" }], []),
    [
      {
        id: "worker",
        name: "Worker",
        machine: "",
        models: [],
        accelerator: "unknown",
        engine: "unknown",
        load: null,
        rtt: null,
        reachable: true,
      },
    ],
  );
});

test("live inventory uses only matching probe metrics and removes absent workers", () => {
  const probed = [
    {
      id: "worker",
      name: "Worker",
      machine: "host",
      models: ["probed-model"],
      accelerator: "cpu",
      engine: "llama",
      load: 2,
      rtt: 3,
      reachable: false,
    },
  ];
  const workers = livePoolWorkers(
    [
      {
        status: "online",
        id: "worker",
        name: "Worker",
        labels: { models: "stale-model" },
      },
      {
        status: "online",
        id: "other",
        name: "Other",
        labels: { models: "label-model," },
      },
    ],
    probed,
  );
  assert.deepEqual(
    workers.map(({ id, models, load, rtt, reachable }) => ({
      id,
      models,
      load,
      rtt,
      reachable,
    })),
    [
      {
        id: "worker",
        models: ["probed-model"],
        load: 2,
        rtt: 3,
        reachable: false,
      },
      {
        id: "other",
        models: ["label-model"],
        load: null,
        rtt: null,
        reachable: true,
      },
    ],
  );
  assert.deepEqual(livePoolWorkers([], probed), []);
});
