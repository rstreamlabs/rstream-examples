import assert from "node:assert/strict";
import test from "node:test";
import {
  containsSensitiveText,
  redactError,
  redactSensitiveText,
} from "../lib/redaction.mjs";

const jwt =
  "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxdWFsaWZpY2F0aW9uIn0.signature-value"; // gitleaks:allow -- synthetic redaction fixture

test("redacts edge, bearer, and bare JWT credentials", () => {
  const value = [
    `https://camera.example/whep?rstream.token=${jwt}&viewer=one`,
    `https%3A%2F%2Fcamera.example%2Fwhep%3Frstream%2Etoken%3D${jwt}`,
    `Authorization: Bearer ${jwt}`,
    `nested ${jwt}`,
  ].join("\n");
  const redacted = redactSensitiveText(value);
  assert.doesNotMatch(redacted, /signature-value/);
  assert.doesNotMatch(redacted, /eyJ/);
  assert.match(redacted, /rstream\.token=\[REDACTED\]&viewer=one/);
  assert.match(redacted, /rstream%2Etoken%3D\[REDACTED\]/i);
  assert.match(redacted, /Authorization: Bearer \[REDACTED\]/);
  assert.equal(containsSensitiveText(value), true);
  assert.equal(containsSensitiveText(redacted), false);
});

test("does not alter non-sensitive diagnostics", () => {
  const value = "DELETE https://camera.example/whep/session (net::ERR_FAILED)";
  assert.equal(redactSensitiveText(value), value);
});

test("redacts messages and stacks without mutating the source error", () => {
  const source = new Error(`request failed for ?rstream.token=${jwt}`);
  const redacted = redactError(source);
  assert.match(source.message, /eyJ/);
  assert.doesNotMatch(redacted.message, /eyJ/);
  assert.doesNotMatch(redacted.stack, /eyJ/);
});

test("redacts Chromium request failures before they become artifacts", () => {
  const value = `DELETE https://camera.example/whep/session?rstream.token=${jwt} net::ERR_FAILED`;
  const redacted = redactSensitiveText(value);
  assert.doesNotMatch(redacted, /eyJ|signature-value/);
  assert.match(redacted, /rstream\.token=\[REDACTED\]/);
});
