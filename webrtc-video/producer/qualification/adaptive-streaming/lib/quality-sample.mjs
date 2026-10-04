import { parseQualityState } from "../../../../shared/quality-client.ts";
import { readFile, stat } from "node:fs/promises";

const maximumResponseBytes = 16 * 1024;

export async function readSourceQualityCredential(path) {
  if ((await stat(path)).size > maximumResponseBytes)
    throw new Error("source quality credential file is too large");
  let value;
  try {
    value = JSON.parse(await readFile(path, "utf8"));
    const url = new URL(value.endpoint);
    if (
      url.protocol !== "https:" ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      url.pathname !== "/api/quality"
    )
      throw new Error();
    if (
      typeof value.authorization !== "string" ||
      value.authorization.length > 8192 ||
      /[\r\n\x00]/.test(value.authorization)
    )
      throw new Error();
  } catch {
    throw new Error("invalid source quality credential file");
  }
  return { endpoint: value.endpoint, authorization: value.authorization };
}

// Qualification-only observation of the existing source-control API. This
// credential stays in the collector and never enters the browser or evidence.
export async function collectSourceQuality(credential, fetcher = fetch) {
  const response = await fetcher(credential.endpoint, {
    headers: credential.authorization
      ? { Authorization: credential.authorization }
      : {},
    cache: "no-store",
    redirect: "error",
    signal: AbortSignal.timeout(2000),
  });
  if (response.status !== 200 || !response.body) {
    await response.body?.cancel();
    throw new Error(`source quality returned ${response.status}`);
  }
  const reader = response.body.getReader();
  const chunks = [];
  let bytes = 0;
  let complete = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        complete = true;
        break;
      }
      bytes += value.byteLength;
      if (bytes > maximumResponseBytes)
        throw new Error("source quality response is too large");
      chunks.push(value);
    }
    // Keep malformed JSON and credentials out of exception messages.
    let value;
    try {
      value = JSON.parse(Buffer.concat(chunks, bytes).toString("utf8"));
    } catch {
      throw new Error("source quality response is not JSON");
    }
    return parseQualityState(value);
  } finally {
    if (!complete) await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
