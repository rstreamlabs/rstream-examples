#!/usr/bin/env node
// Bounded load driver for the real /api/chat path. It validates the UI stream,
// records worker selection and latency, and exits non-zero when an explicit
// acceptance threshold is missed. It never records prompts or model output.

import { randomUUID } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { pathToFileURL } from "node:url";

const DEFAULT_TOTAL = 60;
const DEFAULT_CONCURRENCY = 8;
const DEFAULT_TIMEOUT_MS = 6 * 60 * 1000;

export function parseArgs(argv) {
  const out = {};
  for (let index = 0; index < argv.length; index++) {
    const argument = argv[index];
    if (!argument.startsWith("--")) {
      throw new Error(`unexpected argument: ${argument}`);
    }
    const key = argument.slice(2);
    const value =
      argv[index + 1] && !argv[index + 1].startsWith("--")
        ? argv[++index]
        : "true";
    out[key] = value;
  }
  return out;
}

function finiteNumber(value, name, { integer = false, min = 0 } = {}) {
  const parsed = Number(value);
  if (
    !Number.isFinite(parsed) ||
    parsed < min ||
    (integer && !Number.isInteger(parsed))
  ) {
    throw new Error(
      `${name} must be ${integer ? "an integer" : "a number"} >= ${min}`,
    );
  }
  return parsed;
}

export function percentile(values, percentileValue) {
  const sorted = values
    .filter(Number.isFinite)
    .sort((left, right) => left - right);
  if (sorted.length === 0) return null;
  const rank = Math.max(
    0,
    Math.ceil((percentileValue / 100) * sorted.length) - 1,
  );
  return sorted[Math.min(sorted.length - 1, rank)];
}

function eventBlocks(buffer) {
  const blocks = [];
  let match;
  while ((match = /\r?\n\r?\n/.exec(buffer))) {
    blocks.push(buffer.slice(0, match.index));
    buffer = buffer.slice(match.index + match[0].length);
  }
  return { blocks, remainder: buffer };
}

function protocolError(value) {
  if (value && typeof value === "object" && value.type === "error") {
    return String(value.errorText ?? value.error ?? "worker stream failed");
  }
  if (value && typeof value === "object" && value.error) {
    const error = value.error;
    if (typeof error === "string") return error;
    if (typeof error?.message === "string") return error.message;
    return "worker stream failed";
  }
  return null;
}

export async function consumeUIStream(body, started = performance.now()) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let worker = null;
  let ttft = null;
  let sawDone = false;
  let sawPayload = false;
  const consumeBlock = (block) => {
    for (const line of block.split(/\r?\n/)) {
      const match = /^data:\s?(.*)$/.exec(line);
      if (!match) continue;
      if (match[1] === "[DONE]") {
        sawDone = true;
        continue;
      }
      let value;
      try {
        value = JSON.parse(match[1]);
      } catch (error) {
        throw new Error(`malformed UI stream JSON: ${String(error)}`);
      }
      const error = protocolError(value);
      if (error) throw new Error(error);
      const selected =
        value?.messageMetadata?.worker ?? value?.metadata?.worker;
      if (selected && !worker) worker = String(selected);
      if (
        value?.type === "text-delta" ||
        value?.type === "tool-input-available" ||
        value?.type === "tool-output-available"
      ) {
        sawPayload = true;
        if (ttft === null) ttft = performance.now() - started;
      }
    }
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const parsed = eventBlocks(buffer);
    buffer = parsed.remainder;
    for (const block of parsed.blocks) consumeBlock(block);
  }
  buffer += decoder.decode();
  if (buffer.trim()) consumeBlock(buffer);
  if (!sawDone) throw new Error("UI stream ended without [DONE]");
  if (!worker)
    throw new Error("UI stream did not identify the selected worker");
  if (!sawPayload)
    throw new Error("UI stream completed without text or tool output");
  return { worker, ttft };
}

export async function oneTurn({
  baseURL,
  model,
  prompt,
  timeoutMS,
  signal,
  index,
}) {
  const started = performance.now();
  const timeout = AbortSignal.timeout(timeoutMS);
  const requestSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const body = {
    model,
    messages: [
      {
        id: randomUUID(),
        role: "user",
        parts: [{ type: "text", text: prompt }],
      },
    ],
  };
  try {
    const response = await fetch(`${baseURL}/api/chat`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: requestSignal,
    });
    if (response.status === 401)
      throw new Error("HTTP 401: run the app with AUTH_DISABLED=true");
    if (!response.ok || !response.body) {
      const text = await response.text().catch(() => "");
      throw new Error(`HTTP ${response.status}: ${text.slice(0, 160)}`);
    }
    const stream = await consumeUIStream(response.body, started);
    return {
      index,
      ok: true,
      worker: stream.worker,
      ttftMS: stream.ttft,
      totalMS: performance.now() - started,
    };
  } catch (error) {
    return {
      index,
      ok: false,
      error: error instanceof Error ? error.message : String(error),
      totalMS: performance.now() - started,
    };
  }
}

export function summarize(
  results,
  wallSeconds,
  thresholds,
  expectedTurns = results.length,
) {
  const successful = results.filter((result) => result.ok);
  const failed = results.filter((result) => !result.ok);
  const byWorker = {};
  for (const result of successful) {
    byWorker[result.worker] = (byWorker[result.worker] ?? 0) + 1;
  }
  const shares = Object.values(byWorker).map(
    (count) => count / Math.max(1, successful.length),
  );
  const summary = {
    turns: expectedTurns,
    completed: results.length,
    successful: successful.length,
    failed: failed.length,
    successRate: successful.length / Math.max(1, expectedTurns),
    turnsPerSecond: successful.length / Math.max(Number.EPSILON, wallSeconds),
    ttftP50MS: percentile(
      successful.map((result) => result.ttftMS),
      50,
    ),
    ttftP95MS: percentile(
      successful.map((result) => result.ttftMS),
      95,
    ),
    totalP50MS: percentile(
      successful.map((result) => result.totalMS),
      50,
    ),
    totalP95MS: percentile(
      successful.map((result) => result.totalMS),
      95,
    ),
    workers: byWorker,
    workerCount: Object.keys(byWorker).length,
    maxWorkerShare: shares.length ? Math.max(...shares) : null,
  };
  const violations = [];
  if (results.length !== expectedTurns) {
    violations.push(
      `only ${results.length}/${expectedTurns} turns completed before the run stopped`,
    );
  }
  if (1 - summary.successRate > thresholds.maxFailureRate) {
    violations.push(
      `failure rate ${(1 - summary.successRate).toFixed(3)} exceeds ${thresholds.maxFailureRate}`,
    );
  }
  if (summary.workerCount < thresholds.minWorkers) {
    violations.push(
      `only ${summary.workerCount} workers answered; expected at least ${thresholds.minWorkers}`,
    );
  }
  if (
    summary.maxWorkerShare !== null &&
    summary.maxWorkerShare > thresholds.maxWorkerShare
  ) {
    violations.push(
      `largest worker share ${summary.maxWorkerShare.toFixed(3)} exceeds ${thresholds.maxWorkerShare}`,
    );
  }
  if (
    thresholds.maxTTFTP95MS > 0 &&
    (summary.ttftP95MS ?? Infinity) > thresholds.maxTTFTP95MS
  ) {
    violations.push(
      `TTFT p95 ${Math.round(summary.ttftP95MS ?? Infinity)}ms exceeds ${thresholds.maxTTFTP95MS}ms`,
    );
  }
  if (
    thresholds.maxTotalP95MS > 0 &&
    (summary.totalP95MS ?? Infinity) > thresholds.maxTotalP95MS
  ) {
    violations.push(
      `total p95 ${Math.round(summary.totalP95MS ?? Infinity)}ms exceeds ${thresholds.maxTotalP95MS}ms`,
    );
  }
  return { summary, violations };
}

function bar(share) {
  const width = Math.round(share * 25);
  return "█".repeat(width).padEnd(25);
}

function printReport(summary, violations, results, wallSeconds) {
  console.log(`\n\n${"─".repeat(56)}`);
  console.log(
    `turns      ${summary.turns} · ok ${summary.successful} · failed ${summary.failed}`,
  );
  console.log(
    `wall       ${wallSeconds.toFixed(1)}s · ${summary.turnsPerSecond.toFixed(2)} turns/s`,
  );
  console.log(
    `TTFT       p50 ${Math.round(summary.ttftP50MS ?? 0)}ms · p95 ${Math.round(summary.ttftP95MS ?? 0)}ms`,
  );
  console.log(
    `total      p50 ${Math.round(summary.totalP50MS ?? 0)}ms · p95 ${Math.round(summary.totalP95MS ?? 0)}ms`,
  );
  console.log("\nworker distribution:");
  for (const [worker, count] of Object.entries(summary.workers).sort(
    (left, right) => right[1] - left[1],
  )) {
    const share = count / Math.max(1, summary.successful);
    console.log(
      `  ${worker.padEnd(28)} ${String(count).padStart(4)}  ${bar(share)} ${(share * 100).toFixed(1)}%`,
    );
  }
  const failed = results.filter((result) => !result.ok);
  if (failed.length) {
    console.log("\nfailures (first 5):");
    for (const failure of failed.slice(0, 5))
      console.log(`  #${failure.index}: ${failure.error}`);
  }
  if (violations.length) {
    console.log("\nacceptance violations:");
    for (const violation of violations) console.log(`  - ${violation}`);
  }
  console.log("");
}

export async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv);
  const allowedArgs = new Set([
    "base-url",
    "model",
    "total",
    "concurrency",
    "timeout-ms",
    "max-failure-rate",
    "min-workers",
    "max-worker-share",
    "max-ttft-p95-ms",
    "max-total-p95-ms",
    "prompt",
    "output",
  ]);
  for (const key of Object.keys(args)) {
    if (!allowedArgs.has(key)) throw new Error(`unknown option: --${key}`);
  }
  const baseURL = (
    args["base-url"] ??
    process.env.BASE_URL ??
    "http://localhost:3000"
  ).replace(/\/$/, "");
  const model = args.model ?? process.env.MODEL;
  if (!model) throw new Error("--model is required");
  const total = finiteNumber(args.total ?? DEFAULT_TOTAL, "--total", {
    integer: true,
    min: 1,
  });
  const concurrency = finiteNumber(
    args.concurrency ?? DEFAULT_CONCURRENCY,
    "--concurrency",
    { integer: true, min: 1 },
  );
  const timeoutMS = finiteNumber(
    args["timeout-ms"] ?? DEFAULT_TIMEOUT_MS,
    "--timeout-ms",
    { integer: true, min: 1 },
  );
  const thresholds = {
    maxFailureRate: finiteNumber(
      args["max-failure-rate"] ?? 0,
      "--max-failure-rate",
    ),
    minWorkers: finiteNumber(args["min-workers"] ?? 1, "--min-workers", {
      integer: true,
      min: 1,
    }),
    maxWorkerShare: finiteNumber(
      args["max-worker-share"] ?? 1,
      "--max-worker-share",
    ),
    maxTTFTP95MS: finiteNumber(
      args["max-ttft-p95-ms"] ?? 0,
      "--max-ttft-p95-ms",
    ),
    maxTotalP95MS: finiteNumber(
      args["max-total-p95-ms"] ?? 0,
      "--max-total-p95-ms",
    ),
  };
  if (thresholds.maxFailureRate > 1 || thresholds.maxWorkerShare > 1) {
    throw new Error("rate and share thresholds must be between 0 and 1");
  }
  const prompt = args.prompt ?? "Reply with exactly: ok";
  const controller = new AbortController();
  const stop = () => controller.abort(new Error("load run interrupted"));
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
  console.log(
    `driving ${total} turns at ${baseURL} · model=${model} · concurrency=${concurrency}`,
  );
  const results = [];
  let launched = 0;
  const started = performance.now();
  async function loop() {
    while (launched < total && !controller.signal.aborted) {
      const index = launched++;
      const result = await oneTurn({
        baseURL,
        model,
        prompt,
        timeoutMS,
        signal: controller.signal,
        index,
      });
      results.push(result);
      process.stdout.write(result.ok ? "." : "x");
    }
  }
  try {
    await Promise.all(
      Array.from({ length: Math.min(concurrency, total) }, loop),
    );
  } finally {
    process.removeListener("SIGINT", stop);
    process.removeListener("SIGTERM", stop);
  }
  const wallSeconds = (performance.now() - started) / 1000;
  results.sort((left, right) => left.index - right.index);
  const { summary, violations } = summarize(
    results,
    wallSeconds,
    thresholds,
    total,
  );
  printReport(summary, violations, results, wallSeconds);
  const evidence = {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    parameters: { baseURL, model, total, concurrency, timeoutMS, thresholds },
    wallSeconds,
    summary,
    violations,
    results,
  };
  if (args.output) {
    await mkdir(dirname(args.output), { recursive: true });
    await writeFile(args.output, `${JSON.stringify(evidence, null, 2)}\n`, {
      mode: 0o600,
    });
  }
  return violations.length === 0 ? 0 : 1;
}

const executedDirectly =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (executedDirectly) {
  main()
    .then((exitCode) => {
      process.exitCode = exitCode;
    })
    .catch((error) => {
      console.error(
        `error: ${error instanceof Error ? error.message : String(error)}`,
      );
      process.exitCode = 2;
    });
}
