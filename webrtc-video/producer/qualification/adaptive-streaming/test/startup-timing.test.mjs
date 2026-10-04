import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";
import { installStartupTiming } from "../lib/startup-timing.mjs";

function fixture(supported = true) {
  let now = 0;
  let listener;
  let callback;
  let canceled = 0;
  const connect = {
    addEventListener(event, fn, capture) {
      assert.equal(event, "click");
      assert.equal(capture, true);
      listener = fn;
    },
    removeEventListener(event, fn, capture) {
      assert.equal(event, "click");
      assert.equal(capture, true);
      if (listener === fn) listener = null;
    },
  };
  const video = supported
    ? {
        requestVideoFrameCallback(fn) {
          callback = fn;
          return 1;
        },
        cancelVideoFrameCallback(id) {
          assert.equal(id, 1);
          canceled++;
        },
      }
    : {};
  const context = {
    window: { __rstreamQualificationTelemetry: { events: [] } },
    document: {
      querySelector: (selector) => (selector === "#video" ? video : connect),
    },
    performance: { now: () => now },
  };
  const install = () =>
    vm.runInNewContext(`(${installStartupTiming.toString()})()`, context);
  const available = install();
  return {
    available,
    install,
    snapshot: () => context.window.__rstreamStartupTiming.snapshot(),
    stop: () => context.window.__rstreamStartupTiming.stop(),
    click(at) {
      now = at;
      listener?.();
    },
    frame(at, overrides = {}) {
      now = at;
      callback?.(at, {
        expectedDisplayTime: at + 5,
        width: 1280,
        height: 720,
        presentedFrames: 1,
        ...overrides,
      });
    },
    events: () => context.window.__rstreamQualificationTelemetry.events,
    canceled: () => canceled,
  };
}

test("measures from the actual click and preserves the first compositor callback", () => {
  const f = fixture();
  assert.equal(f.available, true);
  assert.equal(f.snapshot().measurementValid, false);
  f.click(200);
  assert.equal(f.snapshot().requestToCallbackMilliseconds, null);
  f.frame(350);
  f.click(500);
  f.frame(600);
  const report = f.snapshot();
  assert.equal(report.measurementValid, true);
  assert.equal(report.requestToCallbackMilliseconds, 150);
  assert.equal(report.requestToExpectedDisplayMilliseconds, 155);
  assert.equal(report.firstFrame.callbackMilliseconds, 350);
  assert.equal(f.events().length, 2);
});

test("cancel and reinstall remove listeners and reject late frame callbacks", () => {
  const f = fixture();
  f.click(200);
  f.stop();
  f.stop();
  f.frame(350);
  f.click(400);
  assert.equal(f.canceled(), 1);
  assert.equal(f.snapshot().measurementValid, false);
  assert.equal(f.events().length, 1);
  f.install();
  f.click(600);
  f.frame(650);
  assert.equal(f.snapshot().requestToCallbackMilliseconds, 50);
});

test("missing, invalid or pre-request presentation evidence never becomes zero latency", () => {
  for (const metadata of [
    { expectedDisplayTime: undefined },
    { expectedDisplayTime: 100 },
    { width: 0 },
    { presentedFrames: 0 },
  ]) {
    const f = fixture();
    f.click(200);
    f.frame(350, metadata);
    assert.equal(f.snapshot().measurementValid, false);
    assert.equal(f.snapshot().requestToExpectedDisplayMilliseconds, null);
  }
  const f = fixture(false);
  assert.equal(f.available, false);
  f.click(200);
  f.stop();
  assert.equal(f.snapshot().requestedAtMilliseconds, null);
  assert.equal(f.snapshot().measurementValid, false);
});
