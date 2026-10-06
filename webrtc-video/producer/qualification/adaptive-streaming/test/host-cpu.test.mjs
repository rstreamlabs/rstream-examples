import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

async function fixture(t, uptimes, wallTimes) {
  const directory = await mkdtemp(join(tmpdir(), "rstream-host-cpu-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  // Substitute only the two procfs inputs in a temporary copy. The actual
  // sampler, its arithmetic, signal handling, and output run unchanged.
  const source = await readFile(
    new URL("../sample-host-cpu.sh", import.meta.url),
    "utf8",
  );
  const script = join(directory, "sample.sh");
  await writeFile(
    script,
    source
      .replace("< /proc/stat", '< "${HOST_CPU_TEST_DIRECTORY}/stat"')
      .replace("< /proc/uptime", '< "${HOST_CPU_TEST_DIRECTORY}/uptime"'),
    { mode: 0o700 },
  );
  await writeFile(join(directory, "stat"), "cpu 10 0 10 80 0 0 0 0 0\n");
  await writeFile(join(directory, "uptime"), `${uptimes[0]} 0.00\n`);
  await writeFile(join(directory, "wall"), `${wallTimes[0]}\n`);
  await writeFile(join(directory, "index"), "1\n");
  await writeFile(join(directory, "uptimes"), `${uptimes.join("\n")}\n`);
  await writeFile(join(directory, "wall-times"), `${wallTimes.join("\n")}\n`);
  await writeFile(
    join(directory, "date"),
    `#!/bin/sh
case "$*" in
  *'%s%3N'*) cat "\${HOST_CPU_TEST_DIRECTORY}/wall" ;;
  *) cut -d '|' -f 2 "\${HOST_CPU_TEST_DIRECTORY}/wall" ;;
esac
`,
    { mode: 0o700 },
  );
  await writeFile(
    join(directory, "sleep"),
    `#!/bin/sh
set -eu
read -r index < "\${HOST_CPU_TEST_DIRECTORY}/index"
index=$((index + 1))
uptime="$(sed -n "\${index}p" "\${HOST_CPU_TEST_DIRECTORY}/uptimes")"
if [ -z "\${uptime}" ]; then
  kill -TERM "$PPID"
  exit 0
fi
printf '%s 0.00\\n' "\${uptime}" > "\${HOST_CPU_TEST_DIRECTORY}/uptime"
sed -n "\${index}p" "\${HOST_CPU_TEST_DIRECTORY}/wall-times" > "\${HOST_CPU_TEST_DIRECTORY}/wall"
printf '%s\\n' "\${index}" > "\${HOST_CPU_TEST_DIRECTORY}/index"
`,
    { mode: 0o700 },
  );
  return {
    directory,
    script,
    output: join(directory, "samples.jsonl"),
    env: {
      ...process.env,
      PATH: `${directory}:${process.env.PATH}`,
      HOST_CPU_TEST_DIRECTORY: directory,
    },
  };
}

function utc(epoch) {
  return `${epoch}|${new Date(epoch).toISOString()}`;
}

async function run(f) {
  const result = spawnSync(f.script, [f.output], {
    encoding: "utf8",
    env: f.env,
    timeout: 5000,
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
  return (await readFile(f.output, "utf8")).trim().split("\n").map(JSON.parse);
}

test("realtime steps do not become scheduler stalls, but a boot-time pause does", async (t) => {
  const f = await fixture(
    t,
    ["100.00", "100.25", "100.50", "101.25"],
    [100000, 100650, 100300, 101050].map(utc),
  );
  const samples = await run(f);
  assert.deepEqual(
    samples.map((s) => s.gapMilliseconds),
    [0, 250, 250, 750],
  );
  assert.deepEqual(
    samples.map((s) => s.bootMilliseconds),
    [100000, 100250, 100500, 101250],
  );
  assert.ok(samples.every((s) => s.gapClock === "linux-boottime"));
  assert.equal(samples[2].capturedAt, new Date(100300).toISOString());
});

test("boot-time fractions 08 and 09 use decimal arithmetic", async (t) => {
  const times = [0, 80, 90, 100, 1000];
  const f = await fixture(
    t,
    ["0.00", "0.08", "0.09", "0.10", "1.00"],
    times.map(utc),
  );
  const samples = await run(f);
  assert.deepEqual(
    samples.map((s) => s.bootMilliseconds),
    times,
  );
  assert.deepEqual(
    samples.map((s) => s.gapMilliseconds),
    [0, 80, 10, 10, 900],
  );
});

test("terminating a sampler also terminates and reaps its pending sleep", async (t) => {
  const f = await fixture(t, ["100.00"], [utc(100000)]);
  await writeFile(
    join(f.directory, "sleep"),
    `#!/bin/sh
printf '%s\\n' "$$" > "\${HOST_CPU_TEST_DIRECTORY}/sleep.pid"
exec /bin/sleep "$1"
`,
    { mode: 0o700 },
  );
  const child = spawn(f.script, [f.output, "60000"], {
    env: f.env,
    stdio: "ignore",
  });
  const exited = once(child, "exit");
  let sleeper;
  t.after(() => {
    child.kill("SIGKILL");
    if (sleeper) {
      try {
        process.kill(sleeper, "SIGKILL");
      } catch {}
    }
  });
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = await readFile(join(f.directory, "sleep.pid"), "utf8").catch(
      () => "",
    );
    if (/^[1-9][0-9]*\s*$/.test(value)) {
      sleeper = Number(value.trim());
      break;
    }
    await delay(10);
  }
  assert.ok(sleeper, "sampler did not start its timer");
  child.kill("SIGTERM");
  const timeout = new AbortController();
  try {
    const result = await Promise.race([
      exited,
      delay(2000, "timeout", { signal: timeout.signal }),
    ]);
    assert.notEqual(result, "timeout", "sampler did not stop promptly");
    assert.equal(result[0], 0);
    assert.throws(() => process.kill(sleeper, 0), { code: "ESRCH" });
  } finally {
    timeout.abort();
  }
});
