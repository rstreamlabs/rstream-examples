"use client";

import { getToolName, type DynamicToolUIPart, type ToolUIPart } from "ai";
import { Check, TerminalSquare, X } from "lucide-react";
import { MessageResponse } from "@/components/ai-elements/message";
import { Button } from "@/components/ui/button";

type ApprovalResponder = (response: {
  id: string;
  approved: boolean;
  reason?: string;
}) => void;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function ToolCallView({
  part,
  onApprove,
}: {
  part: ToolUIPart | DynamicToolUIPart;
  onApprove: ApprovalResponder;
}) {
  const name = getToolName(part).replace(/^rstream_/, "");
  const isExec = name.includes("exec");

  switch (part.state) {
    case "approval-requested":
      return (
        <div className="my-2 rounded-lg border border-input bg-card p-3">
          <p className="mb-2 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            <TerminalSquare className="size-3.5" />
            {isExec ? "Run on your machine?" : `Allow ${name}?`}
          </p>
          <Code text={formatInput(part.input)} />
          <div className="mt-3 flex gap-2">
            <Button
              size="sm"
              className="gap-1.5"
              onClick={() =>
                onApprove({ id: part.approval.id, approved: true })
              }
            >
              <Check className="size-3.5" />
              Approve
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="gap-1.5"
              onClick={() =>
                onApprove({ id: part.approval.id, approved: false })
              }
            >
              <X className="size-3.5" />
              Deny
            </Button>
          </div>
        </div>
      );

    case "input-streaming":
    case "input-available":
    case "approval-responded":
      return (
        <div className="my-2">
          <ToolHeader name={name} />
          <Code text={formatInput(part.input)} shell={isExec} />
          <p className="mt-2 text-xs text-muted-foreground">Running…</p>
        </div>
      );

    case "output-denied":
      return (
        <div className="my-2">
          <ToolHeader name={name} />
          <Code text={formatInput(part.input)} shell={isExec} />
          <p className="mt-2 text-xs text-muted-foreground">
            Declined — not run.
          </p>
        </div>
      );

    case "output-error":
      return (
        <div className="my-2">
          <ToolHeader name={name} />
          <Code text={formatInput(part.input)} shell={isExec} />
          <Output text={part.errorText} tone="error" />
        </div>
      );

    case "output-available":
      return (
        <div className="my-2">
          <ToolHeader name={name} />
          <Code text={formatInput(part.input)} shell={isExec} />
          <Output text={formatOutput(part.output)} />
        </div>
      );

    default:
      return null;
  }
}

function ToolHeader({ name }: { name: string }) {
  return (
    <p className="mb-2 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
      <TerminalSquare className="size-3.5" />
      {name}
    </p>
  );
}

function formatInput(input: unknown): string {
  if (!isRecord(input)) return typeof input === "string" ? input : "";
  const command = input.command;
  if (Array.isArray(command)) return command.join(" ");
  if (typeof command === "string") {
    const args = Array.isArray(input.args) ? input.args.join(" ") : "";
    return [command, args].filter(Boolean).join(" ");
  }
  if (Object.keys(input).length === 0) return "";
  return JSON.stringify(input) ?? "";
}

function formatOutput(output: unknown): string {
  if (!isRecord(output)) {
    return typeof output === "string" ? output : (JSON.stringify(output) ?? "");
  }
  const streams = [output.stdout, output.stderr]
    .filter(
      (value): value is string => typeof value === "string" && value !== "",
    )
    .join("\n");
  if (streams) return streams;
  return JSON.stringify(output, null, 2) ?? "";
}

function Code({ text, shell = true }: { text: string; shell?: boolean }) {
  if (!text) return null;
  return (
    <div className="flex min-w-0 items-center gap-2 font-mono text-xs">
      {shell ? <span className="shrink-0 text-muted-foreground">$</span> : null}
      <span className="min-w-0 flex-1 truncate text-foreground">{text}</span>
    </div>
  );
}

// Tool output is rendered through the same Markdown/code-block component as the
// assistant's replies (a fenced block), so tool results and code look and behave
// identically — one copy/download affordance, one responsive style. Errors stay
// a short inline line.
function Output({ text, tone }: { text: string; tone?: "error" }) {
  if (!text) return null;
  if (tone === "error") {
    return <p className="mt-2 font-mono text-xs text-destructive">{text}</p>;
  }
  return (
    <div className="mt-2">
      <MessageResponse>{`\`\`\`\n${text}\n\`\`\``}</MessageResponse>
    </div>
  );
}
