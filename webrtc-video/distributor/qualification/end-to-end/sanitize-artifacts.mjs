import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import {
  containsSensitiveText,
  redactSensitiveText,
} from "../../../producer/qualification/adaptive-streaming/lib/redaction.mjs";

const root = process.argv[2];
if (!root) {
  throw new Error("usage: node sanitize-artifacts.mjs DIRECTORY");
}

async function filesBelow(directory) {
  const entries = await fs.readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...(await filesBelow(target)));
    else if (entry.isFile()) files.push(target);
  }
  return files;
}

for (const file of await filesBelow(root)) {
  const content = await fs.readFile(file);
  if (content.includes(0)) continue;
  const source = content.toString("utf8");
  const redacted = redactSensitiveText(source);
  if (redacted !== source) {
    const temporary = `${file}.redacting-${process.pid}`;
    await fs.writeFile(temporary, redacted, { mode: 0o600 });
    await fs.rename(temporary, file);
  }
  if (containsSensitiveText(await fs.readFile(file, "utf8"))) {
    throw new Error(`credential remains in qualification artifact ${file}`);
  }
}
