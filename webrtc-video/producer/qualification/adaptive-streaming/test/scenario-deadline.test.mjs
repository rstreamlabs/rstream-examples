import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { collectorDeadlineSeconds } from "../lib/scenario-deadline.mjs";

test("an extended baseline leaves time for impairment, recovery and setup", () => {
  const phases = [20, 180, 45, 45, 35, 45, 1];
  assert.equal(collectorDeadlineSeconds(phases), 551);
  assert.equal(collectorDeadlineSeconds([...phases, 30]), 581);
  assert.equal(collectorDeadlineSeconds([20, 25, 45, 45, 35, 45, 1]), 396);
});

test("rejects invalid or overlong schedules before they outlive credentials", () => {
  assert.equal(collectorDeadlineSeconds([540]), 720);
  for (const phases of [[], [0], [-1], [1.5], [NaN], [Infinity], [541]]) {
    assert.throws(() => collectorDeadlineSeconds(phases), /scenario/);
  }
});

test("the runner-facing command returns a bounded deadline or an error", () => {
  const script = fileURLToPath(
    new URL("../lib/scenario-deadline.mjs", import.meta.url),
  );
  const valid = spawnSync(
    process.execPath,
    [script, "20", "180", "45", "45", "35", "45", "1"],
    { encoding: "utf8" },
  );
  assert.equal(valid.status, 0, valid.stderr);
  assert.equal(valid.stdout.trim(), "551");
  const invalid = spawnSync(process.execPath, [script, "999999999"], {
    encoding: "utf8",
  });
  assert.equal(invalid.status, 1);
  assert.equal(invalid.stdout, "");
  assert.match(invalid.stderr, /at most 540/);
});
