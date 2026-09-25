/**
 * The conversation. One renderer per entry kind; tool calls delegate to the
 * per-tool registry through `ToolCard`.
 *
 * A turn streams a token at a time, so the list re-renders constantly: every
 * row is memoised on the item the reducer handed it, which only changes for
 * the row that changed. Without that, one delta re-parses every markdown
 * message in the session.
 */

import { ArrowDown, Scissors, TriangleAlert, Undo2 } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";
import { useRewind, useSessionOutline, useRunStatus } from "@/features/session/queries";
import { useTranscriptPreferences } from "@/features/session/preferences";
import { Reasoning } from "@/features/session/Reasoning";
import { Speed } from "@/features/session/Speed";
import { rewindTarget } from "@/features/session/tree";
import { useSessionStore } from "@/features/session/store";
import { ToolCard } from "@/features/session/ToolCard";
import { cutOffText } from "@/features/session/transcript";
import type { AssistantItem, TranscriptItem } from "@/features/session/transcript";
import { cn } from "@/lib/utils";
import { failureText } from "@/lib/failure";

export type TranscriptProps = {
  /** sessionId is the session on screen, which a rewind acts on. */
  sessionId: string;
  /** empty is what the view shows before the session has any entries. */
  empty?: React.ReactNode;
};

/** bottomSlackPx is how far from the bottom still counts as following along. */
const bottomSlackPx = 100;

export function Transcript({ sessionId, empty }: TranscriptProps) {
  // Three selectors rather than the whole store: a panel that only wants the
  // questions should not re-render on every token.
  const committed = useSessionStore((s) => s.committed);
  const live = useSessionStore((s) => s.live);
  const questions = useSessionStore((s) => s.questions);
  const stopReason = useSessionStore((s) => s.stopReason);

  const rendered = useMemo(() => [...committed, ...live], [committed, live]);
  const questionCalls = useMemo(() => new Set(questions.map((q) => q.call_id)), [questions]);
  const announcement = useAnnouncement(rendered);

  // Rewinding takes the conversation back to before a message the user sent
  // and puts it back in the composer. A run in progress owns the head, so it
  // is offered only while the session is idle.
  const outline = useSessionOutline(sessionId);
  const status = useRunStatus(sessionId);
  const rewind = useRewind(sessionId);
  const idle = !(status.data?.active ?? false);
  const nodes = outline.data?.nodes;
  const rewindable = useMemo(() => {
    const ids = new Set<string>();
    for (const node of nodes ?? []) {
      if (node.kind === "user" && rewindTarget(nodes ?? [], node.id) !== null) ids.add(node.id);
    }
    return ids;
  }, [nodes]);
  // A stable callback, so that a row memoised on its item is not re-rendered
  // by every token of the turn in flight.
  const onRewind = useCallback(
    (entryId: string, text: string) => {
      rewind.mutate({ entryId, text });
    },
    [rewind],
  );

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
                onRewind={
                  idle && item.kind === "user" && rewindable.has(item.entryId ?? "")
                    ? onRewind
                    : undefined
                }
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
      {rewind.isError && (
        <p role="alert" className="text-destructive border-t px-4 py-2 text-xs">
          {failureText("rewind the conversation", rewind.error)}
        </p>
      )}
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

/**
 * Assistant is one block of model prose. It reads the speed preference
 * itself, so that turning the line on or off re-renders the answers and
 * nothing else.
 */
function Assistant({ item }: { item: AssistantItem }) {
  const speed = useTranscriptPreferences((s) => s.speed);
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
      {speed && <Speed timings={item.timings} />}
    </article>
  );
}

const Item = memo(function Item({
  item,
  openTool,
  onRewind,
}: {
  item: TranscriptItem;
  openTool: boolean;
  /** onRewind is absent when this row cannot be rewound to. */
  onRewind?: (entryId: string, text: string) => void;
}) {
  switch (item.kind) {
    case "user":
      // A message the user wrote is the one thing on screen that did not come
      // out of the model. It is a bubble against the right edge, narrower than
      // the model's full-width prose, so a glance down the transcript reads as
      // a conversation with two sides rather than one column of blocks.
      //
      // The rewind button sits outside the bubble, in the gutter the bubble's
      // width leaves free, so it never reflows the message it belongs to.
      return (
        <div className="group flex items-start justify-end gap-1">
          {onRewind !== undefined && item.entryId !== undefined && (
            <Button
              size="icon-xs"
              variant="ghost"
              className="mt-1 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 focus-visible:opacity-100"
              aria-label="Rewind the conversation to this message and edit it"
              title="Rewind to this message and edit it"
              onClick={() => {
                onRewind(item.entryId ?? "", item.text);
              }}
            >
              <Undo2 aria-hidden className="size-3.5" />
            </Button>
          )}
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
      return <Assistant item={item} />;
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
