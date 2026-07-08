"use client";

import { useChat } from "@ai-sdk/react";
import {
  DefaultChatTransport,
  isToolUIPart,
  lastAssistantMessageIsCompleteWithApprovalResponses,
} from "ai";
import {
  ArrowUp,
  ChevronDown,
  Cpu,
  Paperclip,
  RefreshCw,
  Square,
  SquarePen,
  TriangleAlert,
  X,
} from "lucide-react";
import { useMemo, useRef, useState } from "react";

import {
  Conversation,
  ConversationContent,
  ConversationScrollButton,
} from "@/components/ai-elements/conversation";
import {
  Message,
  MessageContent,
  MessageResponse,
} from "@/components/ai-elements/message";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { CopyButton } from "@/components/copy-button";
import { ToolCallView } from "@/components/tool-message";
import { WorkerPool } from "@/components/worker-pool";
import type { MeshUIMessage } from "@/lib/mesh-types";
import { usePool } from "@/lib/use-pool";

const SUGGESTIONS = [
  "What are rstream tunnels?",
  "What's new in open-source LLMs?",
  "Write a bash one-liner to check disk usage",
];

const LOG_LIMIT = 8000;

const LINK_SAFETY: { enabled: false } = { enabled: false };

function messageText(message: MeshUIMessage): string {
  return message.parts
    .map((part) => (part.type === "text" ? part.text : ""))
    .join("");
}

function hasVisibleContent(message: MeshUIMessage): boolean {
  return message.parts.some(
    (part) =>
      (part.type === "text" && part.text.trim() !== "") ||
      isToolUIPart(part) ||
      (part.type === "reasoning" && part.text.trim() !== ""),
  );
}

function ThinkingDots() {
  return (
    <span className="flex gap-1 py-1" aria-label="Thinking">
      <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/50 [animation-delay:-0.3s]" />
      <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/50 [animation-delay:-0.15s]" />
      <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/50" />
    </span>
  );
}

export function Chat({ projectEndpoint }: { projectEndpoint: string }) {
  const { workers, models, loading } = usePool();
  const [input, setInput] = useState("");
  const [model, setModel] = useState("");
  const [pinnedWorkerId, setPinnedWorkerId] = useState<string | null>(null);
  // Derive the pinned worker from the live pool: if it leaves, the pin goes
  // inert (routing falls back to auto) and re-applies if the worker returns.
  const pinnedWorker = pinnedWorkerId
    ? (workers.find((w) => w.id === pinnedWorkerId) ?? null)
    : null;
  // Pinning a worker narrows the picker to the models that worker serves.
  const availableModels = pinnedWorker ? pinnedWorker.models : models;
  const selectedModel =
    model && availableModels.includes(model)
      ? model
      : (availableModels[0] ?? "");
  function togglePin(id: string) {
    setPinnedWorkerId((prev) => (prev === id ? null : id));
  }
  const transport = useMemo(
    () => new DefaultChatTransport<MeshUIMessage>({ api: "/api/chat" }),
    [],
  );

  const {
    messages,
    sendMessage,
    setMessages,
    status,
    stop,
    regenerate,
    error,
    clearError,
    addToolApprovalResponse,
  } = useChat<MeshUIMessage>({
    transport,
    sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
  });
  const busy = status === "submitted" || status === "streaming";
  const canSend = input.trim().length > 0 && selectedModel !== "" && !busy;
  const body = pinnedWorker
    ? { model: selectedModel, workerId: pinnedWorker.id }
    : { model: selectedModel };
  function send(text: string) {
    clearError();
    sendMessage({ text }, { body });
  }

  function submit() {
    if (!canSend) return;
    send(input);
    setInput("");
  }
  function regenerateMessage(messageId: string) {
    void regenerate({ messageId, body }).catch(() => {});
  }
  function retry() {
    clearError();
    const last = [...messages].reverse().find((m) => m.role === "assistant");
    if (last) regenerateMessage(last.id);
  }

  function approve(response: { id: string; approved: boolean }) {
    void addToolApprovalResponse({ ...response, options: { body } });
  }
  const fileRef = useRef<HTMLInputElement>(null);
  async function attachFile(file: File) {
    const text = await file.text();
    const snippet =
      text.length > LOG_LIMIT
        ? text.slice(0, LOG_LIMIT) + "\n…(truncated)"
        : text;
    setInput(
      (prev) =>
        `${prev ? prev.trimEnd() + "\n\n" : ""}Analyze this log (${file.name}):\n\n\`\`\`\n${snippet}\n\`\`\`\n`,
    );
  }
  return (
    <div className="flex min-h-[26rem] flex-1 py-2 sm:py-4">
      <aside className="mr-8 hidden w-72 shrink-0 overflow-y-auto border-r border-border pr-8 lg:block">
        {messages.length > 0 ? (
          <Button
            variant="outline"
            size="sm"
            className="mb-4 w-full gap-1.5"
            onClick={() => setMessages([])}
          >
            <SquarePen className="size-3.5" />
            New chat
          </Button>
        ) : null}
        <WorkerPool
          workers={workers}
          pinnedId={pinnedWorkerId}
          onPin={togglePin}
          loading={loading}
          projectEndpoint={projectEndpoint}
        />
      </aside>

      <main className="relative flex min-h-0 w-full min-w-0 flex-1 flex-col">
        <details className="group mb-3 shrink-0 rounded-lg border border-border lg:hidden">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-3 py-2.5 text-xs font-medium uppercase tracking-wide text-muted-foreground [&::-webkit-details-marker]:hidden">
            <span className="flex items-center gap-2">
              Worker pool
              <span className="text-muted-foreground/70">
                {workers.length} online
              </span>
            </span>
            <ChevronDown className="size-4 transition group-open:rotate-180" />
          </summary>
          <div className="border-t border-border p-3">
            {messages.length > 0 ? (
              <Button
                variant="outline"
                size="sm"
                className="mb-3 w-full gap-1.5"
                onClick={() => setMessages([])}
              >
                <SquarePen className="size-3.5" />
                New chat
              </Button>
            ) : null}
            <WorkerPool
              workers={workers}
              pinnedId={pinnedWorkerId}
              onPin={togglePin}
              loading={loading}
              projectEndpoint={projectEndpoint}
              hideHeader
            />
          </div>
        </details>

        {messages.length === 0 ? (
          <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 px-4">
            <p className="max-w-md text-center text-sm leading-7 text-muted-foreground">
              Open models run on machines you control, reached privately over
              rstream. The weights never leave your hardware.
            </p>
            <div className="grid w-full max-w-md gap-2">
              {SUGGESTIONS.map((suggestion) => (
                <button
                  key={suggestion}
                  type="button"
                  onClick={() => send(suggestion)}
                  disabled={selectedModel === ""}
                  className="truncate rounded-lg border border-border bg-card px-3.5 py-2.5 text-left text-sm text-foreground transition hover:border-input disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {suggestion}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <Conversation className="min-h-0 flex-1">
            <ConversationContent className="mx-auto w-full max-w-3xl gap-4 px-4">
              {messages.map((message, messageIndex) => (
                <Message from={message.role} key={message.id}>
                  <MessageContent>
                    {message.parts.map((part, index) => {
                      if (part.type === "text") {
                        return (
                          <MessageResponse key={index} linkSafety={LINK_SAFETY}>
                            {part.text}
                          </MessageResponse>
                        );
                      }
                      if (isToolUIPart(part)) {
                        return (
                          <ToolCallView
                            key={part.toolCallId}
                            part={part}
                            onApprove={approve}
                          />
                        );
                      }
                      if (part.type === "reasoning" && part.text) {
                        return (
                          <details key={index} className="my-1">
                            <summary className="cursor-pointer list-none text-xs font-medium uppercase tracking-wide text-muted-foreground [&::-webkit-details-marker]:hidden">
                              Reasoning
                            </summary>
                            <div className="mt-1 whitespace-pre-wrap rounded-md bg-secondary/50 p-2 text-xs leading-relaxed text-muted-foreground">
                              {part.text}
                            </div>
                          </details>
                        );
                      }
                      return null;
                    })}
                    {message.role === "assistant" &&
                    busy &&
                    messageIndex === messages.length - 1 &&
                    !hasVisibleContent(message) ? (
                      <ThinkingDots />
                    ) : null}
                  </MessageContent>
                  {message.role === "assistant" ? (
                    <div className="flex items-center gap-1 text-xs text-muted-foreground">
                      {message.metadata?.worker ? (
                        <span className="flex items-center gap-1.5">
                          <Cpu className="size-3" />
                          answered by {message.metadata.worker}
                        </span>
                      ) : null}
                      {messageText(message) ? (
                        <CopyButton text={messageText(message)} />
                      ) : null}
                      {messageIndex === messages.length - 1 &&
                      status === "ready" ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground"
                          aria-label="Regenerate"
                          onClick={() => regenerateMessage(message.id)}
                        >
                          <RefreshCw className="size-3.5" />
                        </Button>
                      ) : null}
                    </div>
                  ) : null}
                </Message>
              ))}
              {status === "submitted" ? (
                <Message from="assistant">
                  <MessageContent>
                    <ThinkingDots />
                  </MessageContent>
                </Message>
              ) : null}
            </ConversationContent>
            <ConversationScrollButton />
          </Conversation>
        )}

        {error ? (
          <div className="mx-auto w-full max-w-3xl shrink-0 px-4 pt-3">
            <div className="flex items-center gap-2.5 rounded-xl border border-destructive/20 bg-destructive/5 py-1.5 pl-3 pr-1.5 text-sm">
              <TriangleAlert className="size-4 shrink-0 text-destructive" />
              <span className="min-w-0 flex-1 line-clamp-2 text-destructive">
                {error.message || "Something went wrong. Please try again."}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 shrink-0 gap-1.5 px-2 text-destructive hover:bg-destructive/10 hover:text-destructive"
                onClick={retry}
              >
                <RefreshCw className="size-3.5" />
                Retry
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Dismiss"
                className="shrink-0 text-destructive/70 hover:bg-destructive/10 hover:text-destructive"
                onClick={clearError}
              >
                <X className="size-4" />
              </Button>
            </div>
          </div>
        ) : null}

        <form
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
          className="mx-auto w-full max-w-3xl shrink-0 px-4 pt-4"
        >
          <div className="rounded-xl border border-input bg-card p-2 shadow-sm">
            <Textarea
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  submit();
                }
              }}
              placeholder="Message your mesh…"
              rows={1}
              className="max-h-40 min-h-10 resize-none border-0 bg-transparent shadow-none focus-visible:ring-0"
            />
            <div className="mt-1 flex items-center justify-between gap-2">
              <div className="flex items-center gap-1.5">
                <input
                  ref={fileRef}
                  type="file"
                  accept=".log,.txt,.json,.yaml,.yml,.md,.csv,.out,.err,text/*"
                  className="hidden"
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (file) void attachFile(file);
                    event.target.value = "";
                  }}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Attach a log file"
                  className="text-muted-foreground"
                  onClick={() => fileRef.current?.click()}
                >
                  <Paperclip className="size-4" />
                </Button>
                <Select
                  value={selectedModel}
                  onValueChange={(value) => setModel(value ?? "")}
                  disabled={availableModels.length === 0}
                >
                  <SelectTrigger
                    size="sm"
                    className="w-auto gap-2 border-border"
                  >
                    <SelectValue
                      placeholder={loading ? "Loading models…" : "No models"}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {availableModels.map((m) => (
                      <SelectItem key={m} value={m}>
                        {m}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              {busy ? (
                <Button
                  type="button"
                  size="icon"
                  variant="secondary"
                  onClick={() => void stop()}
                  aria-label="Stop"
                >
                  <Square className="size-4" />
                </Button>
              ) : (
                <Button
                  type="submit"
                  size="icon"
                  disabled={!canSend}
                  aria-label="Send"
                >
                  <ArrowUp className="size-4" />
                </Button>
              )}
            </div>
          </div>
        </form>
      </main>
    </div>
  );
}
