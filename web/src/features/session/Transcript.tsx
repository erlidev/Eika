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
import {
  Profiler,
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { MessageImage } from "@/api/types";
import { Markdown } from "@/components/Markdown";
import { ScrollRegion } from "@/components/ScrollRegion";
import { MessageImages } from "@/components/MessageImages";
import { Button } from "@/components/ui/button";
import { SubagentLink } from "@/features/session/renderers/HomeRenderer";
import { Compaction } from "@/features/session/Compaction";
import { useRewind, useSessionOutline, useRunStatus } from "@/features/session/queries";
import { useTranscriptPreferences } from "@/features/session/preferences";
import { Reasoning } from "@/features/session/Reasoning";
import { Speed } from "@/features/session/Speed";
import { rewindableEntries } from "@/features/session/tree";
import { useSessionStore } from "@/features/session/store";
import { ToolCard } from "@/features/session/ToolCard";
import { cutOffText, visibleItems } from "@/features/session/transcript";
import type { AssistantItem, TranscriptItem } from "@/features/session/transcript";
import { cn } from "@/lib/utils";
import type { TranscriptPages } from "@/features/session/useSessionStream";
import { failureText } from "@/lib/failure";

declare const __EIKA_MEASURE_HISTORY__: boolean;

declare global {
  interface Window {
    eikaHistoryMeasurement?: { rows: number; commits: { duration: number; rows: number }[] };
  }
}

export type TranscriptProps = {
  /** sessionId is the session on screen, which a rewind acts on. */
  sessionId: string;
  pages: TranscriptPages;
  /** empty is what the view shows before the session has any entries. */
  empty?: React.ReactNode;
  /**
   * readOnly is a transcript that is watched, not steered: it reads no outline
   * and no row offers a rewind, which would move the head of a session Eika owns.
   */
  readOnly?: boolean;
};

const none: TranscriptItem[] = [];

/** bottomSlackPx is how far from the bottom still counts as following along. */
const bottomSlackPx = 100;

export function Transcript({ sessionId, pages, empty, readOnly = false }: TranscriptProps) {
  // Three selectors rather than the whole store: a panel that only wants the
  // questions should not re-render on every token.
  const storeSessionId = useSessionStore((s) => s.sessionId);
  const storedCommitted = useSessionStore((s) => s.committed);
  const storedLive = useSessionStore((s) => s.live);
  // The store holds one session, and opens the next in an effect, so for one
  // render a view that just switched sessions would still show the last one.
  const open = storeSessionId === sessionId;
  const committed = open ? storedCommitted : none;
  const live = open ? storedLive : none;
  const questions = useSessionStore((s) => s.questions);
  const elicitations = useSessionStore((s) => s.elicitations);
  const stopReason = useSessionStore((s) => s.stopReason);

  const rendered = useMemo(() => visibleItems(committed, live), [committed, live]);
  const announcement = useAnnouncement(rendered);

  // Rewinding takes the conversation back to before a message the user sent
  // and puts it back in the composer. A run in progress owns the head, so it
  // is offered only while the session is idle.
  const outline = useSessionOutline(readOnly ? undefined : sessionId);
  const status = useRunStatus(sessionId);
  const rewind = useRewind(sessionId);
  const rewindMutate = rewind.mutate;
  const idle = !(status.data?.active ?? false);
  // A card whose call waits on the user opens by itself: an ask_user
  // question, or an MCP server asking for input. The run state holds a
  // request made before this page subscribed.
  const waitingCalls = useMemo(
    () =>
      new Set([
        ...questions.map((q) => q.call_id),
        ...elicitations.map((x) => x.call_id),
        ...(status.data?.elicitations ?? []).map((x) => x.call_id),
      ]),
    [questions, elicitations, status.data?.elicitations],
  );
  const nodes = outline.data?.nodes;
  const rewindable = useMemo(() => rewindableEntries(nodes ?? []), [nodes]);
  // A stable callback, so that a row memoised on its item is not re-rendered
  // by every token of the turn in flight.
  const onRewind = useCallback(
    (entryId: string, text: string, images: MessageImage[]) => {
      rewindMutate({ entryId, text, images });
    },
    [rewindMutate],
  );

  const scroller = useRef<HTMLDivElement>(null);
  const bottom = useRef<HTMLDivElement>(null);
  const anchor = useRef<{ element: Element; top: number } | null>(null);
  useLayoutEffect(() => {
    const el = scroller.current;
    const saved = anchor.current;
    if (el && saved?.element.isConnected) {
      el.scrollTop += saved.element.getBoundingClientRect().top - saved.top;
    }
    anchor.current = null;
  }, [pages.pageVersion]);

  const loadOlder = () => {
    pages.loadOlder(() => {
      const el = scroller.current;
      if (!el) return;
      const top = el.getBoundingClientRect().top;
      const row = [...el.querySelectorAll("[data-transcript-row]")].find(
        (item) => item.getBoundingClientRect().bottom > top,
      );
      if (row) anchor.current = { element: row, top: row.getBoundingClientRect().top };
      setFollowing(false);
    });
  };
  // Following the stream is the point of the view, so it scrolls itself —
  // but only while the user is at the bottom. Scrolling up to read earlier
  // output during a run is a deliberate act, and yanking it back is not.
  const [following, setFollowing] = useState(true);

  // A DOM measurement has no render-time equivalent, so this is an effect.
  useEffect(() => {
    if (!following) return;
    bottom.current?.scrollIntoView({ block: "end" });
    // A question opens its card without adding a row, so it counts too.
  }, [rendered, following, waitingCalls]);

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
        {pages.hasOlder && (
          <div className="flex justify-center px-4 pt-4">
            <Button size="sm" variant="secondary" disabled={pages.loading} onClick={loadOlder}>
              {pages.loading ? "Loading older messages…" : "Load older messages"}
            </Button>
          </div>
        )}
        {pages.error !== undefined && (
          <div
            role="alert"
            className="text-destructive flex items-center justify-center gap-2 p-3 text-xs"
          >
            {failureText("load messages", pages.error)}
            <Button size="sm" variant="secondary" onClick={pages.retry}>
              Retry
            </Button>
          </div>
        )}
        {rendered.length === 0 ? (
          <div className="text-muted-foreground flex h-full items-center justify-center p-8 text-sm">
            {empty ?? "No messages yet."}
          </div>
        ) : (
          <Profiler
            id="transcript"
            onRender={(_id, _phase, duration) => {
              if (!__EIKA_MEASURE_HISTORY__) return;
              const measurement = window.eikaHistoryMeasurement;
              if (measurement) {
                measurement.commits.push({ duration, rows: measurement.rows });
                measurement.rows = 0;
              }
            }}
          >
            <div className="mx-auto flex w-full max-w-3xl flex-col gap-4 px-4 py-5">
              {rendered.map((item) => (
                <div key={item.key} data-transcript-row>
                  <Item
                    item={item}
                    openTool={
                      item.kind === "tool" &&
                      (waitingCalls.has(item.callId) ||
                        // A watched subagent's stored question has no live event on reload.
                        (readOnly && item.name === "ask_user" && !item.done))
                    }
                    readOnly={readOnly}
                    onRewind={
                      idle && item.kind === "user" && rewindable.has(item.entryId ?? "")
                        ? onRewind
                        : undefined
                    }
                  />
                </div>
              ))}
              <CutOff reason={stopReason} />
              <div ref={bottom} />
            </div>
          </Profiler>
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
            // eslint-disable-next-line no-restricted-syntax -- It floats over the transcript, so it casts a shadow.
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

type CutOffProps = { reason: string | undefined };

/**
 * CutOff says that the last turn stopped before the model was finished. It
 * belongs in the transcript rather than the status bar: it is about the
 * answer above it, and it is gone as soon as the next turn starts.
 */
function CutOff({ reason }: CutOffProps) {
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

type AssistantProps = { item: AssistantItem };

/**
 * Assistant is one block of model prose. It reads the speed preference
 * itself, so that turning the line on or off re-renders the answers and
 * nothing else.
 */
function Assistant({ item }: AssistantProps) {
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

type ItemProps = {
  item: TranscriptItem;
  openTool: boolean;
  /** readOnly is passed to the tool cards, which then take no answers. */
  readOnly: boolean;
  /** onRewind is absent when this row cannot be rewound to. */
  onRewind?: (entryId: string, text: string, images: MessageImage[]) => void;
};

const Item = memo(function Item({ item, openTool, readOnly, onRewind }: ItemProps) {
  if (__EIKA_MEASURE_HISTORY__ && window.eikaHistoryMeasurement) {
    // eslint-disable-next-line react-hooks/immutability -- Count render attempts only in the profiling build.
    window.eikaHistoryMeasurement.rows += 1;
  }
  switch (item.kind) {
    case "user":
      // A message the user wrote is the one thing on screen that did not come
      // out of the model. It is a bubble against the right edge, narrower than
      // the model's full-width prose, so a glance down the transcript reads as
      // a conversation with two sides rather than one column of blocks.
      //
      // It is written in markdown and shown rendered, as the model's answers
      // are; the composer and a rewind hand back the source to edit.
      //
      // The rewind button sits outside the bubble, in the gutter the bubble's
      // width leaves free, so it never reflows the message it belongs to.
      return (
        <div className="group flex items-start justify-end gap-1">
          {onRewind !== undefined && item.entryId !== undefined && (
            <Button
              size="icon-xs"
              variant="ghost"
              className="rewind-action mt-1 transition-opacity"
              aria-label="Rewind the conversation to this message and edit it"
              title="Rewind to this message and edit it"
              onClick={() => {
                onRewind(item.entryId ?? "", item.text, item.images);
              }}
            >
              <Undo2 aria-hidden className="size-3.5" />
            </Button>
          )}
          <article className="bg-primary/10 border-primary/20 max-w-[80%] min-w-0 rounded-md border px-3.5 py-2">
            <h3 className="sr-only">You</h3>
            <MessageImages images={item.images} className={item.text === "" ? "" : "mb-2"} />
            {item.text !== "" && (
              // A table's header and stripes are shades of muted, which the
              // bubble's tint swallows; on the page's own background they show.
              <Markdown
                lineBreaks
                className="text-foreground [&_table]:bg-background text-sm leading-relaxed"
              >
                {item.text}
              </Markdown>
            )}
          </article>
        </div>
      );
    case "reasoning":
      return <Reasoning item={item} />;
    case "assistant":
      return <Assistant item={item} />;
    case "tool":
      return <ToolCard call={item} open={openTool} readOnly={readOnly} />;
    case "compaction":
      return <Compaction item={item} />;
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
    case "notice": {
      if (item.text === "") return null;
      const name = /^([a-z0-9-]+) \([^)]+\) (?:reported:|asks:|was interrupted)/.exec(
        item.text,
      )?.[1];
      const asked = item.question;
      if (asked !== undefined) {
        return (
          <article className="bg-muted/40 min-w-0 rounded-md border px-3 py-2 text-sm">
            <h3 className="text-muted-foreground mb-1 text-xs font-semibold">
              {asked.name} ({asked.agent}) asks
            </h3>
            <p className="break-words whitespace-pre-wrap">{asked.question}</p>
            {(asked.options?.length ?? 0) > 0 && (
              <ul className="text-muted-foreground mt-1 list-disc pl-5 text-xs">
                {asked.options?.map((option) => (
                  <li key={option}>{option}</li>
                ))}
              </ul>
            )}
            <SubagentLink name={asked.name} />
          </article>
        );
      }
      return (
        <article className="bg-muted/40 min-w-0 rounded-md border px-3 py-2 text-sm">
          <h3 className="text-muted-foreground mb-1 text-xs font-semibold">
            {item.entryKind === "notice"
              ? "Notice"
              : item.entryKind === "preamble"
                ? "Session context"
                : item.entryKind}
          </h3>
          <ScrollRegion label="Notice text" className="max-h-48 whitespace-pre-wrap break-words">
            {item.text}
          </ScrollRegion>
          {name !== undefined && <SubagentLink name={name} />}
        </article>
      );
    }
  }
});
