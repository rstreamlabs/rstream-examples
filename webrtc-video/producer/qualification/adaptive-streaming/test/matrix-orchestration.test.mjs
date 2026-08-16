import assert from "node:assert/strict";
import {
  chmod,
  copyFile,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

const matrixScript = fileURLToPath(
  new URL("../run-matrix.sh", import.meta.url),
);

test("uses a separate producer daemon only for relay matrix runs", async (t) => {
  const runtime = await mkdtemp(join(tmpdir(), "rstream-video-matrix-test-"));
  t.after(() => rm(runtime, { force: true, recursive: true }));
  const copiedMatrix = join(runtime, "run-matrix.sh");
  const fakeRunner = join(runtime, "run.sh");
  const fakeNode = join(runtime, "node");
  const callLog = join(runtime, "calls.log");
  const output = join(runtime, "artifacts");
  await copyFile(matrixScript, copiedMatrix);
  await writeFile(
    fakeRunner,
    `#!/bin/sh
printf '%s\\t%s\\t%s\\t%s\\n' \\
  "$RSTREAM_QUALIFICATION_PATH" \\
  "$RSTREAM_QUALIFICATION_PROTECTION" \\
  "\${RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST-unset}" \\
  "$1" >>"$RSTREAM_TEST_CALL_LOG"
mkdir -p "$1"
`,
  );
  await writeFile(fakeNode, "#!/bin/sh\nexit 0\n");
  await chmod(copiedMatrix, 0o700);
  await chmod(fakeRunner, 0o700);
  await chmod(fakeNode, 0o700);
  const result = spawnSync("bash", [copiedMatrix, output], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${runtime}:${process.env.PATH}`,
      RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST: "tcp://producer.test:2376",
      RSTREAM_QUALIFICATION_REPETITIONS: "1",
      RSTREAM_TEST_CALL_LOG: callLog,
    },
  });
  assert.equal(result.status, 0, result.stderr);
  const calls = (await readFile(callLog, "utf8"))
    .trim()
    .split("\n")
    .map((line) => line.split("\t").slice(0, 3));
  assert.deepEqual(calls, [
    ["direct", "nack-rtx", "unset"],
    ["relay", "nack-rtx", "tcp://producer.test:2376"],
    ["direct", "nack-rtx-flexfec", "unset"],
    ["relay", "nack-rtx-flexfec", "tcp://producer.test:2376"],
  ]);
});
