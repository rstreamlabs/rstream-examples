import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const script = new URL("./sanitize-artifacts.mjs", import.meta.url);
const jwt =
  "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxdWFsaWZpY2F0aW9uIn0.signature-value"; // gitleaks:allow -- synthetic redaction fixture

test("sanitizes nested qualification artifacts in place", async (context) => {
  const directory = await fs.mkdtemp(
    path.join(os.tmpdir(), "rstream-artifact-redaction-"),
  );
  context.after(() => fs.rm(directory, { recursive: true, force: true }));
  const nested = path.join(directory, "nested");
  await fs.mkdir(nested);
  const artifact = path.join(nested, "browser.log");
  await fs.writeFile(
    artifact,
    `DELETE https://camera.example/whep/session?rstream.token=${jwt} net::ERR_FAILED\n`,
  );
  const result = spawnSync(process.execPath, [script.pathname, directory], {
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  const redacted = await fs.readFile(artifact, "utf8");
  assert.doesNotMatch(redacted, /eyJ|signature-value/);
  assert.match(redacted, /rstream\.token=\[REDACTED\]/);
});
