import { pathToFileURL } from "node:url";

// Allow two minutes for media setup and one for phase publication/teardown.
// The 12-minute ceiling leaves room for producer startup and cleanup inside
// the runner's 15-minute project-token lifetime.
export function collectorDeadlineSeconds(phaseDurations) {
  if (
    phaseDurations.length === 0 ||
    phaseDurations.some((value) => !Number.isSafeInteger(value) || value <= 0)
  ) {
    throw new Error("scenario durations must be positive integer seconds");
  }
  const deadline = phaseDurations.reduce((sum, value) => sum + value, 180);
  if (!Number.isSafeInteger(deadline) || deadline > 720) {
    throw new Error("scenario phases must total at most 540 seconds");
  }
  return deadline;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  try {
    console.log(collectorDeadlineSeconds(process.argv.slice(2).map(Number)));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
