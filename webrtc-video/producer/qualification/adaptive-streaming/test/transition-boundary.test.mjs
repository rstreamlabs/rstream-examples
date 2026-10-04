import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";
import { installTransitionBoundary } from "../lib/transition-boundary.mjs";

function fixture() {
  let now = 1000;
  let sequence = 0;
  let calls = 0;
  let read = () => stats();
  const timers = new Map();
  const stats = (changes = {}) =>
    new Map([
      [
        "video",
        {
          type: "inbound-rtp",
          kind: "video",
          id: "video",
          ssrc: 123,
          timestamp: 1_000_000 + now,
          framesDecoded: 100,
          framesDropped: 0,
          freezeCount: 1,
          totalFreezesDuration: 0.4,
          ...changes,
        },
      ],
    ]);
  const peer = {
    connectionState: "connected",
    getStats() {
      calls++;
      return read();
    },
  };
  const context = {
    window: { __rstreamQualificationPeers: [peer] },
    performance: { timeOrigin: 1_000_000, now: () => now },
    setTimeout(callback, delay) {
      const id = ++sequence;
      timers.set(id, { at: now + delay, callback });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
  };
  const install = () =>
    vm.runInNewContext(`(${installTransitionBoundary.toString()})()`, context);
  install();
  const probe = () => context.window.__rstreamTransitionBoundary;
  const drainPromises = async () => {
    for (let i = 0; i < 5; i++) await Promise.resolve();
  };
  return {
    stats,
    install,
    peer,
    get calls() {
      return calls;
    },
    get timers() {
      return timers.size;
    },
    read(callback) {
      read = callback;
    },
    replacePeer() {
      context.window.__rstreamQualificationPeers.push({ ...peer });
    },
    observe(name = "viewer-network", startedAt = "2026-10-04T00:00:00Z") {
      probe().observe({ name, startedAt });
    },
    snapshot: () => JSON.parse(JSON.stringify(probe().snapshot())),
    stop: () => probe().stop(),
    async advance(at) {
      while (true) {
        const next = [...timers]
          .filter(([, t]) => t.at <= at)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        now = next[1].at;
        timers.delete(next[0]);
        next[1].callback();
        await drainPromises();
      }
      now = at;
      await drainPromises();
    },
    drainPromises,
  };
}

test("native counters that stabilized before cutoff are bracketed without counting old freezes", async () => {
  const f = fixture();
  f.observe();
  assert.equal(f.snapshot().cutoffMilliseconds, 5000);
  await f.advance(4000);
  f.observe(); // One-second polling does not reset the cutoff or allocate timers.
  assert.equal(f.timers, 3);
  f.read(() => f.stats({ freezeCount: 3, totalFreezesDuration: 1.8 }));
  await f.advance(4850);
  assert.equal(f.snapshot().status, "pending");
  await f.advance(5100);
  const result = f.snapshot();
  assert.equal(result.status, "valid");
  assert.equal(result.before.collectedAtMilliseconds, 4850);
  assert.equal(result.after.collectedAtMilliseconds, 5100);
  assert.equal(result.after.totalFreezesDurationSeconds, 1.8);
  assert.equal(f.calls, 2);
  assert.equal(f.timers, 0);
});

for (const [field, value] of [
  ["freezeCount", 2],
  ["totalFreezesDuration", 0.6],
  ["framesDropped", 1],
]) {
  test(`a ${field} increment straddling the cutoff is uncertain, never accepted`, async () => {
    const f = fixture();
    f.observe();
    await f.advance(4850);
    f.read(() => f.stats({ [field]: value }));
    await f.advance(5100);
    assert.equal(f.snapshot().status, "invalid");
    assert.equal(f.snapshot().reason, "counters-changed-across-boundary");
    assert.equal(f.timers, 0);
  });
}

test("ongoing freezes are not erased by a boundary snapshot and remain a later native increment", async () => {
  const f = fixture();
  f.observe();
  await f.advance(5100);
  const result = f.snapshot();
  assert.equal(result.status, "valid");
  const afterRenderingResumes = {
    freezeCount: 2,
    totalFreezesDurationSeconds: 1.1,
  };
  assert.equal(afterRenderingResumes.freezeCount - result.after.freezeCount, 1);
  assert.ok(
    afterRenderingResumes.totalFreezesDurationSeconds -
      result.after.totalFreezesDurationSeconds >
      0.69,
  );
});

test("cached stats from before cutoff cannot serve as the after snapshot", async () => {
  const f = fixture();
  f.observe();
  await f.advance(4850);
  f.read(() => f.stats({ timestamp: 1_004_900 }));
  await f.advance(5100);
  assert.equal(f.snapshot().reason, "stats-outside-boundary");
});

for (const [label, timestamp] of [
  ["stale", 1_004_700],
  ["future", 1_004_900],
  ["relative-clock", 4850],
  ["missing", undefined],
]) {
  test(`${label} native timestamps are rejected`, async () => {
    const f = fixture();
    f.read(() => f.stats({ timestamp }));
    f.observe();
    await f.advance(4850);
    assert.equal(f.snapshot().reason, "stats-outside-boundary");
    assert.equal(f.timers, 0);
  });
}

test("missing counters are unavailable, not implicitly zero", async () => {
  const f = fixture();
  f.read(() => f.stats({ framesDropped: undefined }));
  f.observe();
  await f.advance(4850);
  assert.equal(f.snapshot().reason, "invalid-native-counters");
});

test("FEC without decoded frames is ignored; multiple decoded streams are ambiguous", async () => {
  const f = fixture();
  f.read(() => {
    const reports = f.stats();
    reports.set("fec", {
      type: "inbound-rtp",
      kind: "video",
      id: "fec",
      framesDecoded: 0,
    });
    return reports;
  });
  f.observe();
  await f.advance(5100);
  assert.equal(f.snapshot().status, "valid");
  f.observe("recovery");
  f.read(() => {
    const reports = f.stats();
    reports.set("second", { ...reports.get("video"), id: "second" });
    return reports;
  });
  await f.advance(8950);
  assert.equal(f.snapshot().reason, "ambiguous-video-stats");
});

for (const [field, value, reason] of [
  ["id", "replacement", "inbound-stream-changed"],
  ["ssrc", 456, "inbound-stream-changed"],
  ["framesDecoded", 99, "decoded-counter-reset"],
  ["freezeCount", 0, "counters-changed-across-boundary"],
]) {
  test(`a changed or reset ${field} invalidates the boundary`, async () => {
    const f = fixture();
    f.observe();
    await f.advance(4850);
    f.read(() => f.stats({ [field]: value }));
    await f.advance(5100);
    assert.equal(f.snapshot().reason, reason);
  });
}

test("a peer replaced between snapshots invalidates the boundary", async () => {
  const f = fixture();
  f.observe();
  await f.advance(4850);
  f.replacePeer();
  await f.advance(5100);
  assert.equal(f.snapshot().reason, "peer-changed");
});

test("a hung getStats is bounded, cannot accumulate requests, and ignores late completion", async () => {
  const f = fixture();
  let resolve;
  f.read(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  f.observe();
  await f.advance(5100);
  assert.equal(f.snapshot().reason, "stats-request-overlap");
  assert.equal(f.timers, 0);
  assert.equal(f.calls, 1);
  f.observe("recovery");
  assert.equal(f.snapshot().reason, "previous-stats-request-pending");
  resolve(f.stats());
  await f.drainPromises();
  assert.equal(f.snapshot().reason, "previous-stats-request-pending");
});

test("a hung after snapshot reaches its deadline and cannot later become valid", async () => {
  const f = fixture();
  f.observe();
  await f.advance(4850);
  let resolve;
  f.read(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await f.advance(5250);
  assert.equal(f.snapshot().reason, "boundary-deadline-exceeded");
  resolve(f.stats({ timestamp: 1_005_100 }));
  await f.drainPromises();
  assert.equal(f.snapshot().status, "invalid");
  assert.equal(f.timers, 0);
});

test("phase changes and stop clear all timers; late requests cannot change another phase", async () => {
  const f = fixture();
  f.stop(); // Even before a phase exists.
  f.install();
  f.observe();
  f.observe("baseline");
  assert.equal(f.snapshot(), null);
  assert.equal(f.timers, 0);
  f.observe("source-network");
  let resolve;
  f.read(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await f.advance(4850);
  f.observe("complete");
  resolve(f.stats());
  await f.drainPromises();
  assert.equal(f.snapshot(), null);
  assert.equal(f.timers, 0);
  f.observe("recovery");
  f.stop();
  f.stop();
  assert.equal(f.snapshot().reason, "stopped");
  await f.advance(20_000);
  assert.equal(f.calls, 1);
  assert.equal(f.timers, 0);
});

test("stats rejection records a fixed reason without leaking a browser error", async () => {
  const f = fixture();
  f.read(() => {
    throw new Error("sensitive browser context");
  });
  f.observe();
  await f.advance(4850);
  assert.equal(f.snapshot().reason, "stats-request-failed");
  assert.equal(JSON.stringify(f.snapshot()).includes("sensitive"), false);
});
