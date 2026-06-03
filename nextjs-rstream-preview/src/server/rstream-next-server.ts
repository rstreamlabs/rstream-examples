import { Client } from "@rstreamlabs/runtime";
import { loadServerConfig } from "./env";
import http from "node:http";
import next from "next";

const config = loadServerConfig();
const dev = process.env.NODE_ENV !== "production";
const app = next({ dev, port: config.port });
const handle = app.getRequestHandler();
const shutdown = new AbortController();

process.once("SIGINT", () => shutdown.abort());
process.once("SIGTERM", () => shutdown.abort());

await app.prepare();

const server = http.createServer((req, res) => {
  void handle(req, res).catch((error) => {
    console.error("Next.js request failed", error);
    if (res.headersSent) {
      res.destroy(error instanceof Error ? error : undefined);
      return;
    }
    res.statusCode = 500;
    res.end("internal server error");
  });
});
const upgrade = app.getUpgradeHandler();

server.on("upgrade", (req, socket, head) => {
  void upgrade(req, socket, head).catch((error) => {
    if (!isConnectionReset(error)) {
      console.error("Next.js upgrade failed", error);
    }
    socket.destroy(error instanceof Error ? error : undefined);
  });
});

server.on("clientError", (error, socket) => {
  if (isConnectionReset(error)) {
    socket.destroy();
    return;
  }
  console.warn("HTTP client error", error.message);
  socket.end("HTTP/1.1 400 Bad Request\r\n\r\n");
});

try {
  await serveWithReconnect();
} finally {
  await app.close();
}

async function serveWithReconnect(): Promise<void> {
  let delay = config.tunnel.reconnectInitialDelayMs;
  while (!shutdown.signal.aborted) {
    try {
      await serveOnce();
      delay = config.tunnel.reconnectInitialDelayMs;
    } catch (error) {
      if (shutdown.signal.aborted) {
        return;
      }
      console.error("rstream tunnel stopped", error);
    }
    if (shutdown.signal.aborted) {
      return;
    }
    console.warn(`rstream reconnecting in ${delay}ms`);
    await sleep(delay, shutdown.signal);
    delay = Math.min(delay * 2, config.tunnel.reconnectMaxDelayMs);
  }
}

async function serveOnce(): Promise<void> {
  // The runtime SDK reads the same local context as the CLI for this demo server.
  const ctrl = await Client.fromEnv().connect();
  try {
    // Create one published HTTP tunnel and serve Next.js directly through it.
    const tunnel = await ctrl.createTunnel({
      hostname: config.tunnel.hostname,
      httpVersion: "http/1.1",
      labels: config.labels,
      name: config.tunnel.name,
      protocol: "http",
      publish: true,
      rstreamAuth: config.tunnel.rstreamAuth,
      tokenAuth: config.tunnel.tokenAuth,
    });
    try {
      console.log(`Public URL: ${await tunnel.forwardingAddress()}`);
      // tunnel.serve adapts the rstream tunnel listener to the Node HTTP server.
      await tunnel.serve(server, { signal: shutdown.signal });
    } finally {
      await tunnel
        .close()
        .catch((error) =>
          console.warn("Unable to close rstream tunnel", error),
        );
    }
  } finally {
    await ctrl
      .close()
      .catch((error) => console.warn("Unable to close rstream control", error));
  }
}

function isConnectionReset(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "ECONNRESET"
  );
}

async function sleep(ms: number, signal: AbortSignal): Promise<void> {
  await new Promise<void>((resolve) => {
    const timeout = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timeout);
        resolve();
      },
      { once: true },
    );
  });
}
