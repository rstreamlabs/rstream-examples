import { readFile } from "node:fs/promises";
import { createServer } from "node:http";

export async function startQualificationViewer(endpoint) {
  const bundle = await readFile(new URL("../viewer.js", import.meta.url));
  const config = JSON.stringify({ authorization: "", endpoint }).replaceAll(
    "<",
    "\\u003c",
  );
  const html = `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>rstream qualification viewer</title></head>
  <body>
    <video id="video" autoplay muted playsinline></video>
    <button id="connect" disabled>Connect</button>
    <button id="disconnect" disabled>Disconnect</button>
    <select id="turn-policy"><option value="direct">Direct</option><option value="relay">Relay</option></select>
    <span id="peer-status">Peer: idle</span>
    <span id="playback-status">Idle</span>
    <span id="signaling-status">Idle</span>
    <span id="twcc-target-status">-</span>
    <span id="encoder-target-status">-</span>
    <script>window.__rstreamQualificationViewer=${config}</script>
    <script type="module" src="/viewer.js"></script>
  </body>
</html>`;
  const server = createServer((request, response) => {
    if (request.method !== "GET") {
      response.writeHead(405, { Allow: "GET" }).end();
      return;
    }
    if (request.url === "/viewer.js") {
      response
        .writeHead(200, {
          "Cache-Control": "no-store",
          "Content-Type": "application/javascript; charset=utf-8",
        })
        .end(bundle);
      return;
    }
    if (request.url === "/" || request.url === "/favicon.ico") {
      if (request.url === "/favicon.ico") {
        response.writeHead(204).end();
      } else {
        response
          .writeHead(200, {
            "Cache-Control": "no-store",
            "Content-Security-Policy":
              "default-src 'self'; connect-src http: https:; media-src blob:; script-src 'self' 'unsafe-inline'",
            "Content-Type": "text/html; charset=utf-8",
          })
          .end(html);
      }
      return;
    }
    response.writeHead(404).end();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("qualification viewer did not bind a TCP address");
  }
  return { server, url: `http://127.0.0.1:${address.port}/` };
}

export function closeServer(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
    server.closeAllConnections();
  });
}
