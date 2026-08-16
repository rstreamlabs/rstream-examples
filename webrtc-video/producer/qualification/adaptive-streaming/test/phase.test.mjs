import assert from "node:assert/strict";
import test from "node:test";
import { readPhase } from "../lib/phase.mjs";

test("retries a transient partial phase file", async () => {
  const values = [
    '{"name":"constrained"',
    '{"name":"constrained","startedAt":"2026-08-13T00:00:00Z"}',
  ];
  let reads = 0;
  const phase = await readPhase("phase.json", {
    attempts: 2,
    read: async () => values[reads++],
    wait: async () => {},
  });
  assert.equal(phase.name, "constrained");
  assert.equal(reads, 2);
});

test("fails with context after the bounded retry budget", async () => {
  await assert.rejects(
    readPhase("phase.json", {
      attempts: 3,
      read: async () => "{",
      wait: async () => {},
    }),
    /failed to read a stable phase control file phase\.json after 3 attempts/,
  );
});
