import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";
import { installStartupTiming } from "../lib/startup-timing.mjs";

function fixture(supported = true) {
  let now = 0;
  let listener;
  let callback;
  let canceled = 0;
  const state = {
    documentVisible: true,
    connected: true,
    unobstructed: true,
    readyState: 4,
    paused: false,
    ended: false,
    width: 1280,
    height: 720,
    rect: { left: 8, top: 8, width: 640, height: 360 },
    videoStyle: { display: "block", visibility: "visible", opacity: "1" },
    parentStyle: { display: "block", visibility: "visible", opacity: "1" },
  };
  const parent = { parentElement: null };
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
        get isConnected() {
          return state.connected;
        },
        get readyState() {
          return state.readyState;
        },
        get paused() {
          return state.paused;
        },
        get ended() {
          return state.ended;
        },
        get videoWidth() {
          return state.width;
        },
        get videoHeight() {
          return state.height;
        },
        parentElement: parent,
        getBoundingClientRect: () => state.rect,
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
    window: {
      __rstreamQualificationTelemetry: { events: [] },
      innerWidth: 1280,
      innerHeight: 720,
      getComputedStyle: (element) =>
        element === video ? state.videoStyle : state.parentStyle,
    },
    document: {
      querySelector: (selector) => (selector === "#video" ? video : connect),
      get visibilityState() {
        return state.documentVisible ? "visible" : "hidden";
      },
      elementFromPoint: () => (state.unobstructed ? video : {}),
    },
    performance: { now: () => now },
  };
  const install = () =>
    vm.runInNewContext(`(${installStartupTiming.toString()})()`, context);
  const available = install();
  return {
    available,
    state,
    install,
    snapshot: () => context.window.__rstreamStartupTiming.snapshot(),
    stop: () => context.window.__rstreamStartupTiming.stop(),
    click(at) {
      now = at;
      listener?.();
    },
    frame(at, overrides = {}, observedAt = at) {
      now = observedAt;
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
  assert.equal(report.visiblePresentationValid, true);
  assert.equal(report.requestToVisiblePresentationMilliseconds, 155);
  assert.equal(f.events().length, 3);
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
    { presentedFrames: 2 },
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

test("missed compositor callbacks retain unknown exact timing but yield a conservative visible observation", () => {
  const f = fixture();
  f.click(200);
  f.frame(350, { presentedFrames: 2, expectedDisplayTime: 355 }, 410);
  const report = f.snapshot();
  assert.equal(report.measurementValid, false);
  assert.equal(report.requestToExpectedDisplayMilliseconds, null);
  assert.equal(report.visiblePresentationValid, true);
  assert.equal(report.requestToVisiblePresentationMilliseconds, 210);
  assert.equal(report.firstVisibleFrame.presentedFrames, 2);
  assert.equal(report.firstFrame.expectedDisplayMilliseconds, 355);
});

for (const [name, hide] of [
  [
    "document hidden",
    (s) => {
      s.documentVisible = false;
    },
  ],
  [
    "video detached",
    (s) => {
      s.connected = false;
    },
  ],
  [
    "overlay covers center",
    (s) => {
      s.unobstructed = false;
    },
  ],
  [
    "video not ready",
    (s) => {
      s.readyState = 1;
    },
  ],
  [
    "video paused",
    (s) => {
      s.paused = true;
    },
  ],
  [
    "video ended",
    (s) => {
      s.ended = true;
    },
  ],
  [
    "empty video dimensions",
    (s) => {
      s.width = 0;
    },
  ],
  [
    "zero layout size",
    (s) => {
      s.rect.height = 0;
    },
  ],
  [
    "outside viewport",
    (s) => {
      s.rect.top = 800;
    },
  ],
  [
    "invalid layout",
    (s) => {
      s.rect.left = Number.NaN;
    },
  ],
  [
    "video hidden by style",
    (s) => {
      s.videoStyle.visibility = "hidden";
    },
  ],
  [
    "ancestor not displayed",
    (s) => {
      s.parentStyle.display = "none";
    },
  ],
  [
    "transparent ancestor",
    (s) => {
      s.parentStyle.opacity = "0";
    },
  ],
]) {
  test(`${name} cannot establish visible startup`, () => {
    const f = fixture();
    hide(f.state);
    f.click(200);
    f.frame(350);
    assert.equal(f.snapshot().measurementValid, true);
    assert.equal(f.snapshot().visiblePresentationValid, false);
    assert.equal(f.snapshot().requestToVisiblePresentationMilliseconds, null);
    f.stop();
    assert.equal(f.canceled(), 1);
  });
}

test("waits for an unobstructed frame without backdating a hidden first image", () => {
  const f = fixture();
  f.state.unobstructed = false;
  f.click(200);
  f.frame(350);
  f.state.unobstructed = true;
  f.frame(550, { presentedFrames: 7 });
  const report = f.snapshot();
  assert.equal(report.requestToExpectedDisplayMilliseconds, 155);
  assert.equal(report.firstFrame.visibility.visible, false);
  assert.equal(report.firstVisibleFrame.presentedFrames, 7);
  assert.equal(report.requestToVisiblePresentationMilliseconds, 355);
  assert.equal(report.visiblePresentationValid, true);
  f.frame(850, { presentedFrames: 16 });
  assert.equal(f.snapshot().requestToVisiblePresentationMilliseconds, 355);
  assert.equal(f.events().length, 3);
});

test("invalid metadata cannot become a visible zero-duration observation", () => {
  for (const metadata of [
    { expectedDisplayTime: undefined },
    { expectedDisplayTime: 100 },
    { width: 0 },
    { height: Number.NaN },
    { presentedFrames: 0 },
    { presentedFrames: 1.5 },
  ]) {
    const f = fixture();
    f.click(200);
    f.frame(350, metadata);
    assert.equal(f.snapshot().visiblePresentationValid, false);
    assert.equal(f.snapshot().requestToVisiblePresentationMilliseconds, null);
    f.stop();
  }
});

test("stopping after a hidden frame prevents late visible evidence and further callbacks", () => {
  const f = fixture();
  f.state.unobstructed = false;
  f.click(200);
  f.frame(350);
  f.stop();
  f.state.unobstructed = true;
  f.frame(550, { presentedFrames: 2 });
  assert.equal(f.snapshot().visiblePresentationValid, false);
  assert.equal(f.snapshot().firstVisibleFrame, null);
  assert.equal(f.canceled(), 1);
  assert.equal(f.events().length, 2);
});
