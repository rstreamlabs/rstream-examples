import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const compose = fileURLToPath(new URL("../compose.yaml", import.meta.url));
function render(envFile) {
  return spawnSync("docker", ["compose", "--profile", "rstream", "--env-file", envFile, "-f", compose, "config", "--format", "json"], {
    env: { PATH: process.env.PATH, DOCKER_HOST: "tcp://127.0.0.1:1" }, encoding: "utf8", timeout: 10000,
  });
}

test("an unconfigured stack refuses to render a predictable Grafana password", async (t) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "rstream-homelab-config-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const envFile = path.join(directory, ".env");
  await writeFile(envFile, "", { mode: 0o600 });
  const result = render(envFile);
  assert.equal(result.error, undefined, "Docker Compose CLI must be installed; no daemon is contacted");
  assert.notEqual(result.status, 0, "configuration without a private password must fail");
  assert.match(result.stderr, /GRAFANA_ADMIN_PASSWORD/);
});

test("generated credentials persist privately and configure a non-root watcher", async (t) => {
  const { readFile, stat } = await import("node:fs/promises");
  const { configure } = await import("./configure.mjs");
  const directory = await mkdtemp(path.join(os.tmpdir(), "rstream-homelab-private-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  assert.equal(await configure(directory, "997"), true);
  const envFile = path.join(directory, ".env");
  const original = await readFile(envFile, "utf8");
  assert.equal((await stat(envFile)).mode & 0o777, 0o600);
  assert.match(original, /GRAFANA_ADMIN_PASSWORD=[A-Za-z0-9_-]{43}/);
  assert.equal(await configure(directory, "123"), false);
  assert.equal(await readFile(envFile, "utf8"), original, "rerunning setup must preserve the actual login password");
  const result = render(envFile);
  assert.equal(result.status, 0, result.stderr);
  const config = JSON.parse(result.stdout);
  assert.equal(config.services.rstream.user, "1000:1000");
  assert.equal(config.services.rstream.environment.RSTREAM_CONFIG, "/tmp/rstream-no-config.yaml");
  assert.deepEqual(config.services.rstream.group_add, ["997"]);
  assert.equal(config.services.rstream.volumes.find((volume) => volume.target === "/var/run/docker.sock").read_only, true);
  const password = original.match(/GRAFANA_ADMIN_PASSWORD=(.+)/)[1];
  assert.equal(config.services.grafana.environment.GF_SECURITY_ADMIN_PASSWORD, password);
});


test("setup refuses a public or redirected existing secret file", async (t) => {
  const { chmod, symlink } = await import("node:fs/promises");
  const { configure } = await import("./configure.mjs");
  const directory = await mkdtemp(path.join(os.tmpdir(), "rstream-homelab-mode-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const envFile = path.join(directory, ".env");
  await writeFile(envFile, "GRAFANA_ADMIN_PASSWORD=fixture", { mode: 0o600 });
  await chmod(envFile, 0o644);
  await assert.rejects(configure(directory, "0"), /mode 0600/);
  await rm(envFile);
  const target = path.join(directory, "other");
  await writeFile(target, "fixture", { mode: 0o600 });
  await symlink(target, envFile);
  await assert.rejects(configure(directory, "0"), /regular private file/);
});
