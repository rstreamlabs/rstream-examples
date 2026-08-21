import assert from "node:assert/strict";
import test from "node:test";

import {
  ICERestartTimers,
  iceRestartDisposition,
} from "../internal/web/embed/ice-recovery.ts";

class FakeTimers {
  next = 1;
  pending = new Map();

  cancel = (timer) => {
    this.pending.delete(timer);
  };

  schedule = (callback) => {
    const timer = this.next;
    this.next += 1;
    this.pending.set(timer, callback);
    return timer;
  };

  run() {
    const callbacks = [...this.pending.values()];
    this.pending.clear();
    for (const callback of callbacks) {
      callback();
    }
  }
}

test("connected ICE cancels stale outcome and retry timers", () => {
  const clock = new FakeTimers();
  const timers = new ICERestartTimers(clock.schedule, clock.cancel);
  let outcomes = 0;
  let retries = 0;
  timers.scheduleOutcome(() => {
    outcomes += 1;
  }, 15_000);
  assert.equal(
    timers.scheduleRetry(() => {
      retries += 1;
    }, 750),
    true,
  );
  timers.clear();
  clock.run();
  assert.equal(outcomes, 0);
  assert.equal(retries, 0);
  assert.equal(timers.retryScheduled, false);
});

test("a restart completed without an ICE state event is acknowledged", () => {
  const clock = new FakeTimers();
  const timers = new ICERestartTimers(clock.schedule, clock.cancel);
  let expired = false;
  timers.scheduleOutcome(() => {
    expired = true;
  }, 15_000);
  assert.equal(iceRestartDisposition("connected", "connected"), "connected");
  timers.clearOutcome();
  clock.run();
  assert.equal(expired, false);
});

test("checking remains pending while failed and disconnected states retry", () => {
  assert.equal(iceRestartDisposition("connecting", "checking"), "pending");
  assert.equal(iceRestartDisposition("failed", "connected"), "retry");
  assert.equal(iceRestartDisposition("connected", "disconnected"), "retry");
});
