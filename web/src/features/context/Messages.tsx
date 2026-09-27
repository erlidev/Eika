/** The messages of a request, every one whole, filtered by author. */

import { useState } from "react";

import type { Message } from "@/api/types";
import { MessageImages } from "@/components/MessageImages";
import { OutputBlock } from "@/components/OutputBlock";
import { Button } from "@/components/ui/button";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

/** roleTone is how each author's messages are marked. */
const roleTone: Record<Message["role"], string> = {
  user: "border-primary/40 bg-primary/10 text-primary",
  assistant: "bg-muted text-foreground border-transparent",
  tool: "border-chart-3/40 bg-chart-3/10 text-chart-3",
  system: "bg-muted text-muted-foreground border-transparent",
};

type MessagesProps = { messages: readonly Message[]; sizes: readonly number[] };

/** Messages is the conversation as the request sends it, every message whole. */
export function Messages({ messages, sizes }: MessagesProps) {
  const [role, setRole] = useState<Message["role"] | "all">("all");
  if (messages.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No messages yet: the next one starts the conversation.
      </p>
    );
  }
  const names = new Map<string, string>();
  for (const m of messages) for (const c of m.tool_calls ?? []) names.set(c.id, c.name);
  const largest = Math.max(...sizes, 1);
  const roles = (["user", "assistant", "tool"] as const).filter((r) =>
    messages.some((m) => m.role === r),
  );
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1" role="group" aria-label="Show messages by">
        {(["all", ...roles] as const).map((r) => (
          <Button
            key={r}
            type="button"
            size="xs"
            variant={role === r ? "secondary" : "ghost"}
            aria-pressed={role === r}
            onClick={() => {
              setRole(r);
            }}
          >
            {r === "all" ? "All" : r}
            <span className="text-muted-foreground font-mono tabular-nums">
              {r === "all" ? messages.length : messages.filter((m) => m.role === r).length}
            </span>
          </Button>
        ))}
      </div>
      <ol className="space-y-2">
        {messages.map((m, i) =>
          role !== "all" && m.role !== role ? null : (
            // The messages are a fixed list for this request; their order is their identity.
            <li key={`${String(i)}:${m.role}`}>
              <MessageCard
                n={i + 1}
                message={m}
                size={sizes[i] ?? 0}
                largest={largest}
                toolName={m.tool_call_id === undefined ? undefined : names.get(m.tool_call_id)}
              />
            </li>
          ),
        )}
      </ol>
    </div>
  );
}

type MessageCardProps = {
  n: number;
  message: Message;
  size: number;
  largest: number;
  toolName: string | undefined;
};

/** longMessage is how many characters a message shows before it folds. */
const longMessage = 1200;

function MessageCard({ n, message: m, size, largest, toolName }: MessageCardProps) {
  const [whole, setWhole] = useState(false);
  const content = m.content ?? "";
  const folded = !whole && content.length > longMessage;
  return (
    <article
      className="overflow-hidden rounded-md border"
      aria-label={`Message ${String(n)}, ${m.role}`}
    >
      <header className="flex flex-wrap items-center gap-2 border-b px-3 py-1.5">
        <span className="text-muted-foreground font-mono text-2xs tabular-nums">#{n}</span>
        <span className={cn("rounded-full border px-1.5 text-2xs font-medium", roleTone[m.role])}>
          {m.role}
        </span>
        {toolName !== undefined && (
          <span className="text-muted-foreground font-mono text-xs">result of {toolName}</span>
        )}
        {m.is_error === true && <span className="text-destructive text-xs">error</span>}
        <span className="ml-auto flex items-center gap-2">
          <span
            className="bg-muted hidden h-1.5 w-16 overflow-hidden rounded-full sm:block"
            aria-hidden
          >
            <span
              className="bg-primary/60 block h-full"
              style={{ width: `${String((size / largest) * 100)}%` }}
            />
          </span>
          <span className="text-muted-foreground font-mono text-xs tabular-nums">
            ~{formatTokens(size)}
          </span>
        </span>
      </header>
      <div className="space-y-2 px-3 py-2">
        {m.reasoning !== undefined && m.reasoning !== "" && (
          <div className="space-y-0.5">
            <p className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
              Reasoning, replayed
            </p>
            <p className="text-muted-foreground text-xs whitespace-pre-wrap italic">
              {m.reasoning}
            </p>
          </div>
        )}
        <MessageImages images={m.images ?? []} />
        {content !== "" && (
          <p
            className={cn(
              "text-sm break-words whitespace-pre-wrap",
              m.role === "tool" && "font-mono text-xs",
              m.is_error === true && "text-destructive",
            )}
          >
            {folded ? `${content.slice(0, longMessage)}…` : content}
          </p>
        )}
        {content.length > longMessage && (
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => {
              setWhole(!whole);
            }}
          >
            {whole ? "Show less" : `Show all ${formatTokens(content.length)} characters`}
          </Button>
        )}
        {(m.tool_calls ?? []).map((c) => (
          <div key={c.id} className="space-y-1">
            <p className="text-xs">
              Calls <span className="font-mono font-medium">{c.name}</span>
              <span className="text-muted-foreground font-mono"> {c.id}</span>
            </p>
            <OutputBlock label={`Arguments of ${c.name}`} maxHeightClass="max-h-60">
              {c.arguments_malformed === true
                ? String(c.arguments)
                : JSON.stringify(c.arguments, null, 2)}
            </OutputBlock>
          </div>
        ))}
      </div>
    </article>
  );
}
