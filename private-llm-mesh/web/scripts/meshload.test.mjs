import assert from "node:assert/strict";
import test from "node:test";

import {
  consumeUIStream,
  parseArgs,
  percentile,
  summarize,
} from "./meshload.mjs";

function stream(...chunks) {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

test("parseArgs rejects positional input", () => {
  assert.deepEqual(parseArgs(["--model", "m", "--total", "4"]), {
    model: "m",
    total: "4",
  });
  assert.throws(() => parseArgs(["m"]), /unexpected argument/);
});

test("percentile uses nearest-rank semantics", () => {
  assert.equal(percentile([4, 1, 3, 2], 50), 2);
  assert.equal(percentile([4, 1, 3, 2], 95), 4);
  assert.equal(percentile([], 50), null);
});

test("consumeUIStream validates fragmented CRLF events", async () => {
  const body = stream(
    'data: {"type":"start","messageMetadata":{"worker":"worker-a"}}\r',
    '\n\r\ndata: {"type":"text-delta","delta":"ok"}\r\n\r\n',
    "data: [DONE]\r\n\r\n",
  );
  const result = await consumeUIStream(body, performance.now());
  assert.equal(result.worker, "worker-a");
  assert.ok(result.ttft >= 0);
});

test("consumeUIStream surfaces explicit worker errors", async () => {
  const body = stream(
    'data: {"type":"start","messageMetadata":{"worker":"worker-a"}}\n\n',
    'data: {"type":"error","errorText":"inference failed"}\n\n',
    "data: [DONE]\n\n",
  );
  await assert.rejects(consumeUIStream(body), /inference failed/);
});

test("consumeUIStream rejects truncated successful-looking streams", async () => {
  const body = stream(
    'data: {"type":"start","messageMetadata":{"worker":"worker-a"}}\n\n',
    'data: {"type":"text-delta","delta":"partial"}\n\n',
  );
  await assert.rejects(consumeUIStream(body), /without \[DONE\]/);
});

test("summarize enforces failures, worker count, fairness, and latency", () => {
  const results = [
    { ok: true, worker: "a", ttftMS: 10, totalMS: 20 },
    { ok: true, worker: "a", ttftMS: 20, totalMS: 30 },
    { ok: true, worker: "b", ttftMS: 30, totalMS: 40 },
    { ok: false, error: "failed", totalMS: 5 },
  ];
  const { summary, violations } = summarize(results, 1, {
    maxFailureRate: 0,
    minWorkers: 3,
    maxWorkerShare: 0.6,
    maxTTFTP95MS: 25,
    maxTotalP95MS: 35,
  });
  assert.equal(summary.workerCount, 2);
  assert.equal(summary.ttftP95MS, 30);
  assert.equal(violations.length, 5);
});

test("summarize cannot pass an interrupted partial run", () => {
  const { summary, violations } = summarize(
    [{ ok: true, worker: "a", ttftMS: 10, totalMS: 20 }],
    1,
    {
      maxFailureRate: 1,
      minWorkers: 1,
      maxWorkerShare: 1,
      maxTTFTP95MS: 0,
      maxTotalP95MS: 0,
    },
    10,
  );
  assert.equal(summary.completed, 1);
  assert.match(violations[0], /only 1\/10 turns completed/);
});
