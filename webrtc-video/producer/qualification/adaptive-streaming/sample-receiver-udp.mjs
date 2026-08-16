import { execFile } from "node:child_process";
import { appendFile, readFile } from "node:fs/promises";
import os from "node:os";
import process from "node:process";
import { promisify } from "node:util";
import { pathToFileURL } from "node:url";

const execFileAsync = promisify(execFile);

export function parseLinuxUDPStats(raw) {
  const lines = raw.split("\n");
  for (let index = 0; index + 1 < lines.length; index++) {
    if (!lines[index].startsWith("Udp:")) continue;
    const names = lines[index].trim().split(/\s+/).slice(1);
    const values = lines[index + 1].trim().split(/\s+/);
    if (values[0] !== "Udp:") continue;
    const counters = Object.fromEntries(
      names.map((name, position) => [name, Number(values[position + 1])]),
    );
    return {
      datagramsReceived: counter(counters.InDatagrams),
      datagramsSent: counter(counters.OutDatagrams),
      inputErrors: counter(counters.InErrors),
      noPortDrops: counter(counters.NoPorts),
      receiveBufferDrops: counter(counters.RcvbufErrors),
      sendBufferDrops: counter(counters.SndbufErrors),
    };
  }
  throw new Error("Linux /proc/net/snmp did not contain UDP counters");
}

export function parseDarwinUDPStats(raw) {
  const value = (pattern) => {
    const match = raw.match(pattern);
    return match ? Number(match[1]) : 0;
  };
  const receivedMatch = raw.match(/^\s*(\d+) datagrams received$/m);
  const sentMatch = raw.match(/^\s*(\d+) datagrams output$/m);
  if (!receivedMatch || !sentMatch) {
    throw new Error("Darwin netstat did not contain UDP counters");
  }
  return {
    datagramsReceived: Number(receivedMatch[1]),
    datagramsSent: Number(sentMatch[1]),
    inputErrors:
      value(/^\s*(\d+) with incomplete header$/m) +
      value(/^\s*(\d+) with bad data length field$/m) +
      value(/^\s*(\d+) with bad checksum$/m),
    noPortDrops: value(/^\s*(\d+) dropped due to no socket$/m),
    receiveBufferDrops: value(/^\s*(\d+) dropped due to full socket buffers$/m),
    sendBufferDrops: 0,
  };
}

export function dockerExecArguments({ container, context, host }) {
  if (!container) throw new Error("docker container is required");
  if (context && host) {
    throw new Error("docker context and host are mutually exclusive");
  }
  const args = [];
  if (context) args.push("--context", context);
  if (host) args.push("--host", host);
  args.push("exec", container, "cat", "/proc/net/snmp");
  return args;
}

async function readCounters(
  platform,
  dockerContainer,
  dockerContext,
  dockerHost,
) {
  if (dockerContainer) {
    const { stdout } = await execFileAsync(
      "docker",
      dockerExecArguments({
        container: dockerContainer,
        context: dockerContext,
        host: dockerHost,
      }),
    );
    return parseLinuxUDPStats(stdout);
  }
  if (platform === "linux") {
    return parseLinuxUDPStats(await readFile("/proc/net/snmp", "utf8"));
  }
  if (platform === "darwin") {
    const { stdout } = await execFileAsync("netstat", [
      "-s",
      "-p",
      "udp",
      "-f",
      "inet",
    ]);
    return parseDarwinUDPStats(stdout);
  }
  throw new Error(`unsupported receiver sampler platform: ${platform}`);
}

async function readPhase(path) {
  try {
    const phase = JSON.parse(await readFile(path, "utf8"));
    return typeof phase.name === "string" ? phase.name : "unknown";
  } catch {
    return "unknown";
  }
}

async function sample(
  output,
  phaseFile,
  platform,
  dockerContainer,
  dockerContext,
  dockerHost,
) {
  const [counters, phase] = await Promise.all([
    readCounters(platform, dockerContainer, dockerContext, dockerHost),
    readPhase(phaseFile),
  ]);
  await appendFile(
    output,
    `${JSON.stringify({
      capturedAt: new Date().toISOString(),
      phase,
      source: dockerContainer
        ? "linux-docker-network-namespace"
        : platform === "linux"
          ? "linux-network-namespace"
          : "darwin-host-global",
      ...counters,
    })}\n`,
    { mode: 0o600 },
  );
}

export async function runSampler({
  output,
  phaseFile,
  intervalMilliseconds = 1000,
  platform = os.platform(),
  dockerContainer,
  dockerContext,
  dockerHost,
  signal,
}) {
  if (!output || !phaseFile)
    throw new Error("output and phaseFile are required");
  if (!Number.isInteger(intervalMilliseconds) || intervalMilliseconds < 100) {
    throw new Error("intervalMilliseconds must be an integer of at least 100");
  }
  while (!signal?.aborted) {
    const started = performance.now();
    await sample(
      output,
      phaseFile,
      platform,
      dockerContainer,
      dockerContext,
      dockerHost,
    );
    const remaining = Math.max(
      0,
      intervalMilliseconds - (performance.now() - started),
    );
    await new Promise((resolve) => {
      const finish = () => {
        clearTimeout(timeout);
        signal?.removeEventListener("abort", finish);
        resolve();
      };
      const timeout = setTimeout(finish, remaining);
      signal?.addEventListener("abort", finish, { once: true });
    });
  }
}

function counter(value) {
  return Number.isFinite(value) && value >= 0 ? value : 0;
}

function parseArguments(argv) {
  const argumentsByName = new Map();
  for (let index = 0; index < argv.length; index += 2) {
    const name = argv[index];
    const value = argv[index + 1];
    if (!name?.startsWith("--") || value === undefined) {
      throw new Error("arguments must be --name value pairs");
    }
    argumentsByName.set(name.slice(2), value);
  }
  return {
    dockerContainer: argumentsByName.get("docker-container"),
    dockerContext: argumentsByName.get("docker-context"),
    dockerHost: argumentsByName.get("docker-host"),
    intervalMilliseconds: Number(argumentsByName.get("interval-ms") || "1000"),
    output: argumentsByName.get("output"),
    phaseFile: argumentsByName.get("phase-file"),
  };
}

async function main() {
  const controller = new AbortController();
  const stop = () => controller.abort();
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
  try {
    await runSampler({
      ...parseArguments(process.argv.slice(2)),
      signal: controller.signal,
    });
  } finally {
    process.removeListener("SIGINT", stop);
    process.removeListener("SIGTERM", stop);
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  main().catch((error) => {
    process.stderr.write(
      `receiver UDP sampler failed: ${error instanceof Error ? error.message : String(error)}\n`,
    );
    process.exitCode = 1;
  });
}
