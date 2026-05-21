import { appendFile, mkdir, readFile } from "node:fs/promises";
import crypto from "node:crypto";
import path from "node:path";

export interface GitHubWebhookEvent {
  action?: string;
  delivery: string;
  event: string;
  receivedAt: string;
  repository?: string;
  sender?: string;
}

const dataDirectory = () =>
  process.env.WEBHOOK_DATA_DIR ?? path.join(process.cwd(), ".data");

const eventsFile = () => path.join(dataDirectory(), "github-events.jsonl");

export function verifyGitHubSignature(
  body: string,
  secret: string,
  signature: string | null,
): boolean {
  if (!signature?.startsWith("sha256=")) {
    return false;
  }
  const expected = crypto
    .createHmac("sha256", secret)
    .update(body)
    .digest("hex");
  const actual = signature.slice("sha256=".length);
  const expectedBuffer = Buffer.from(expected, "hex");
  const actualBuffer = Buffer.from(actual, "hex");
  if (expectedBuffer.length !== actualBuffer.length) {
    return false;
  }
  return crypto.timingSafeEqual(expectedBuffer, actualBuffer);
}

export async function appendGitHubWebhookEvent(
  event: GitHubWebhookEvent,
): Promise<void> {
  await mkdir(dataDirectory(), { recursive: true });
  await appendFile(eventsFile(), `${JSON.stringify(event)}\n`, "utf8");
}

export async function readRecentGitHubWebhookEvents(
  limit = 10,
): Promise<GitHubWebhookEvent[]> {
  let raw: string;
  try {
    raw = await readFile(eventsFile(), "utf8");
  } catch {
    return [];
  }
  return raw
    .split("\n")
    .filter(Boolean)
    .map(parseGitHubWebhookEvent)
    .reverse()
    .slice(0, limit);
}

function parseGitHubWebhookEvent(line: string): GitHubWebhookEvent {
  const value = JSON.parse(line);
  if (!isObject(value)) {
    throw new Error("stored webhook event must be an object");
  }
  return {
    action: optionalString(value.action),
    delivery: requiredString(value.delivery, "delivery"),
    event: requiredString(value.event, "event"),
    receivedAt: requiredString(value.receivedAt, "receivedAt"),
    repository: optionalString(value.repository),
    sender: optionalString(value.sender),
  };
}

function requiredString(value: unknown, name: string): string {
  if (typeof value !== "string" || value === "") {
    throw new Error(`stored webhook event ${name} must be a string`);
  }
  return value;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
