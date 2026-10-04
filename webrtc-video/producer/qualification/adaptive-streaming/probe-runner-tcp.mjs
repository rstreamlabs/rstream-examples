// A credential-free, direct TCP diagnostic. This does not reproduce SDK proxy,
// interface, TLS or QUIC settings, and never publishes an endpoint or raw error.
import { readFile } from "node:fs/promises";
import { connect, isIP } from "node:net";
import { pathToFileURL } from "node:url";

const timeoutMilliseconds = 5_000;

function endpoint(report) {
  const address = report?.checks?.find(
    (check) => check?.name === "engine_address" && check.status === "pass",
  )?.details?.address;
  if (typeof address !== "string" || address.length > 512) return null;
  const match = /^(\[[0-9a-fA-F:.]+\]|[a-zA-Z0-9.-]+):([0-9]{1,5})$/.exec(
    address,
  );
  if (!match) return null;
  const port = Number(match[2]);
  if (port < 1 || port > 65535) return null;
  const host = match[1].replace(/^\[|\]$/g, "");
  if (match[1].startsWith("[") && isIP(host) !== 6) return null;
  return { host, port };
}

function errorClass(error) {
  switch (error?.code) {
    case "ETIMEDOUT":
      return "timeout";
    case "ECONNREFUSED":
      return "connection-refused";
    case "ENETUNREACH":
    case "EHOSTUNREACH":
      return "network-unreachable";
    case "ENOTFOUND":
    case "EAI_AGAIN":
      return "dns";
    case "EADDRNOTAVAIL":
      return "local-bind";
    default:
      return "unclassified";
  }
}

function probe(target, family, dependencies) {
  const { dial, schedule, clear, now } = dependencies;
  return new Promise((resolve) => {
    const start = now();
    let settled = false;
    let socket;
    let timer;
    const finish = (status, failure = null) => {
      if (settled) return;
      settled = true;
      clear(timer);
      // Keep the error listener until destruction: a late socket error must
      // neither become unhandled nor settle a second time.
      socket?.destroy();
      resolve({
        family: family === 4 ? "ipv4" : "ipv6",
        status,
        errorClass: failure,
        elapsedMilliseconds: Math.round(Math.max(0, now() - start)),
      });
    };
    try {
      socket = dial({ ...target, family });
      socket.once("connect", () => finish("pass"));
      socket.on("error", (error) => finish("fail", errorClass(error)));
      socket.once("close", () => finish("fail", "closed"));
      timer = schedule(() => finish("fail", "timeout"), timeoutMilliseconds);
    } catch (error) {
      finish("fail", errorClass(error));
    }
  });
}

export async function probeRunnerTCP(report, dependencies = {}) {
  const target = Array.isArray(report?.checks) ? endpoint(report) : null;
  const result = {
    schemaVersion: 1,
    scope: "runner-host-direct-tcp-without-sdk-transport-settings",
    endpointAvailable: target !== null,
    timeoutMilliseconds,
    probes: [],
  };
  if (!target) return result;
  const family = isIP(target.host);
  result.probes = await Promise.all(
    (family ? [family] : [4, 6]).map((value) =>
      probe(target, value, {
        dial: connect,
        schedule: setTimeout,
        clear: clearTimeout,
        now: () => performance.now(),
        ...dependencies,
      }),
    ),
  );
  return result;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  let report;
  try {
    const data = await readFile(process.argv[2]);
    if (data.length <= 1_048_576) report = JSON.parse(data.toString("utf8"));
  } catch {
    // Absent or invalid diagnostic input is unavailable, not a network pass.
  }
  process.stdout.write(
    `${JSON.stringify(await probeRunnerTCP(report), null, 2)}\n`,
  );
}
