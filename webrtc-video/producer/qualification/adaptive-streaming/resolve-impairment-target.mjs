import { readFile } from "node:fs/promises";
import { lookup } from "node:dns/promises";
import process from "node:process";
import { selectImpairmentTarget } from "./lib/impairment-target.mjs";

const [readyPath, pathKind] = process.argv.slice(2);
if (!readyPath || !pathKind) {
  throw new Error("usage: resolve-impairment-target.mjs READY_FILE PATH_KIND");
}
const ready = JSON.parse(await readFile(readyPath, "utf8"));
const target = selectImpairmentTarget(ready, pathKind);
if (target.family === 0) {
  const addresses = await lookup(target.address, { all: true, verbatim: true });
  const selected =
    addresses.find((entry) => entry.family === 4) || addresses[0];
  if (!selected) {
    throw new Error(
      `TURN target ${target.address} did not resolve to an address`,
    );
  }
  target.resolvedFrom = target.address;
  target.address = selected.address;
  target.family = selected.family;
}
process.stdout.write(`${JSON.stringify(target)}\n`);
