import { spawn } from "node:child_process";

const project = "homelab-rstream-smoke";
const grafanaPort = "13100";
const prometheusPort = "19190";
const adminUser = "admin";
const adminPassword = "smoke-rstream";
const grafanaOrigin = `http://127.0.0.1:${grafanaPort}`;
const prometheusOrigin = `http://127.0.0.1:${prometheusPort}`;
const env = {
  ...process.env,
  COMPOSE_PROJECT_NAME: project,
  GRAFANA_ADMIN_PASSWORD: adminPassword,
  GRAFANA_ADMIN_USER: adminUser,
  GRAFANA_PORT: grafanaPort,
  PROMETHEUS_PORT: prometheusPort,
};

function run(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (chunk) => {
      output += chunk.toString();
    });
    child.stderr.on("data", (chunk) => {
      output += chunk.toString();
    });
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) {
        resolve(output);
        return;
      }
      reject(
        new Error(
          `${command} ${args.join(" ")} exited with ${code}\n${output}`,
        ),
      );
    });
  });
}

async function waitForJson(url, options, predicate, label) {
  const deadline = Date.now() + 90_000;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, options);
      const body = await response.json();
      if (response.ok && predicate(body)) {
        return body;
      }
      lastError = new Error(
        `${label} returned ${response.status}: ${JSON.stringify(body)}`,
      );
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw lastError ?? new Error(`${label} did not become ready`);
}

async function waitForText(url, predicate, label) {
  const deadline = Date.now() + 90_000;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      const body = await response.text();
      if (response.ok && predicate(body)) {
        return body;
      }
      lastError = new Error(`${label} returned ${response.status}: ${body}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw lastError ?? new Error(`${label} did not become ready`);
}

function basicAuth() {
  return `Basic ${Buffer.from(`${adminUser}:${adminPassword}`).toString("base64")}`;
}

try {
  await run("docker", [
    "compose",
    "-p",
    project,
    "up",
    "-d",
    "prometheus",
    "grafana",
  ]);
  await waitForText(
    `${prometheusOrigin}/-/ready`,
    (body) => body.includes("Ready"),
    "Prometheus",
  );
  await waitForJson(
    `${grafanaOrigin}/api/health`,
    undefined,
    (body) => body.database === "ok",
    "Grafana",
  );
  await waitForJson(
    `${grafanaOrigin}/api/datasources/name/Prometheus`,
    { headers: { authorization: basicAuth() } },
    (body) =>
      body.type === "prometheus" && body.url === "http://prometheus:9090",
    "Grafana datasource",
  );
  await waitForJson(
    `${grafanaOrigin}/api/dashboards/uid/homelab-monitoring`,
    { headers: { authorization: basicAuth() } },
    (body) => body.dashboard?.title === "Homelab Monitoring",
    "Grafana dashboard",
  );
  await waitForJson(
    `${prometheusOrigin}/api/v1/query?query=up`,
    undefined,
    (body) => {
      const jobs = new Set(
        (body.data?.result ?? [])
          .map((series) => series.metric?.job)
          .filter(Boolean),
      );
      return (
        body.status === "success" &&
        jobs.has("grafana") &&
        jobs.has("prometheus")
      );
    },
    "Prometheus query",
  );
  console.log(`PASS homelab Grafana/Prometheus smoke: ${grafanaOrigin}`);
} finally {
  await run("docker", [
    "compose",
    "-p",
    project,
    "down",
    "-v",
    "--remove-orphans",
  ]);
}
