import { rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import path from "node:path";

const port = process.env.PORT ?? "3109";
const host = "127.0.0.1";
const origin = `http://${host}:${port}`;
const webhookSecret = "smoke-secret";
const dataDir = path.join(process.cwd(), ".data-smoke");

function sign(body) {
  return `sha256=${crypto
    .createHmac("sha256", webhookSecret)
    .update(body)
    .digest("hex")}`;
}

async function waitForReady(child) {
  const deadline = Date.now() + 45_000;
  let lastError;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`server exited with ${child.exitCode}`);
    }
    try {
      const response = await fetch(`${origin}/api/health`);
      const body = await response.json();
      if (response.ok && body.ok) {
        return;
      }
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw lastError ?? new Error("server did not become ready");
}

await rm(dataDir, { force: true, recursive: true });

const child = spawn(
  "npx",
  ["next", "dev", "--hostname", host, "--port", port],
  {
    env: {
      ...process.env,
      GITHUB_WEBHOOK_SECRET: webhookSecret,
      RSTREAM_TUNNEL: "0",
      WEBHOOK_DATA_DIR: dataDir,
    },
    stdio: ["ignore", "pipe", "pipe"],
  },
);

let logs = "";
child.stdout.on("data", (chunk) => {
  logs += chunk.toString();
});
child.stderr.on("data", (chunk) => {
  logs += chunk.toString();
});

try {
  await waitForReady(child);
  const payload = JSON.stringify({
    action: "opened",
    repository: { full_name: "rstreamlabs/example" },
    sender: { login: "ci" },
  });
  const response = await fetch(`${origin}/api/webhooks/github`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-github-delivery": "smoke-delivery",
      "x-github-event": "issues",
      "x-hub-signature-256": sign(payload),
    },
    body: payload,
  });
  if (!response.ok) {
    throw new Error(`webhook returned ${response.status}`);
  }
  const page = await fetch(origin);
  const html = await page.text();
  if (
    !html.includes("Webhook inbox") ||
    !html.includes("smoke-delivery") ||
    !html.includes("GitHub webhook URL")
  ) {
    throw new Error("home page did not render expected content");
  }
  const recent = await fetch(`${origin}/api/webhooks/github`);
  const body = await recent.json();
  if (body.events?.[0]?.delivery !== "smoke-delivery") {
    throw new Error("webhook delivery was not persisted");
  }
  console.log(`PASS nextjs-rstream-preview smoke: ${origin}`);
} catch (error) {
  console.error(logs);
  throw error;
} finally {
  child.kill("SIGTERM");
  await new Promise((resolve) => child.once("exit", resolve));
  await rm(dataDir, { force: true, recursive: true });
}
