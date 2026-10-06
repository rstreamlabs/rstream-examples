import assert from "node:assert/strict";
import test from "node:test";
import {
  calibrateBrowserClock,
  calibrationBounds,
  summarizeClockAlignment,
} from "../latency/clock.mjs";

function observations(start = 10000, offset = 5000, duration = 1) {
  return Array.from({ length: 7 }, (_, index) => {
    const before = start + index * 40;
    return {
      hostBeforeMilliseconds: before,
      hostAfterMilliseconds: before + duration,
      browserNowMilliseconds: before + (index % 2 ? 0.1 : 0.9) - offset,
      browserTimeOriginMilliseconds: 1790000000000,
    };
  });
}

test("asymmetric IPC delays bound the true offset without assuming equal transit times", () => {
  const initial = observations(),
    final = observations(20000);
  const bounds = calibrationBounds(initial);
  assert(bounds.lowerOffsetMilliseconds <= 5000);
  assert(bounds.upperOffsetMilliseconds >= 5000);
  assert(bounds.upperOffsetMilliseconds - bounds.lowerOffsetMilliseconds < 2.3);
  const alignment = summarizeClockAlignment(initial, final);
  assert.equal(alignment.valid, true);
  assert(alignment.maximumErrorMilliseconds < 1.2);
});

test("missing, reversed, inconsistent and navigated clocks cannot qualify", () => {
  assert.equal(calibrationBounds([]), null);
  assert.equal(summarizeClockAlignment(observations(), null).valid, false);
  for (const modify of [
    (values) => {
      values[2].hostAfterMilliseconds = -1;
    },
    (values) => {
      values[2].hostBeforeMilliseconds = NaN;
    },
    (values) => {
      values[2].browserNowMilliseconds -= 50;
    },
    (values) => {
      values[2].browserTimeOriginMilliseconds++;
    },
    (values) => {
      values[2].browserNowMilliseconds += 10;
    },
  ]) {
    const values = observations();
    modify(values);
    assert.equal(calibrationBounds(values), null);
  }
  const changedPage = observations(20000).map((sample) => ({
    ...sample,
    browserTimeOriginMilliseconds: 1790000000100,
  }));
  assert.equal(
    summarizeClockAlignment(observations(), changedPage).valid,
    false,
  );
  assert.equal(
    summarizeClockAlignment(observations(), observations()).valid,
    false,
  );
  assert.equal(
    summarizeClockAlignment(observations(), observations(20000, 5010)).valid,
    false,
  );
});

test("wide clock uncertainty fails instead of becoming an apparently precise latency", () => {
  const report = summarizeClockAlignment(
    observations(10000, 5000, 20),
    observations(20000, 5000, 20),
  );
  assert.equal(report.valid, false);
  assert(report.maximumErrorMilliseconds > 5);
});

test("bounded calibration retains every host/browser observation", async () => {
  const values = observations();
  let index = 0,
    after = false;
  const now = () => {
    if (!after) {
      after = true;
      return values[index].hostBeforeMilliseconds;
    }
    after = false;
    return values[index++].hostAfterMilliseconds;
  };
  const clock = () => ({
    nowMilliseconds: values[index].browserNowMilliseconds,
    timeOriginMilliseconds: values[index].browserTimeOriginMilliseconds,
  });
  assert.deepEqual(await calibrateBrowserClock(clock, now), values);
});

test("a timed-out browser read cannot start another calibration request", async () => {
  let resolve,
    reads = 0;
  const pending = new Promise((done) => {
    resolve = done;
  });
  const calibration = calibrateBrowserClock(
    () => {
      reads++;
      return pending;
    },
    () => 100,
    { timeoutMilliseconds: 10 },
  );
  await assert.rejects(calibration, /timed out/);
  resolve({ nowMilliseconds: 1, timeOriginMilliseconds: 1000 });
  await new Promise((done) => setImmediate(done));
  assert.equal(reads, 1);
});

test("invalid clock responses and read failures remain calibration failures", async () => {
  await assert.rejects(
    calibrateBrowserClock(() => {
      throw new Error("closed");
    }),
    /closed/,
  );
  await assert.rejects(
    calibrateBrowserClock(() => ({
      nowMilliseconds: NaN,
      timeOriginMilliseconds: 1,
    })),
    /inconsistent/,
  );
  await assert.rejects(
    calibrateBrowserClock(() => ({}), undefined, {
      timeoutMilliseconds: Infinity,
    }),
    /deadline/,
  );
});
