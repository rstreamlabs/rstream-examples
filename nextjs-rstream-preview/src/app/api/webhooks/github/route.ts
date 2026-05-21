import {
  appendGitHubWebhookEvent,
  readRecentGitHubWebhookEvents,
  verifyGitHubSignature,
} from "@/lib/webhooks";
import { randomUUID } from "node:crypto";

export const runtime = "nodejs";

interface GitHubPayload {
  action?: string;
  repository?: { full_name?: string };
  sender?: { login?: string };
}

export async function GET() {
  return Response.json({
    events: await readRecentGitHubWebhookEvents(20),
  });
}

export async function POST(request: Request) {
  const secret = process.env.GITHUB_WEBHOOK_SECRET;
  if (!secret) {
    return Response.json(
      { error: "GITHUB_WEBHOOK_SECRET is required" },
      { status: 500 },
    );
  }
  const body = await request.text();
  if (
    !verifyGitHubSignature(
      body,
      secret,
      request.headers.get("x-hub-signature-256"),
    )
  ) {
    return Response.json({ error: "invalid signature" }, { status: 401 });
  }
  let payload: GitHubPayload;
  try {
    payload = parseGitHubPayload(JSON.parse(body));
  } catch {
    return Response.json({ error: "invalid JSON payload" }, { status: 400 });
  }
  const event = {
    action: payload.action,
    delivery: request.headers.get("x-github-delivery") ?? randomUUID(),
    event: request.headers.get("x-github-event") ?? "unknown",
    receivedAt: new Date().toISOString(),
    repository: payload.repository?.full_name,
    sender: payload.sender?.login,
  };
  await appendGitHubWebhookEvent(event);
  return Response.json({ ok: true, event });
}

function parseGitHubPayload(value: unknown): GitHubPayload {
  if (!isObject(value)) {
    return {};
  }
  return {
    action: optionalString(value.action),
    repository: parseRepository(value.repository),
    sender: parseSender(value.sender),
  };
}

function parseRepository(value: unknown): GitHubPayload["repository"] {
  if (!isObject(value)) {
    return undefined;
  }
  return { full_name: optionalString(value.full_name) };
}

function parseSender(value: unknown): GitHubPayload["sender"] {
  if (!isObject(value)) {
    return undefined;
  }
  return { login: optionalString(value.login) };
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
