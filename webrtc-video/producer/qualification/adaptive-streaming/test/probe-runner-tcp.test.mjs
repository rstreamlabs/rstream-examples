import assert from "node:assert/strict";
import { EventEmitter, once } from "node:events";
import { createServer } from "node:net";
import test from "node:test";
import { probeRunnerTCP } from "../probe-runner-tcp.mjs";

const report = (address) => ({
  checks: [{ name: "engine_address", status: "pass", details: { address } }],
});

test(
  "a real loopback connection is released before server shutdown",
  { timeout: 3000 },
  async () => {
    const server = createServer((socket) => socket.resume());
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    try {
      const result = await probeRunnerTCP(
        report(`127.0.0.1:${server.address().port}`),
      );
      assert.equal(result.probes.length, 1);
      assert.equal(result.probes[0].status, "pass");
    } finally {
      await new Promise((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    }
  },
);

function fixture(action) {
  const sockets = [];
  const timers = new Set();
  return {
    sockets,
    timers,
    dependencies: {
      dial(options) {
        const socket = new EventEmitter();
        socket.options = options;
        socket.destroy = () => {
          socket.destroyed = true;
          queueMicrotask(() => socket.emit("close"));
        };
        sockets.push(socket);
        queueMicrotask(() => action(socket));
        return socket;
      },
      schedule(callback, delay) {
        assert.equal(delay, 5000);
        timers.add(callback);
        return callback;
      },
      clear(timer) {
        timers.delete(timer);
      },
      now: () => 100,
    },
  };
}

test("probes both DNS families, closes sockets and never copies private input", async () => {
  const f = fixture((socket) => socket.emit("connect"));
  const result = await probeRunnerTCP(
    report("private.example:443"),
    f.dependencies,
  );
  assert.deepEqual(
    f.sockets.map((s) => s.options.family),
    [4, 6],
  );
  assert.ok(f.sockets.every((s) => s.destroyed));
  assert.equal(f.timers.size, 0);
  assert.ok(
    result.probes.every((p) => p.status === "pass" && p.errorClass === null),
  );
  assert.equal(JSON.stringify(result).includes("private.example"), false);
  for (const socket of f.sockets)
    socket.emit("error", { code: "ETIMEDOUT", message: "private" });
  assert.ok(result.probes.every((p) => p.status === "pass"));
});

test("silent connections have a fixed deadline and late success cannot change timeout", async () => {
  const f = fixture(() => {});
  const pending = probeRunnerTCP(report("private.example:443"), f.dependencies);
  for (const callback of [...f.timers]) callback();
  const result = await pending;
  assert.equal(f.timers.size, 0);
  assert.ok(f.sockets.every((s) => s.destroyed));
  assert.ok(
    result.probes.every(
      (p) => p.status === "fail" && p.errorClass === "timeout",
    ),
  );
  for (const socket of f.sockets) socket.emit("connect");
  assert.ok(result.probes.every((p) => p.status === "fail"));
});

test("reports only allowlisted error classes, including synchronous dial errors", async () => {
  for (const [code, expected] of [
    ["ECONNREFUSED", "connection-refused"],
    ["ENETUNREACH", "network-unreachable"],
    ["ENOTFOUND", "dns"],
    ["EADDRNOTAVAIL", "local-bind"],
    ["private", "unclassified"],
  ]) {
    const f = fixture((socket) =>
      socket.emit("error", { code, message: "private" }),
    );
    const result = await probeRunnerTCP(
      report("private.example:443"),
      f.dependencies,
    );
    assert.ok(
      result.probes.every(
        (p) => p.status === "fail" && p.errorClass === expected,
      ),
    );
    assert.equal(JSON.stringify(result).includes("private"), false);
    assert.equal(f.timers.size, 0);
  }
  const result = await probeRunnerTCP(report("private.example:443"), {
    dial() {
      throw { code: "ECONNREFUSED" };
    },
  });
  assert.ok(result.probes.every((p) => p.errorClass === "connection-refused"));
});

test("literal addresses probe only their actual family", async () => {
  for (const [address, family] of [
    ["192.0.2.1:443", 4],
    ["[2001:db8::1]:443", 6],
  ]) {
    const f = fixture((socket) => socket.emit("connect"));
    const result = await probeRunnerTCP(report(address), f.dependencies);
    assert.deepEqual(
      f.sockets.map((s) => s.options.family),
      [family],
    );
    assert.equal(result.probes.length, 1);
    assert.equal(JSON.stringify(result).includes(address), false);
  }
});

test("unavailable, malformed and untrusted address inputs do not dial", async () => {
  for (const input of [
    undefined,
    {},
    { checks: "invalid" },
    { checks: [null, "invalid"] },
    report("https://private.example:443"),
    report("private.example:0"),
    report("private.example:65536"),
    report("private@host:443"),
    report("[abc]:443"),
    report("private.example:443/path"),
    {
      checks: [
        {
          name: "engine_address",
          status: "fail",
          details: { address: "private.example:443" },
        },
      ],
    },
  ]) {
    const result = await probeRunnerTCP(input, {
      dial() {
        assert.fail("must not dial");
      },
    });
    assert.equal(result.endpointAvailable, false);
    assert.deepEqual(result.probes, []);
  }
});

test("early close without a connect event is a failure and clears its deadline", async () => {
  const f = fixture((socket) => socket.emit("close"));
  const result = await probeRunnerTCP(
    report("private.example:443"),
    f.dependencies,
  );
  assert.ok(
    result.probes.every(
      (p) => p.status === "fail" && p.errorClass === "closed",
    ),
  );
  assert.equal(f.timers.size, 0);
});
