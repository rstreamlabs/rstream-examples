const grafanaPort = process.env.GRAFANA_PORT ?? "13000";
const prometheusPort = process.env.PROMETHEUS_PORT ?? "19090";
const grafanaOrigin = `http://127.0.0.1:${grafanaPort}`;
const prometheusOrigin = `http://127.0.0.1:${prometheusPort}`;

async function waitForJson(url, predicate, label) {
  const deadline = Date.now() + 90_000;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      const body = await response.json();
      if (response.ok && predicate(body)) {
        return;
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
        return;
      }
      lastError = new Error(`${label} returned ${response.status}: ${body}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw lastError ?? new Error(`${label} did not become ready`);
}

await waitForText(
  `${prometheusOrigin}/-/ready`,
  (body) => body.includes("Ready"),
  "Prometheus",
);
await waitForJson(
  `${grafanaOrigin}/api/health`,
  (body) => body.database === "ok",
  "Grafana",
);
await waitForJson(
  `${prometheusOrigin}/api/v1/query?query=up`,
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
  "Prometheus scrape",
);
console.log(`Grafana and Prometheus are ready: ${grafanaOrigin}`);
