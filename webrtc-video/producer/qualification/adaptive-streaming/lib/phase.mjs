import { readFile } from "node:fs/promises";

export async function readPhase(
  path,
  { attempts = 10, read = readFile, wait = delay } = {},
) {
  let lastError;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      const raw = await read(path, "utf8");
      const phase = JSON.parse(raw);
      if (
        typeof phase.name !== "string" ||
        typeof phase.startedAt !== "string"
      ) {
        throw new Error("phase must contain string name and startedAt fields");
      }
      return phase;
    } catch (error) {
      lastError = normalizeError(error);
      if (attempt < attempts) {
        await wait(attempt * 10);
      }
    }
  }
  throw new Error(
    `failed to read a stable phase control file ${path} after ${attempts} attempts: ${lastError?.message || "unknown error"}`,
    { cause: lastError },
  );
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function normalizeError(error) {
  return error instanceof Error ? error : new Error(String(error));
}
