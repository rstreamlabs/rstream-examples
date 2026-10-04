// Finite MediaMTX activation/cancellation qualification. Run through the
// distributor harness so container ownership, credentials and cleanup are shared.
import assert from "node:assert/strict";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { parseArgs } from "node:util";
import { chromium } from "playwright-core";
import { installStartupTiming } from "./lib/startup-timing.mjs";
import { parseOpenMetrics } from "./lib/openmetrics.mjs";
import {
  startQualificationViewer,
  closeServer,
} from "./lib/qualification-viewer.mjs";

const { values } = parseArgs({
  options: {
    "whep-endpoint": { type: "string" },
    "producer-metrics-url": { type: "string" },
    "output-directory": { type: "string" },
  },
});
for (const option of [
  "whep-endpoint",
  "producer-metrics-url",
  "output-directory",
])
  assert.ok(values[option], `Missing --${option}`);
const report = {
  schemaVersion: 1,
  passed: false,
  cases: [],
  cancellations: [],
  cleanup: [],
};
let browser, server;
let stage = "setup";
const abort = new AbortController();
const interrupt = () => abort.abort(new Error("Qualification interrupted"));
const timeout = setTimeout(interrupt, 210000);
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, interrupt);
const stopBrowser = () => {
  void browser?.close().catch(() => {});
};
abort.signal.addEventListener("abort", stopBrowser, { once: true });
const pause = (ms) => delay(ms, undefined, { signal: abort.signal });
const idle = (value) =>
  value.active === 0 && value.opening === 0 && value.sources === 0;
async function metrics() {
  const response = await fetch(values["producer-metrics-url"], {
    signal: AbortSignal.any([abort.signal, AbortSignal.timeout(2000)]),
    redirect: "error",
  });
  assert.ok(response.ok, "Lifecycle metrics unavailable");
  const samples = parseOpenMetrics(await response.text());
  const value = (suffix, label, expected) => {
    const sample = samples.find(
      (item) =>
        item.name === `rstream_video_producer_${suffix}` &&
        (!label || item.labels[label] === expected),
    );
    assert.ok(
      sample && Number.isFinite(sample.value),
      "Lifecycle metric missing or invalid",
    );
    return sample.value;
  };
  return {
    active: value("sessions", "state", "active"),
    opening: value("sessions", "state", "opening"),
    sources: value("source_instances"),
    created: value("whep_initial_requests_total", "outcome", "created"),
  };
}
async function inactive() {
  const startedAt = performance.now();
  while (performance.now() - startedAt < 15000) {
    const observed = await metrics();
    if (idle(observed)) {
      report.cleanup.push({
        milliseconds: performance.now() - startedAt,
        ...observed,
      });
      return observed;
    }
    await pause(50);
  }
  throw new Error("Source did not become inactive");
}
async function start(page, name, round) {
  stage = `${name}:${round}`;
  await page.waitForFunction(
    () => !document.querySelector("#connect").disabled,
  );
  const observation = {
    name,
    round,
    before: await metrics(),
    startup: null,
    after: null,
  };
  report.cases.push(observation);
  await page.evaluate(installStartupTiming);
  try {
    await page.locator("#connect").click();
    await page.waitForFunction(
      () => window.__rstreamStartupTiming.snapshot().firstFrame !== null,
      undefined,
      { timeout: 10000 },
    );
  } finally {
    observation.startup = await page
      .evaluate(() => window.__rstreamStartupTiming.snapshot())
      .catch(() => null);
  }
  observation.after = await metrics();
  assert.ok(
    observation.startup?.measurementValid,
    "First frame measurement invalid",
  );
  assert.equal(
    observation.after.active,
    1,
    "Expected one shared active source session",
  );
  assert.equal(observation.after.sources, 1, "Expected one encoder");
  assert.equal(
    observation.after.created,
    observation.before.created + (name === "cold" ? 1 : 0),
    "Unexpected source recreation",
  );
}
async function stop(page) {
  await page.locator("#disconnect").click();
  await page.waitForFunction(
    () => !document.querySelector("#connect").disabled,
  );
  const result = await page.evaluate(
    () => window.__rstreamQualificationViewer.closeResult,
  );
  assert.equal(
    result?.outcome,
    "deleted",
    "Viewer resource deletion unconfirmed",
  );
}
try {
  const viewer = await startQualificationViewer(values["whep-endpoint"]);
  server = viewer.server;
  browser = await chromium.launch({
    executablePath: "/usr/bin/chromium",
    headless: true,
    args: ["--no-sandbox", "--autoplay-policy=no-user-gesture-required"],
  });
  abort.signal.throwIfAborted();
  report.browserVersion = browser.version();
  const context = await browser.newContext();
  let pageErrors = 0;
  context.on("page", (page) => page.on("pageerror", () => pageErrors++));
  context.setDefaultTimeout(10000);
  for (let round = 1; round <= 3; round++) {
    await inactive();
    const first = await context.newPage();
    await first.goto(viewer.url);
    await start(first, "cold", round);
    await stop(first);
    await start(first, "reopen", round);
    const second = await context.newPage();
    await second.goto(viewer.url);
    await start(second, "warm-join", round);
    await stop(second);
    await second.close();
    await stop(first);
    await first.close();
    await inactive();
  }
  for (const waitMilliseconds of [0, 25, 100, 250]) {
    stage = `cancel:${waitMilliseconds}`;
    const observation = {
      waitMilliseconds,
      closeResult: null,
      stopToSettledMilliseconds: null,
      after: null,
      observations: [],
    };
    report.cancellations.push(observation);
    const page = await context.newPage();
    await page.goto(viewer.url);
    await page.waitForFunction(
      () => !document.querySelector("#connect").disabled,
    );
    await page.locator("#connect").click();
    await pause(waitMilliseconds);
    const stopRequestedAt = performance.now();
    // Exercise disposal while POST is pending, even though this minimal fixture
    // disables its Disconnect button during setup. This is not a UI acceptance test.
    await page.evaluate(() =>
      document.querySelector("#disconnect").dispatchEvent(new Event("click")),
    );
    await page.waitForFunction(
      () => !document.querySelector("#connect").disabled,
    );
    observation.stopToSettledMilliseconds = performance.now() - stopRequestedAt;
    observation.closeResult = await page.evaluate(
      () => window.__rstreamQualificationViewer.closeResult,
    );
    const settledAt = performance.now();
    // Continue beyond the 15s setup deadline plus the qualifier's 1s idle grace.
    // A transient zero immediately after Stop cannot hide delayed activation.
    while (performance.now() - settledAt < 18000) {
      observation.observations.push({
        stopElapsedMilliseconds: performance.now() - stopRequestedAt,
        settledElapsedMilliseconds: performance.now() - settledAt,
        ...(await metrics()),
      });
      await pause(100);
    }
    observation.after = await inactive();
    const tail = observation.observations.filter(
      (value) => value.settledElapsedMilliseconds >= 17000,
    );
    assert.ok(
      tail.length && tail.every(idle),
      "Canceled source did not remain quiescent",
    );
    assert.ok(
      observation.observations
        .filter((value) => value.settledElapsedMilliseconds >= 3000)
        .every(idle),
      "Source outlived confirmed cleanup plus idle grace and scheduling margin",
    );
    assert.ok(
      observation.observations.every(
        (value) => value.active <= 1 && value.sources <= 1,
      ),
      "Cancellation created duplicate sources",
    );
    assert.ok(
      ["deleted", "not-established", "already-absent"].includes(
        observation.closeResult?.outcome,
      ),
      "Cancellation left an unconfirmed resource",
    );
    await page.close();
  }
  assert.equal(pageErrors, 0, "Browser raised an unhandled exception");
  report.passed = true;
} catch (error) {
  report.failure = {
    stage,
    message:
      error instanceof assert.AssertionError
        ? error.message
        : "Startup qualification failed; inspect sanitized browser and media logs",
  };
  process.exitCode = 1;
} finally {
  clearTimeout(timeout);
  abort.signal.removeEventListener("abort", stopBrowser);
  for (const signal of ["SIGTERM", "SIGINT"]) process.off(signal, interrupt);
  try {
    await browser?.close();
    if (server) await closeServer(server);
  } catch {
    report.passed = false;
    report.cleanupFailed = true;
    process.exitCode = 1;
  }
  await writeFile(
    join(values["output-directory"], "startup-cycles.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
}
