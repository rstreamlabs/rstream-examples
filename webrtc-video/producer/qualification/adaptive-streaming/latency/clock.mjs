import process from "node:process";

const sampleCount = 7;
// Conservative allowance for the browser's reduced timer precision. This is
// included in the reported error bound, never subtracted from measured latency.
const browserPrecisionMilliseconds = 1;

export const monotonicMilliseconds = () =>
  Number(process.hrtime.bigint() / 1000n) / 1000;

export function calibrationBounds(observations) {
  if (!Array.isArray(observations) || observations.length !== sampleCount)
    return null;
  let lower = -Infinity,
    upper = Infinity,
    previous;
  for (const observation of observations) {
    if (
      !observation ||
      ![
        observation.hostBeforeMilliseconds,
        observation.hostAfterMilliseconds,
        observation.browserNowMilliseconds,
        observation.browserTimeOriginMilliseconds,
      ].every((value) => Number.isFinite(value) && value >= 0) ||
      observation.hostAfterMilliseconds < observation.hostBeforeMilliseconds ||
      (previous &&
        (observation.hostBeforeMilliseconds < previous.hostAfterMilliseconds ||
          observation.browserNowMilliseconds <
            previous.browserNowMilliseconds ||
          observation.browserTimeOriginMilliseconds !==
            previous.browserTimeOriginMilliseconds))
    )
      return null;
    // The browser read happened between these host reads. No assumption about
    // symmetric IPC delay is needed: intersect all seven admissible intervals.
    lower = Math.max(
      lower,
      observation.hostBeforeMilliseconds -
        observation.browserNowMilliseconds -
        browserPrecisionMilliseconds,
    );
    upper = Math.min(
      upper,
      observation.hostAfterMilliseconds -
        observation.browserNowMilliseconds +
        browserPrecisionMilliseconds,
    );
    previous = observation;
  }
  if (lower > upper) return null;
  return {
    lowerOffsetMilliseconds: lower,
    upperOffsetMilliseconds: upper,
    offsetMilliseconds: (lower + upper) / 2,
    browserTimeOriginMilliseconds: previous.browserTimeOriginMilliseconds,
  };
}

export function summarizeClockAlignment(initial, final, maximumError = 5) {
  const start = calibrationBounds(initial),
    end = calibrationBounds(final);
  if (!start || !end) return { valid: false, maximumErrorMilliseconds: null };
  const error = Math.max(
    ...[start, end].flatMap((bound) => [
      Math.abs(bound.lowerOffsetMilliseconds - start.offsetMilliseconds),
      Math.abs(bound.upperOffsetMilliseconds - start.offsetMilliseconds),
    ]),
  );
  return {
    valid:
      Number.isFinite(maximumError) &&
      maximumError > 0 &&
      start.browserTimeOriginMilliseconds ===
        end.browserTimeOriginMilliseconds &&
      initial.at(-1).hostAfterMilliseconds <= final[0].hostBeforeMilliseconds &&
      Math.max(start.lowerOffsetMilliseconds, end.lowerOffsetMilliseconds) <=
        Math.min(start.upperOffsetMilliseconds, end.upperOffsetMilliseconds) &&
      error <= maximumError,
    maximumErrorMilliseconds: error,
    offsetMilliseconds: start.offsetMilliseconds,
    browserPrecisionMilliseconds,
    initial: start,
    final: end,
  };
}

export async function calibrateBrowserClock(
  readBrowserClock,
  now = monotonicMilliseconds,
  { timeoutMilliseconds = 2000 } = {},
) {
  if (
    !Number.isFinite(timeoutMilliseconds) ||
    timeoutMilliseconds <= 0 ||
    timeoutMilliseconds > 10000
  )
    throw new TypeError("Invalid browser clock calibration deadline");
  let active = true,
    timeout;
  const observations = [];
  const collect = async () => {
    for (let index = 0; index < sampleCount && active; index++) {
      const before = now();
      const clock = await readBrowserClock();
      const after = now();
      if (!active) throw new Error("Browser clock calibration expired");
      observations.push({
        hostBeforeMilliseconds: before,
        hostAfterMilliseconds: after,
        browserNowMilliseconds: clock.nowMilliseconds,
        browserTimeOriginMilliseconds: clock.timeOriginMilliseconds,
      });
    }
    const bounds = calibrationBounds(observations);
    if (
      !bounds ||
      bounds.upperOffsetMilliseconds - bounds.lowerOffsetMilliseconds > 5
    )
      throw new Error("Browser clock calibration is inconsistent or imprecise");
    return observations;
  };
  try {
    return await Promise.race([
      collect(),
      new Promise((_, reject) => {
        timeout = setTimeout(
          () => reject(new Error("Browser clock calibration timed out")),
          timeoutMilliseconds,
        );
      }),
    ]);
  } finally {
    active = false;
    clearTimeout(timeout);
  }
}
