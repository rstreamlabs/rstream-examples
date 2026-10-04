import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const filter = fileURLToPath(new URL("../doctor-report.jq", import.meta.url));

function sanitize(report, exitCode) {
  const result = spawnSync(
    "jq",
    ["--argjson", "exit_code", String(exitCode), "-f", filter],
    { input: JSON.stringify(report), encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

test("publishes only known connectivity outcomes without private diagnostic text", () => {
  const secret = "private-value-must-never-leave-the-report";
  const report = sanitize(
    {
      version: secret,
      engine: secret,
      configPath: secret,
      contextName: secret,
      apiUrl: secret,
      projectEndpoint: secret,
      checks: [
        {
          name: "config",
          status: "pass",
          message: secret,
          details: { secret },
        },
        { name: "token", status: "fail", message: "token has expired" },
        { name: "dns", status: "fail", message: secret, details: { secret } },
        { name: secret, status: "fail" },
        { name: "engine", status: secret },
      ],
    },
    1,
  );
  assert.deepEqual(report.checks, [
    { name: "config", status: "pass" },
    { name: "token", status: "fail" },
    { name: "dns", status: "fail" },
  ]);
  assert.equal(report.tokenExpired, true);
  assert.equal(report.reportAvailable, true);
  assert.equal(report.scope, "runner-host");
  assert.equal(JSON.stringify(report).includes(secret), false);
});

test("retains timeout and missing-report evidence without inventing successful checks", () => {
  const report = sanitize({}, 124);
  assert.equal(report.exitCode, 124);
  assert.equal(report.reportAvailable, false);
  assert.deepEqual(report.checks, []);
  assert.equal(report.tokenExpired, false);
});
