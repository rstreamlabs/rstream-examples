#!/usr/bin/env node
// Mesh load driver — Part B of the scaling verification.
//
// Fires concurrent chat turns at a running app and tallies which worker
// answered each one, plus per-turn latency, so you can watch the mesh spread
// real load across workers and fail over when you kill one. It drives the same
// `/api/chat` endpoint the browser uses and reads the worker name the server
// tags on each answer (`messageMetadata.worker`).
//
// Prerequisites:
//   - the app running with AUTH_DISABLED=true (so the endpoint needs no session)
//   - two or more workers online serving the target model
//
// Usage:
//   node web/scripts/meshload.mjs --model qwen2.5:7b --total 60 --concurrency 8
//   BASE_URL=http://localhost:3000 node web/scripts/meshload.mjs --model qwen2.5:7b
//
// While it runs, kill one worker (Ctrl-C its process) and watch the split shift
// to the survivors with no failed turns — the server retries up to three
// workers per turn.

import { randomUUID } from "node:crypto";

const args = parseArgs(process.argv.slice(2));
const BASE = (process.env.BASE_URL ?? "http://localhost:3000").replace(
  /\/$/,
  "",
);
const MODEL = args.model ?? process.env.MODEL;
const TOTAL = Number(args.total ?? 60);
const CONCURRENCY = Number(args.concurrency ?? 8);
const PROMPT = args.prompt ?? "Reply with exactly: ok";

if (!MODEL) {
  console.error(
    "error: --model is required (e.g. --model qwen2.5:7b)\n" +
      "usage: node web/scripts/meshload.mjs --model <id> [--total 60] [--concurrency 8]",
  );
  process.exit(2);
}

/** One chat turn: POST /api/chat, read the SSE, return the answering worker + timings. */
async function oneTurn() {
  const started = performance.now();
  const body = {
    model: MODEL,
    messages: [
      {
        id: randomUUID(),
        role: "user",
        parts: [{ type: "text", text: PROMPT }],
      },
    ],
  };
  let res;
  try {
    res = await fetch(`${BASE}/api/chat`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch (err) {
    return { ok: false, error: `fetch failed: ${String(err)}` };
  }
  if (res.status === 401) {
    return {
      ok: false,
      error: "401 unauthorized — run the app with AUTH_DISABLED=true",
    };
  }
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => "");
    return { ok: false, error: `HTTP ${res.status} ${text.slice(0, 160)}` };
  }

  let worker = null;
  let ttft = null;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let sep;
    while ((sep = buf.indexOf("\n\n")) !== -1) {
      const event = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      for (const line of event.split("\n")) {
        const m = /^data:\s?(.*)$/.exec(line);
        if (!m || m[1] === "[DONE]") continue;
        let obj;
        try {
          obj = JSON.parse(m[1]);
        } catch {
          continue;
        }
        const w = obj?.messageMetadata?.worker ?? obj?.metadata?.worker;
        if (w && !worker) worker = w;
        if (
          ttft === null &&
          (obj?.type === "text-delta" ||
            obj?.type === "text-start" ||
            obj?.type === "text")
        ) {
          ttft = performance.now() - started;
        }
      }
    }
  }
  return {
    ok: true,
    worker: worker ?? "(unknown)",
    ttft,
    total: performance.now() - started,
  };
}

async function run() {
  console.log(
    `driving ${TOTAL} turns at ${BASE} · model=${MODEL} · concurrency=${CONCURRENCY}\n`,
  );
  const results = [];
  let launched = 0;
  const t0 = performance.now();
  async function loop() {
    while (launched < TOTAL) {
      launched++;
      const r = await oneTurn();
      results.push(r);
      process.stdout.write(r.ok ? (r.worker === "(unknown)" ? "?" : ".") : "x");
    }
  }
  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, TOTAL) }, loop));
  const wall = (performance.now() - t0) / 1000;
  report(results, wall);
}

function report(results, wall) {
  const ok = results.filter((r) => r.ok);
  const failed = results.filter((r) => !r.ok);
  const byWorker = new Map();
  for (const r of ok) byWorker.set(r.worker, (byWorker.get(r.worker) ?? 0) + 1);

  console.log(`\n\n${"─".repeat(48)}`);
  console.log(
    `turns      ${results.length}  ·  ok ${ok.length}  ·  failed ${failed.length}`,
  );
  console.log(
    `wall       ${wall.toFixed(1)}s  ·  ${(ok.length / wall).toFixed(2)} turns/s`,
  );
  if (ok.length) {
    console.log(
      `ttft p50   ${pct(
        ok.map((r) => r.ttft).filter((x) => x != null),
        50,
      )} ms`,
    );
    console.log(
      `total p50  ${pct(
        ok.map((r) => r.total),
        50,
      )} ms  ·  p95 ${pct(
        ok.map((r) => r.total),
        95,
      )} ms`,
    );
  }

  console.log(`\nworker distribution:`);
  const rows = [...byWorker.entries()].sort((a, b) => b[1] - a[1]);
  for (const [name, count] of rows) {
    const share = (count / ok.length) * 100;
    console.log(
      `  ${name.padEnd(28)} ${String(count).padStart(4)}  ${bar(share)} ${share.toFixed(1)}%`,
    );
  }
  if (ok.length && rows.length <= 1) {
    console.log(
      "\n  note: only one worker answered — start a second replica of this model.",
    );
  } else if (rows.length === 2) {
    console.log(
      "\n  note: with equal, stable load the router pins to the two least-loaded\n" +
        "  (top-2). Add a third replica or vary load to see it rotate.",
    );
  }
  if (failed.length) {
    console.log(`\nfailures (first 5):`);
    for (const f of failed.slice(0, 5)) console.log(`  ${f.error}`);
  }
  console.log("");
}

function pct(xs, p) {
  const s = xs.filter((x) => x != null).sort((a, b) => a - b);
  if (!s.length) return "–";
  return Math.round(
    s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))],
  );
}

function bar(share) {
  const n = Math.round(share / 4);
  return "█".repeat(n).padEnd(25);
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    if (argv[i].startsWith("--")) {
      const key = argv[i].slice(2);
      const val =
        argv[i + 1] && !argv[i + 1].startsWith("--") ? argv[++i] : "true";
      out[key] = val;
    }
  }
  return out;
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});
