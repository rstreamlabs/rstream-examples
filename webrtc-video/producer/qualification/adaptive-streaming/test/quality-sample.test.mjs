import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  collectSourceQuality,
  readSourceQualityCredential,
} from "../lib/quality-sample.mjs";

const credential = {
  endpoint: "https://source.example/api/quality",
  authorization: "Bearer private-qualification-credential",
};
const state = {
  modes: [
    { id: "auto", label: "Auto", bitrateKbps: 0 },
    { id: "low", label: "Low", bitrateKbps: 1000 },
  ],
  selected: "auto",
  version: "a".repeat(32) + ":1",
  activeEncoders: 1,
  minAppliedBitrateKbps: 800,
  maxAppliedBitrateKbps: 800,
  failedUpdates: 0,
};

test("quality observation uses a private credential, a deadline and the shared schema", async () => {
  const value = await collectSourceQuality(credential, async (url, init) => {
    assert.equal(url, credential.endpoint);
    assert.equal(init.headers.Authorization, credential.authorization);
    assert.equal(init.redirect, "error");
    assert.equal(init.cache, "no-store");
    assert.ok(init.signal instanceof AbortSignal);
    return Response.json(state);
  });
  assert.deepEqual(value, state);
  assert.ok(!JSON.stringify(value).includes(credential.authorization));
  await assert.rejects(
    collectSourceQuality(credential, async () =>
      Response.json({ ...state, activeEncoders: -1 }),
    ),
    /Invalid source quality/,
  );
});

test("quality observation rejects unsupported and credential-bearing malformed responses", async () => {
  await assert.rejects(
    collectSourceQuality(
      credential,
      async () => new Response(null, { status: 204 }),
    ),
    /returned 204/,
  );
  await assert.rejects(
    collectSourceQuality(
      credential,
      async () => new Response(credential.authorization),
    ),
    (error) => {
      assert.equal(error.message, "source quality response is not JSON");
      return true;
    },
  );
});

test("quality observation cancels an oversized streaming response", async () => {
  let cancelled = false;
  const body = new ReadableStream({
    pull(controller) {
      controller.enqueue(new Uint8Array(4096));
    },
    cancel() {
      cancelled = true;
    },
  });
  await assert.rejects(
    collectSourceQuality(credential, async () => new Response(body)),
    /too large/,
  );
  assert.equal(cancelled, true);
});

test("quality credential files reject insecure targets, malformed input and oversized data without echoing secrets", async () => {
  const directory = await mkdtemp(join(tmpdir(), "rstream-quality-observer-"));
  const path = join(directory, "credential.json");
  try {
    await writeFile(path, JSON.stringify(credential), { mode: 0o600 });
    assert.deepEqual(await readSourceQualityCredential(path), credential);
    for (const value of [
      credential.authorization,
      JSON.stringify({
        ...credential,
        endpoint: "http://source.example/api/quality",
      }),
      JSON.stringify({
        ...credential,
        endpoint: "https://source.example/whep",
      }),
      JSON.stringify({
        ...credential,
        endpoint: "https://user:secret@source.example/api/quality",
      }),
      JSON.stringify({
        ...credential,
        endpoint: "https://source.example/api/quality?token=secret",
      }),
      JSON.stringify({
        ...credential,
        authorization: "secret\r\nHeader: value",
      }),
      JSON.stringify({ ...credential, authorization: "x".repeat(17000) }),
    ]) {
      await writeFile(path, value);
      await assert.rejects(readSourceQualityCredential(path), (error) => {
        assert.match(error.message, /source quality credential file/);
        assert.ok(!error.message.includes("secret"));
        assert.ok(!error.message.includes("Bearer"));
        return true;
      });
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
