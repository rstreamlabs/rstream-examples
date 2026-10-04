import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const filter = fileURLToPath(new URL("../doctor-report.jq", import.meta.url));

function sanitize(report, exitCode) {
  const result = spawnSync(
    "jq",
    ["--argjson", "exit_code", String(exitCode), "-f", filter],
    { input: JSON.stringify(report), encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

test("publishes only known connectivity outcomes without private diagnostic text", () => {
  const secret = "private-value-must-never-leave-the-report";
  const report = sanitize(
    {
      version: secret,
      engine: secret,
      configPath: secret,
      contextName: secret,
      apiUrl: secret,
      projectEndpoint: secret,
      checks: [
        {
          name: "config",
          status: "pass",
          message: secret,
          details: { secret },
        },
        { name: "token", status: "fail", message: "token has expired" },
        { name: "dns", status: "fail", message: secret, details: { secret } },
        { name: secret, status: "fail" },
        { name: "engine", status: secret },
      ],
    },
    1,
  );
  assert.deepEqual(report.checks, [
    { name: "config", status: "pass" },
    { name: "token", status: "fail" },
    { name: "dns", status: "fail" },
  ]);
  assert.equal(report.tokenExpired, true);
  assert.equal(report.reportAvailable, true);
  assert.equal(report.scope, "runner-host");
  assert.equal(JSON.stringify(report).includes(secret), false);
});

test("retains timeout and missing-report evidence without inventing successful checks", () => {
  const report = sanitize({}, 124);
  assert.equal(report.exitCode, 124);
  assert.equal(report.reportAvailable, false);
  assert.deepEqual(report.checks, []);
  assert.equal(report.tokenExpired, false);
  assert.deepEqual(report.transportDiagnostics, []);
  assert.deepEqual(report.dnsAddressFamilies, []);
  assert.deepEqual(report.transportSelection, []);
});

test("classifies network failures and address families without retaining private endpoints", () => {
  const secret = "private-endpoint.invalid";
  const report = sanitize(
    {
      checks: [
        {
          name: "dns",
          status: "pass",
          details: {
            addresses: "192.0.2.1, 2001:db8::1,192.0.2.2",
            host: secret,
          },
        },
        {
          name: "tls",
          status: "fail",
          details: {
            error: `dial tcp ${secret}: connect: connection refused`,
            address: secret,
          },
        },
        {
          name: "quic_transport",
          status: "fail",
          details: { error: `dial ${secret}: context deadline exceeded` },
        },
        {
          name: "tunnel_transport",
          status: "fail",
          details: {
            configuredMode: "auto",
            selectedMode: "none",
            address: secret,
          },
        },
      ],
    },
    1,
  );
  assert.deepEqual(report.dnsAddressFamilies, ["ipv4", "ipv6"]);
  assert.deepEqual(report.transportDiagnostics, [
    { name: "tls", status: "fail", errorClass: "connection-refused" },
    { name: "quic_transport", status: "fail", errorClass: "timeout" },
  ]);
  assert.deepEqual(report.transportSelection, [
    { configured: "auto", selected: "none" },
  ]);
  assert.equal(JSON.stringify(report).includes(secret), false);
  assert.equal(JSON.stringify(report).includes("192.0.2"), false);
});

test("diagnostic classifications never copy unknown values or claim a successful transport failed", () => {
  const secret = "private-value";
  const cases = [
    [undefined, "unavailable"],
    [{ private: secret }, "unavailable"],
    [`${secret}: context canceled`, "canceled"],
    [`${secret}: network is unreachable`, "network-unreachable"],
    [`${secret}: no such network interface`, "local-bind"],
    [`${secret}: bind: cannot assign requested address`, "local-bind"],
    [`${secret}: x509: certificate expired`, "tls-handshake"],
    [secret, "unclassified"],
  ];
  for (const [error, expected] of cases) {
    const report = sanitize(
      {
        checks: [
          { name: "tls", status: "warn", details: { error } },
          { name: "quic_transport", status: "pass", details: { error } },
          { name: secret, status: "fail", details: { error } },
          { name: "dns", status: "pass", details: { addresses: secret } },
          {
            name: "tunnel_transport",
            status: "fail",
            details: { configuredMode: secret, selectedMode: secret },
          },
        ],
      },
      1,
    );
    assert.deepEqual(report.transportDiagnostics, [
      { name: "tls", status: "warn", errorClass: expected },
    ]);
    assert.deepEqual(report.dnsAddressFamilies, ["unrecognized"]);
    assert.deepEqual(report.transportSelection, []);
    assert.equal(JSON.stringify(report).includes(secret), false);
  }
});
