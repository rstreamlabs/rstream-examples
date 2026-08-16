import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("../traffic-control.sh", import.meta.url));

async function prepareRuntime() {
  const directory = await mkdtemp(join(tmpdir(), "rstream-video-tc-test-"));
  const tcLog = join(directory, "tc.log");
  const sleepLog = join(directory, "sleep.log");
  const fakeTC = join(directory, "tc");
  const fakeSleep = join(directory, "sleep");
  await writeFile(
    fakeTC,
    `#!/bin/sh
printf '%s\\n' "$*" >>"$RSTREAM_TEST_TC_LOG"
if [ -n "\${RSTREAM_TEST_TC_FAIL_ON-}" ] && printf '%s' "$*" | grep -Fq "$RSTREAM_TEST_TC_FAIL_ON"; then
  exit 42
fi
case " $* " in
*' -j qdisc '*) printf '[{"kind":"netem","packets":%s}]\\n' "\${RSTREAM_TEST_TC_PACKETS:-1}" ;;
*' -j filter '*) printf '[{"kind":"mock"}]\\n' ;;
esac
`,
  );
  await writeFile(
    fakeSleep,
    `#!/bin/sh
printf '%s\\n' "$1" >>"$RSTREAM_TEST_SLEEP_LOG"
`,
  );
  await chmod(fakeTC, 0o700);
  await chmod(fakeSleep, 0o700);
  return { directory, fakeTC, fakeSleep, sleepLog, tcLog };
}

function argumentsFor(evidenceDirectory) {
  return [
    script,
    "--evidence-directory",
    evidenceDirectory,
    "--network-interface",
    "eth7",
    "--address-family",
    "4",
    "--destination-address",
    "192.0.2.10",
    "--destination-port",
    "3478",
    "--transport-protocol",
    "udp",
    "--match-destination-port",
    "true",
    "--queue-limit-packets",
    "256",
    "--conditioning-capacity-kbps",
    "32000",
    "--capacity-step-one-kbps",
    "16000",
    "--capacity-step-two-kbps",
    "12000",
    "--capacity-step-three-kbps",
    "8000",
    "--capacity-kbps",
    "4000",
    "--transition-step-seconds",
    "5",
    "--conditioning-seconds",
    "20",
    "--constrained-steady-seconds",
    "30",
    "--impaired-seconds",
    "35",
    "--recovery-seconds",
    "45",
    "--recovery-capacity-kbps",
    "100000",
    "--recovery-drain-seconds",
    "1",
  ];
}

test("runs one in-namespace impairment schedule and retains every snapshot", async (t) => {
  const runtime = await prepareRuntime();
  t.after(() => rm(runtime.directory, { force: true, recursive: true }));
  const evidence = join(runtime.directory, "evidence");
  const result = spawnSync("sh", argumentsFor(evidence), {
    encoding: "utf8",
    env: {
      ...process.env,
      RSTREAM_SLEEP_COMMAND: runtime.fakeSleep,
      RSTREAM_TC_COMMAND: runtime.fakeTC,
      RSTREAM_TEST_SLEEP_LOG: runtime.sleepLog,
      RSTREAM_TEST_TC_LOG: runtime.tcLog,
    },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.stdout.trim().split("\n"), [
    "conditioning-started",
    "constrained-started",
    "constrained-step-2-started",
    "constrained-step-3-started",
    "constrained-steady-started",
    "impaired-started",
    "recovery-started",
    "recovery-capacity-started",
    "recovery-drain-started",
    "recovery-complete",
  ]);
  assert.deepEqual(
    (await readFile(runtime.sleepLog, "utf8")).trim().split("\n"),
    ["20", "5", "5", "5", "30", "35", "5", "40", "1"],
  );
  const names = [
    "conditioning-start",
    "conditioning",
    "constrained-step-1-start",
    "constrained-step-1",
    "constrained-step-2-start",
    "constrained-step-2",
    "constrained-step-3-start",
    "constrained-step-3",
    "constrained-steady-start",
    "constrained-steady",
    "impaired-start",
    "impaired",
    "recovery-settle-start",
    "recovery-settle",
    "recovery-capacity-start",
    "recovery-capacity",
    "recovery-drain-start",
    "recovery-drain",
  ];
  for (const name of names) {
    assert.deepEqual(
      JSON.parse(await readFile(join(evidence, `qdisc-${name}.json`))),
      [{ kind: "netem", packets: 1 }],
    );
    assert.deepEqual(
      JSON.parse(await readFile(join(evidence, `filter-${name}.json`))),
      [{ kind: "mock" }],
    );
  }
  const tcLog = await readFile(runtime.tcLog, "utf8");
  assert.match(tcLog, /match ip dst 192\.0\.2\.10\/32/);
  assert.match(tcLog, /match ip dport 3478 0xffff/);
  assert.match(tcLog, /netem limit 256 rate 4000kbit/);
  assert.match(tcLog, /rate 32000kbit delay 0ms loss random 0%/);
  assert.match(tcLog, /rate 16000kbit delay 0ms loss random 0%/);
  assert.match(tcLog, /rate 12000kbit delay 0ms loss random 0%/);
  assert.match(tcLog, /rate 8000kbit delay 0ms loss random 0%/);
  assert.match(tcLog, /delay 120ms 30ms distribution normal loss random 2%/);
  assert.match(tcLog, /netem limit 256 rate 100000kbit/);
  assert.match(tcLog, /delay 0ms loss random 0%/);
  assert.match(tcLog, /qdisc del dev eth7 root/);
});

test("rejects malformed input before touching traffic control", async (t) => {
  const runtime = await prepareRuntime();
  t.after(() => rm(runtime.directory, { force: true, recursive: true }));
  const evidence = join(runtime.directory, "evidence");
  const args = argumentsFor(evidence);
  args[args.indexOf("--address-family") + 1] = "5";
  const result = spawnSync("sh", args, {
    encoding: "utf8",
    env: {
      ...process.env,
      RSTREAM_SLEEP_COMMAND: runtime.fakeSleep,
      RSTREAM_TC_COMMAND: runtime.fakeTC,
      RSTREAM_TEST_SLEEP_LOG: runtime.sleepLog,
      RSTREAM_TEST_TC_LOG: runtime.tcLog,
    },
  });
  assert.equal(result.status, 2);
  assert.match(result.stderr, /address family must be 4 or 6/);
  await assert.rejects(readFile(runtime.tcLog, "utf8"), /ENOENT/);
});

test("removes a partial qdisc when traffic-control setup fails", async (t) => {
  const runtime = await prepareRuntime();
  t.after(() => rm(runtime.directory, { force: true, recursive: true }));
  const evidence = join(runtime.directory, "evidence");
  const result = spawnSync("sh", argumentsFor(evidence), {
    encoding: "utf8",
    env: {
      ...process.env,
      RSTREAM_SLEEP_COMMAND: runtime.fakeSleep,
      RSTREAM_TC_COMMAND: runtime.fakeTC,
      RSTREAM_TEST_SLEEP_LOG: runtime.sleepLog,
      RSTREAM_TEST_TC_FAIL_ON: "filter add",
      RSTREAM_TEST_TC_LOG: runtime.tcLog,
    },
  });
  assert.equal(result.status, 42);
  const tcLog = await readFile(runtime.tcLog, "utf8");
  assert.match(tcLog, /qdisc add dev eth7 root/);
  assert.match(tcLog, /filter add dev eth7/);
  assert.match(tcLog, /qdisc del dev eth7 root/);
});

test("fails early and removes shaping when the selected flow carries no packets", async (t) => {
  const runtime = await prepareRuntime();
  t.after(() => rm(runtime.directory, { force: true, recursive: true }));
  const evidence = join(runtime.directory, "evidence");
  const result = spawnSync("sh", argumentsFor(evidence), {
    encoding: "utf8",
    env: {
      ...process.env,
      RSTREAM_SLEEP_COMMAND: runtime.fakeSleep,
      RSTREAM_TC_COMMAND: runtime.fakeTC,
      RSTREAM_TEST_SLEEP_LOG: runtime.sleepLog,
      RSTREAM_TEST_TC_LOG: runtime.tcLog,
      RSTREAM_TEST_TC_PACKETS: "0",
    },
  });
  assert.equal(result.status, 3);
  assert.match(result.stderr, /target carried no packets/);
  assert.match(
    await readFile(runtime.tcLog, "utf8"),
    /qdisc del dev eth7 root/,
  );
});

test("removes shaping when the scheduler is interrupted", async (t) => {
  const runtime = await prepareRuntime();
  t.after(() => rm(runtime.directory, { force: true, recursive: true }));
  const evidence = join(runtime.directory, "evidence");
  const child = spawn("sh", argumentsFor(evidence), {
    env: {
      ...process.env,
      RSTREAM_SLEEP_COMMAND: "/bin/sleep",
      RSTREAM_TC_COMMAND: runtime.fakeTC,
      RSTREAM_TEST_SLEEP_LOG: runtime.sleepLog,
      RSTREAM_TEST_TC_LOG: runtime.tcLog,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => {
    stdout += chunk;
    if (stdout.includes("conditioning-started")) {
      child.kill("SIGTERM");
    }
  });
  child.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  const result = await new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("close", (code, signal) => resolve({ code, signal }));
  });
  assert.deepEqual(result, { code: 143, signal: null }, stderr);
  assert.match(
    await readFile(runtime.tcLog, "utf8"),
    /qdisc del dev eth7 root/,
  );
});
