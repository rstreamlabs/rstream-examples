import { createOpenAICompatible } from "@ai-sdk/openai-compatible";
import {
  convertToModelMessages,
  extractReasoningMiddleware,
  stepCountIs,
  streamText,
  wrapLanguageModel,
} from "ai";
import { NextResponse } from "next/server";
import { z } from "zod";

import { getServerUser } from "@/lib/auth";
import { mcpInstructions, mcpTools } from "@/lib/mcp-tools";
import type { MeshUIMessage } from "@/lib/mesh-types";
import { mintTurn } from "@/lib/turn";
import { webTools } from "@/lib/web-tools";

export const runtime = "nodejs";
export const maxDuration = 60;

const meshMessageShape = z
  .object({
    id: z.string(),
    role: z.enum(["system", "user", "assistant"]),
    metadata: z.object({ worker: z.string() }).optional(),
    parts: z.array(z.object({ type: z.string() }).passthrough()),
  })
  .passthrough();

const meshMessageSchema = z.custom<MeshUIMessage>(
  (value) => meshMessageShape.safeParse(value).success,
);

const bodySchema = z.object({
  model: z.string().min(1),
  workerId: z.string().optional(),
  messages: z.array(meshMessageSchema),
});

function toolBudget(ctx: number): number {
  return Math.floor(ctx * 0.33 * 3.5);
}

function systemPrompt(today: string): string {
  return [
    "You are the assistant for the user's private LLM mesh: open models running on",
    "their own machines, reached over rstream — the reverse-tunnel product",
    "(https://rstream.io) that gives each machine a private, outbound-only connection,",
    "reachable with no public IP or open port. Beyond answering, you can inspect and",
    "act on the user's own machines through rstream's hosted MCP when the request",
    'calls for it. Always write the product name as "rstream" — one word, all',
    'lowercase, never "rStream" or "RStream". Answer from your own knowledge whenever',
    "you can: you do not",
    "need a tool to write code, explain a command, or reason about a topic. Reach for",
    "web search or fetch only for recent events or facts you cannot answer reliably,",
    "and cite the source URLs when you do. Use the machine tools to inspect or act on",
    "the user's machines when the request calls for it, following the tool descriptions",
    "and the MCP server's instructions. Read-only tools run without asking; for",
    "state-changing tools the app shows an approval dialog, so just call them rather",
    "than asking in your reply. When a tool returns an error, report that error plainly",
    "and do not invent or guess its output — never fabricate command results. Today is",
    `${today} (UTC). Format answers in`,
    "Markdown and be concise.",
  ].join(" ");
}

// Turn a streaming failure into a message for the reader. The OpenAI-compatible
// provider surfaces upstream errors here — most often a worker that does not have
// the requested model. The raw detail is logged for the operator so the message
// stays graceful and never asks the reader to inspect a machine.
function workerError(error: unknown, worker: string, model: string): string {
  const detail = error instanceof Error ? error.message : String(error);
  console.error(`chat turn failed on ${worker} for ${model}: ${detail}`);
  if (/404|not found|no such model|model.*not/i.test(detail)) {
    return `The model ${model} is not available on ${worker}. Select another model to continue.`;
  }
  if (/429|rate|quota|capacity|overloaded/i.test(detail)) {
    return `${worker} is at capacity at the moment. Please try again shortly.`;
  }
  if (/timeout|deadline|etimedout|aborted/i.test(detail)) {
    return `${worker} took too long to respond. Please try again.`;
  }
  return `${worker} was unable to complete this response. Please try again.`;
}

/** Server-side chat turn. */
export async function POST(request: Request): Promise<Response> {
  const user = await getServerUser();
  if (!user) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  const parsed = bodySchema.safeParse(await request.json().catch(() => null));
  if (!parsed.success) {
    return NextResponse.json({ error: "model is required" }, { status: 400 });
  }
  const { model, workerId, messages } = parsed.data;
  const turn = await mintTurn(model, { workerId });
  if (!turn) {
    return NextResponse.json(
      {
        error: workerId
          ? `the pinned worker cannot serve ${model} right now`
          : `no worker serves ${model}`,
      },
      { status: 503 },
    );
  }
  const worker = createOpenAICompatible({
    name: "private-llm-mesh",
    baseURL: `${turn.url}/v1`,
    apiKey: turn.token,
  });
  const result = streamText({
    model: wrapLanguageModel({
      model: worker(turn.model),
      middleware: extractReasoningMiddleware({ tagName: "think" }),
    }),
    // The app's own prompt, then the MCP server's propagated instructions, so
    // tool-usage guidance lives with the server (any MCP client benefits), not
    // hardcoded here.
    system: [
      systemPrompt(new Date().toISOString().slice(0, 10)),
      await mcpInstructions(),
    ]
      .filter(Boolean)
      .join("\n\n"),
    messages: await convertToModelMessages(messages),
    tools: { ...webTools(toolBudget(turn.ctx)), ...(await mcpTools()) },
    temperature: 0.7,
    stopWhen: stepCountIs(5),
    abortSignal: request.signal,
  });
  return result.toUIMessageStreamResponse<MeshUIMessage>({
    originalMessages: messages,
    messageMetadata: ({ part }) =>
      part.type === "start" ? { worker: turn.worker } : undefined,
    onError: (error) => workerError(error, turn.worker, model),
  });
}
