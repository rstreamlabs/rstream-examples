import assert from "node:assert/strict";
import test from "node:test";
import {
  dockerExecArguments,
  parseDarwinUDPStats,
  parseLinuxUDPStats,
} from "../sample-receiver-udp.mjs";

test("targets a remote Docker daemon without changing the sampled command", () => {
  assert.deepEqual(
    dockerExecArguments({ container: "producer", context: "edge-eu" }),
    ["--context", "edge-eu", "exec", "producer", "cat", "/proc/net/snmp"],
  );
  assert.deepEqual(
    dockerExecArguments({
      container: "producer",
      host: "unix:///tmp/edge.sock",
    }),
    [
      "--host",
      "unix:///tmp/edge.sock",
      "exec",
      "producer",
      "cat",
      "/proc/net/snmp",
    ],
  );
});

test("rejects ambiguous Docker daemon selection", () => {
  assert.throws(
    () =>
      dockerExecArguments({
        container: "producer",
        context: "edge-eu",
        host: "unix:///tmp/edge.sock",
      }),
    /mutually exclusive/,
  );
});

test("parses Linux UDP namespace counters", () => {
  const raw = [
    "Ip: Forwarding DefaultTTL",
    "Ip: 2 64",
    "Udp: InDatagrams NoPorts InErrors OutDatagrams RcvbufErrors SndbufErrors",
    "Udp: 100 2 3 90 4 5",
  ].join("\n");
  assert.deepEqual(parseLinuxUDPStats(raw), {
    datagramsReceived: 100,
    datagramsSent: 90,
    inputErrors: 3,
    noPortDrops: 2,
    receiveBufferDrops: 4,
    sendBufferDrops: 5,
  });
});

test("parses Darwin global UDP counters", () => {
  const raw = `udp:
  200 datagrams received
    0 with incomplete header
    0 with bad data length field
    7 dropped due to no socket
    9 dropped due to full socket buffers
    184 delivered
  150 datagrams output
`;
  assert.deepEqual(parseDarwinUDPStats(raw), {
    datagramsReceived: 200,
    datagramsSent: 150,
    inputErrors: 0,
    noPortDrops: 7,
    receiveBufferDrops: 9,
    sendBufferDrops: 0,
  });
});
