import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";
import { installFrameDiagnostics } from "../lib/frame-diagnostics.mjs";

function fixture(supported = true) {
  let now = 0;
  let callback;
  let timer;
  let scheduled = 0;
  let canceled = 0;
  let cleared = 0;
  const video = supported
    ? {
        requestVideoFrameCallback(fn) {
          callback = fn;
          return ++scheduled;
        },
        cancelVideoFrameCallback() {
          canceled++;
        },
      }
    : {};
  const context = {
    window: {},
    document: { querySelector: () => video },
    performance: { now: () => now, timeOrigin: 123456 },
    setInterval(fn, ms) {
      assert.equal(ms, 100);
      timer = fn;
      return 1;
    },
    clearInterval(id) {
      assert.equal(id, 1);
      cleared++;
    },
  };
  const install = () =>
    vm.runInNewContext(`(${installFrameDiagnostics.toString()})()`, context);
  const supportedResult = install();
  return {
    supportedResult,
    install,
    drain: () => context.window.__rstreamFrameDiagnostics?.drain(),
    stop: () => context.window.__rstreamFrameDiagnostics?.stop(),
    frame(at, metadata = {}) {
      now = at;
      callback(at, metadata);
    },
    tick(at) {
      now = at;
      timer();
    },
    counts: () => ({ scheduled, canceled, cleared }),
  };
}

test("frame gaps span sampling boundaries and preserve optional receiver timestamps", () => {
  const f = fixture();
  assert.equal(f.supportedResult, true);
  f.frame(10, { mediaTime: 1, receiveTime: 5, processingDuration: 0.003 });
  let sample = f.drain();
  assert.equal(sample.frames, 1);
  assert.equal(sample.maximumFrameGapMilliseconds, 0);
  f.frame(210, { mediaTime: 1.2, receiveTime: 205, rtpTimestamp: 123 });
  sample = f.drain();
  assert.equal(sample.maximumFrameGapMilliseconds, 200);
  assert.equal(sample.events.length, 1);
  assert.equal(sample.events[0].previous.receiveMilliseconds, 5);
  assert.equal(sample.events[0].previous.processingSeconds, 0.003);
  assert.equal(sample.events[0].current.receiveMilliseconds, 205);
  assert.equal(sample.events[0].current.processingSeconds, null);
  assert.equal(sample.timeOriginMilliseconds, 123456);
  assert.equal(sample.sampledAtMilliseconds, 210);
  assert.equal(f.drain().events.length, 0);
});

test("diagnostic storage is bounded even when sampling stalls", () => {
  const f = fixture();
  for (let i = 1; i <= 200; i++) f.tick(i * 200);
  const sample = f.drain();
  assert.equal(sample.events.length, 128);
  assert.equal(sample.omittedEvents, 72);
  assert.equal(sample.maximumTimerDelayMilliseconds, 100);
  assert.equal(f.drain().omittedEvents, 0);
});

test("stop and reinstall cancel callbacks and timers without rescheduling late callbacks", () => {
  const f = fixture();
  f.frame(10);
  f.stop();
  f.stop();
  const counts = f.counts();
  assert.equal(counts.canceled, 1);
  assert.equal(counts.cleared, 1);
  f.frame(1000);
  f.tick(1000);
  assert.deepEqual(f.counts(), counts);
  assert.equal(f.drain().events.length, 0);
  f.install();
  assert.equal(f.counts().scheduled, counts.scheduled + 1);
  f.install();
  assert.equal(f.counts().canceled, 2);
  assert.equal(f.counts().cleared, 2);
});

test("unsupported video callbacks remain explicitly unavailable", () => {
  const f = fixture(false);
  assert.equal(f.supportedResult, false);
  assert.equal(f.drain(), undefined);
  assert.deepEqual(f.counts(), { scheduled: 0, canceled: 0, cleared: 0 });
});
