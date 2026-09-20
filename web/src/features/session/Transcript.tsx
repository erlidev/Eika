/**
 * The conversation. One renderer per entry kind; tool calls delegate to the
 * per-tool registry through `ToolCard`.
 *
 * A turn streams a token at a time, so the list re-renders constantly: every
 * row is memoised on the item the reducer handed it, which only changes for
 * the row that changed. Without that, one delta re-parses every markdown
 * message in the session.
 */

import { ArrowDown, Scissors, TriangleAlert } from "lucide-react";
import { memo, useEffect, useMemo, useRef, useState } from "react";

import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";
import { Reasoning } from "@/features/session/Reasoning";
import { useSessionStore } from "@/features/session/store";
import { ToolCard } from "@/features/session/ToolCard";
import { cutOffText } from "@/features/session/transcript";
import type { TranscriptItem } from "@/features/session/transcript";
import { cn } from "@/lib/utils";

export type TranscriptProps = {
  /** empty is what the view shows before the session has any entries. */
  empty?: React.ReactNode;
};

/** bottomSlackPx is how far from the bottom still counts as following along. */
const bottomSlackPx = 100;

export function Transcript({ empty }: TranscriptProps) {
  // Three selectors rather than the whole store: a panel that only wants the
  // questions should not re-render on every token.
  const committed = useSessionStore((s) => s.committed);
  const live = useSessionStore((s) => s.live);
  const questions = useSessionStore((s) => s.questions);
  const stopReason = useSessionStore((s) => s.stopReason);

  const rendered = useMemo(() => [...committed, ...live], [committed, live]);
  const questionCalls = useMemo(() => new Set(questions.map((q) => q.call_id)), [questions]);
  const announcement = useAnnouncement(rendered);

  const scroller = useRef<HTMLDivElement>(null);
  const bottom = useRef<HTMLDivElement>(null);
  // Following the stream is the point of the view, so it scrolls itself —
  // but only while the user is at the bottom. Scrolling up to read earlier
  // output during a run is a deliberate act, and yanking it back is not.
  const [following, setFollowing] = useState(true);

  // A DOM measurement has no render-time equivalent, so this is an effect.
  useEffect(() => {
    if (!following) return;
    bottom.current?.scrollIntoView({ block: "end" });
  }, [rendered, following]);

  const jumpToLatest = () => {
    setFollowing(true);
    bottom.current?.scrollIntoView({ block: "end", behavior: "smooth" });
  };

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={scroller}
        // `relative` is load-bearing: the visually hidden headings inside the
        // rows are absolutely positioned at their place in the flow, so
        // without a containing block here they escape this scroller and
        // stretch the page itself by the transcript's scrolled height.
        className="relative min-h-0 flex-1 overflow-y-auto"
        onScroll={() => {
          const el = scroller.current;
          if (!el) return;
          setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight <= bottomSlackPx);
        }}
      >
        {rendered.length === 0 ? (
          <div className="text-muted-foreground flex h-full items-center justify-center p-8 text-sm">
            {empty ?? "No messages yet."}
          </div>
        ) : (
          <div className="mx-auto flex w-full max-w-3xl flex-col gap-4 px-4 py-5">
            {rendered.map((item) => (
              <Item
                key={item.key}
                item={item}
                openTool={item.kind === "tool" && questionCalls.has(item.callId)}
              />
            ))}
            <CutOff reason={stopReason} />
            <div ref={bottom} />
          </div>
        )}
      </div>
      {/*
        The bubbles are not live regions: a region that grew by a token would
        read the whole sentence again on every delta. One region reports that
        the turn started and then reads the reply once it is whole.
      */}
      <p role="status" aria-live="polite" className="sr-only">
        {announcement}
      </p>
      {!following && rendered.length > 0 && (
        <div className="pointer-events-none absolute inset-x-0 bottom-4 flex justify-center">
          <Button
            size="sm"
            variant="secondary"
            // The button floats over the transcript, so it casts a shadow.
            // eslint-disable-next-line no-restricted-syntax
            className="pointer-events-auto shadow-md"
            onClick={jumpToLatest}
          >
            <ArrowDown aria-hidden className="size-3.5" />
            Jump to latest
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * CutOff says that the last turn stopped before the model was finished. It
 * belongs in the transcript rather than the status bar: it is about the
 * answer above it, and it is gone as soon as the next turn starts.
 */
function CutOff({ reason }: { reason: string | undefined }) {
  const text = cutOffText(reason);
  if (text === undefined) return null;
  return (
    <p
      role="status"
      className="text-muted-foreground border-warning/50 flex items-center gap-2 border-l-2 pl-3 text-xs"
    >
      <Scissors aria-hidden className="size-3.5 shrink-0" />
      {text}
    </p>
  );
}

/**
 * useAnnouncement is what a screen reader hears about the turn in flight: a
 * note when the model starts writing, and the reply itself once it is done.
 * The transcript a session opens with is not announced — the reader is
 * reading it, not being told it arrived.
 */
function useAnnouncement(rendered: TranscriptItem[]): string {
  let latest = "";
  for (let i = rendered.length - 1; i >= 0; i -= 1) {
    const item = rendered[i];
    if (item?.kind !== "assistant") continue;
    latest = item.streaming ? "The assistant is replying." : item.text;
    break;
  }

  const [announcement, setAnnouncement] = useState("");
  const previous = useRef(latest);
  useEffect(() => {
    if (latest === previous.current) return;
    previous.current = latest;
    setAnnouncement(latest);
  }, [latest]);
  return announcement;
}

const Item = memo(function Item({ item, openTool }: { item: TranscriptItem; openTool: boolean }) {
  switch (item.kind) {
    case "user":
      // A message the user wrote is the one thing on screen that did not come
      // out of the model. It is a bubble against the right edge, narrower than
      // the model's full-width prose, so a glance down the transcript reads as
      // a conversation with two sides rather than one column of blocks.
      return (
        <div className="flex justify-end">
          <article className="bg-primary/10 border-primary/20 max-w-[80%] min-w-0 rounded-md border px-3.5 py-2">
            <h3 className="sr-only">You</h3>
            <p className="text-foreground text-sm leading-relaxed whitespace-pre-wrap">
              {item.text}
            </p>
          </article>
        </div>
      );
    case "reasoning":
      return <Reasoning item={item} />;
    case "assistant":
      return (
        <article className="min-w-0">
          <h3 className="sr-only">Assistant</h3>
          <Markdown>{item.text}</Markdown>
          {item.streaming && (
            <span
              aria-hidden
              className="bg-foreground ml-0.5 inline-block h-4 w-1.5 animate-pulse align-text-bottom"
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
});
