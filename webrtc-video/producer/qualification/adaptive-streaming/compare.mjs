import process from "node:process";
import { compare, loadMatrix, writeComparison } from "./lib/comparison.mjs";

const directory = process.argv[2];
const minimumRepetitions = Number.parseInt(process.argv[3] || "1", 10);
if (
  !directory ||
  !Number.isSafeInteger(minimumRepetitions) ||
  minimumRepetitions <= 0
) {
  throw new Error(
    "usage: node compare.mjs <matrix-directory> [minimum-repetitions]",
  );
}
const runs = await loadMatrix(directory);
const result = compare(runs, minimumRepetitions);
const revision = runs.find((run) => !run.error)?.manifest.git?.revision || "";
await writeComparison(directory, result, {
  generatedAt: new Date().toISOString(),
  revision,
});
process.stdout.write(
  `${result.passed ? "PASS" : "FAIL"}: ${directory}/comparison.md\n`,
);
if (!result.passed) {
  process.exitCode = 1;
}
