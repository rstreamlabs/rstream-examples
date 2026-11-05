import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import { verifyArchive, verifyCandidate } from "./sdk-candidate.mjs";

const bytes = Buffer.from("original release bytes");
const name = "@rstreamlabs/react";
const version = "2.1.1";
const locked = {
  version,
  resolved: `https://registry.npmjs.org/${name}/-/react-${version}.tgz`,
  integrity: `sha512-${createHash("sha512").update(bytes).digest("base64")}`,
};
const candidate = {
  repository: "rstreamlabs/rstream-js",
  source: "a".repeat(40),
  runId: 1,
  artifactId: 2,
  artifactDigest: `sha256:${createHash("sha256").update(bytes).digest("hex")}`,
};

test("accepts only the reviewed package and original artifact bytes", () => {
  verifyArchive(name, version, bytes, locked);
  verifyCandidate(bytes, candidate);
});
test("rejects rebuilt or modified package bytes", () => {
  assert.throws(() =>
    verifyArchive(name, version, Buffer.from("rebuilt"), locked),
  );
});
test("rejects package version and registry substitutions", () => {
  assert.throws(() => verifyArchive(name, "2.1.2", bytes, locked));
  assert.throws(() =>
    verifyArchive(name, version, bytes, {
      ...locked,
      resolved: "file:other.tgz",
    }),
  );
});
test("rejects replacement GitHub archives", () => {
  assert.throws(() => verifyCandidate(Buffer.from("replacement"), candidate));
});
test("rejects other repositories and moving source references", () => {
  assert.throws(() =>
    verifyCandidate(bytes, { ...candidate, repository: "other/repository" }),
  );
  assert.throws(() => verifyCandidate(bytes, { ...candidate, source: "main" }));
});
test("requires positive workflow and artifact identifiers", () => {
  assert.throws(() => verifyCandidate(bytes, { ...candidate, runId: 0 }));
  assert.throws(() =>
    verifyCandidate(bytes, { ...candidate, artifactId: undefined }),
  );
});
