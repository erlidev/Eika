/**
 * The conversation. One renderer per entry kind; tool calls delegate to the
 * per-tool registry through `ToolCard`.
 */

import { TriangleAlert } from "lucide-react";
import { useEffect, useRef } from "react";

import { Markdown } from "@/components/Markdown";
import { useSessionStore } from "@/features/session/store";
import { ToolCard } from "@/features/session/ToolCard";
import { items } from "@/features/session/transcript";
import type { TranscriptItem } from "@/features/session/transcript";
import { cn } from "@/lib/utils";

export type TranscriptProps = {
  /** empty is what the view shows before the session has any entries. */
  empty?: React.ReactNode;
};

export function Transcript({ empty }: TranscriptProps) {
  const state = useSessionStore();
  const rendered = items(state);
  const questionCalls = new Set(state.questions.map((q) => q.call_id));
  const bottom = useRef<HTMLDivElement>(null);

  // Following the stream is the point of the view, so it scrolls itself.
  // A DOM measurement has no render-time equivalent, so this is an effect.
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [rendered.length, state.live]);

  if (rendered.length === 0) {
    return (
      <div className="text-muted-foreground flex flex-1 items-center justify-center p-8 text-sm">
        {empty ?? "No messages yet."}
      </div>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-3 p-4">
      {rendered.map((item) => (
        <Item
          key={item.key}
          item={item}
          openTool={item.kind === "tool" && questionCalls.has(item.callId)}
        />
      ))}
      <div ref={bottom} />
    </div>
  );
}

function Item({ item, openTool }: { item: TranscriptItem; openTool: boolean }) {
  switch (item.kind) {
    case "user":
      return (
        <article className="bg-muted/60 ml-auto max-w-[85%] rounded-md border px-3 py-2">
          <h3 className="sr-only">You</h3>
          <p className="text-sm whitespace-pre-wrap">{item.text}</p>
        </article>
      );
    case "assistant":
      return (
        <article className="min-w-0">
          <h3 className="sr-only">Assistant</h3>
          <Markdown>{item.text}</Markdown>
          {item.streaming && (
            <span
              aria-label="still writing"
              className="bg-foreground ml-0.5 inline-block h-3.5 w-1.5 animate-pulse align-text-bottom"
            />
          )}
        </article>
      );
    case "tool":
      return <ToolCard call={item} open={openTool} />;
    case "error":
      return (
        <div
          role="alert"
          className={cn(
            "border-destructive/40 bg-destructive/5 text-destructive flex items-start gap-2 rounded-md border px-3 py-2 text-sm",
          )}
        >
          <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />
          <div>
            <p>{item.message}</p>
            {item.retryable && (
              <p className="text-muted-foreground text-xs">
                The failure was retryable; the run used up its retries.
              </p>
            )}
          </div>
        </div>
      );
    default:
      return item.text === "" ? null : (
        <p className="text-muted-foreground border-l-2 pl-3 font-mono text-xs whitespace-pre-wrap">
          {item.text}
        </p>
      );
  }
}
