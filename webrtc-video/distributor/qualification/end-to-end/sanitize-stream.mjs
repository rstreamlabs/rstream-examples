import process from "node:process";
import { redactSensitiveText } from "../../../producer/qualification/adaptive-streaming/lib/redaction.mjs";

process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) {
  process.stdout.write(redactSensitiveText(chunk));
}
