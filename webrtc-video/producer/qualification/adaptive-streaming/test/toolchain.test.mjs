import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const producer = new URL("../../../", import.meta.url);

test("producer container uses the module Go toolchain from an immutable image", async () => {
  const [module, dockerfile] = await Promise.all([
    readFile(new URL("go.mod", producer), "utf8"),
    readFile(new URL("Dockerfile.linux", producer), "utf8"),
  ]);
  const moduleVersion = module.match(/^go ([0-9.]+)$/m)?.[1];
  const imageVersion = dockerfile.match(/^ARG GO_VERSION=([0-9.]+)$/m)?.[1];
  assert.ok(moduleVersion, "go.mod must declare a Go version");
  assert.equal(imageVersion, moduleVersion);
  assert.match(
    dockerfile,
    /^FROM --platform=\$TARGETPLATFORM golang:\$\{GO_VERSION\}-alpine@sha256:[0-9a-f]{64} AS base$/m,
  );
});
