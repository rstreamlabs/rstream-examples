import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

test(
  "format comparison retains failed trials and stops after incomplete setup",
  { skip: process.platform === "win32" },
  () => {
    const script = fileURLToPath(
      new URL("../formats/compare-direct-test.sh", import.meta.url),
    );
    const result = spawnSync("bash", [script], {
      encoding: "utf8",
      timeout: 10_000,
    });
    assert.equal(result.error, undefined);
    assert.equal(result.status, 0, result.stderr);
  },
);
