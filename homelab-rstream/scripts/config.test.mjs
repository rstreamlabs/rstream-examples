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
  assert.equal(Object.hasOwn(config.services.grafana.labels, "rstream.tunnel.grafana.host"), false, "automatic addressing must omit the optional host label");
  assert.equal(config.services.rstream.user, "1000:1000");
  assert.equal(config.services.rstream.environment.RSTREAM_CONFIG, "/tmp/rstream-no-config.yaml");
  assert.deepEqual(config.services.rstream.group_add, ["997"]);
  assert.equal(config.services.rstream.volumes.find((volume) => volume.target === "/var/run/docker.sock").read_only, true);
  const password = original.match(/GRAFANA_ADMIN_PASSWORD=(.+)/)[1];
  assert.equal(config.services.grafana.environment.GF_SECURITY_ADMIN_PASSWORD, password);
});

test("a stable-domain override is selected by the retained Compose configuration", async (t) => {
  const { readFile } = await import("node:fs/promises");
  const { configure } = await import("./configure.mjs");
  const directory = await mkdtemp(path.join(os.tmpdir(), "rstream-homelab-host-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await configure(directory, "997");
  const envFile = path.join(directory, ".env");
  const original = await readFile(envFile, "utf8");
  const selection = "\nCOMPOSE_PATH_SEPARATOR=:\nCOMPOSE_FILE=compose.yaml:compose.hostname.yaml\n";
  await writeFile(envFile, `${original}${selection}RSTREAM_GRAFANA_HOSTNAME=grafana.example.test\n`, { mode: 0o600 });
  const renderSelected = () => spawnSync("docker", ["compose", "--profile", "rstream", "--env-file", envFile, "config", "--format", "json"], {
    cwd: path.dirname(compose), env: { PATH: process.env.PATH, DOCKER_HOST: "tcp://127.0.0.1:1" }, encoding: "utf8", timeout: 10000,
  });
  const result = renderSelected();
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
  const config = JSON.parse(result.stdout);
  assert.equal(config.services.grafana.labels["rstream.tunnel.grafana.host"], "grafana.example.test");
  assert.equal(config.services.grafana.labels["rstream.tunnel.grafana.http.version"], "http/1.1");
  await writeFile(envFile, `${original}${selection}`, { mode: 0o600 });
  const missingHost = renderSelected();
  assert.notEqual(missingHost.status, 0, "explicit stable-domain selection requires a hostname");
  assert.match(missingHost.stderr, /RSTREAM_GRAFANA_HOSTNAME/);
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
