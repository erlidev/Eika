/**
 * How large a request is and where the tokens go, as the panel and the
 * inspector both say it: the size of the whole request with how it is known,
 * the counter's drift when an exact prediction was wrong, the facts of a
 * recorded call, and the parts with the template's remainder as a line of its
 * own.
 */

import type { ModelContext } from "@/api/types";
import { Notice } from "@/components/Notice";
import { Dot } from "@/features/context/parts";
import {
  exactDrift,
  percent,
  remainderNote,
  requestSize,
  tilde,
  viewOf,
} from "@/features/context/segments";
import type { RequestSize, Segment, View } from "@/features/context/segments";
import { formatReported, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

/** methodNotes say what each prediction method means for the number. */
const methodNotes = {
  exact: "Counted by the endpoint's own tokenizer, so a run now measures exactly this.",
  anchored:
    "The text is counted by the endpoint; what the template adds around it is learned from earlier calls.",
  estimated: "The endpoint has no tokenizer for this, so the size is an estimate from bytes.",
} as const;

/** sizeText is a request size as the headline states it: a tilde when it is only an estimate. */
function sizeText(size: RequestSize): string {
  const approximate = size.basis === "parts" || size.method === "estimated";
  return `${tilde(approximate)}${size.tokens.toLocaleString("en-US")}`;
}

type RequestHeadlineProps = { context: ModelContext };

/** RequestHeadline is the size of the request and how it is known. */
export function RequestHeadline({ context }: RequestHeadlineProps) {
  const size = requestSize(context);
  const call = context.call;
  return (
    <div className="space-y-0.5">
      <p className="flex flex-wrap items-baseline gap-x-1.5">
        <span className="font-mono text-lg font-semibold tabular-nums">{sizeText(size)}</span>
        <span className="text-muted-foreground">
          tokens{" "}
          {size.basis === "measured"
            ? "measured by the endpoint"
            : size.basis === "predicted"
              ? `predicted, ${size.method ?? "estimated"}`
              : "in the parts"}
        </span>
      </p>
      <p className="text-muted-foreground">
        {size.basis === "predicted" && size.method !== undefined && `${methodNotes[size.method]} `}
        {size.basis === "parts" &&
          "Nothing measured and nothing predicted for this request: this is only what its parts add up to. "}
        {call === undefined ? "What a message sent now is added to." : `Sent to ${call.model}.`}
      </p>
    </div>
  );
}

type DriftNoticeProps = { context: ModelContext };

/**
 * DriftNotice says a call's exact prediction was wrong. The counter asked the
 * endpoint to render and tokenise the request itself, so any difference from
 * what the endpoint then measured is a bug in the counting or in the request
 * that was counted, and it is shown as one.
 */
export function DriftNotice({ context }: DriftNoticeProps) {
  const drift = exactDrift(context);
  const call = context.call;
  if (drift === undefined || call === undefined) return null;
  return (
    <Notice tone="error">
      The token counter predicted{" "}
      <span className="font-mono">{call.predicted_prompt_tokens?.toLocaleString("en-US")}</span>{" "}
      tokens for this call, exactly, and the endpoint counted{" "}
      <span className="font-mono">{call.prompt_tokens?.toLocaleString("en-US")}</span>: a drift of{" "}
      <span className="font-mono">{drift > 0 ? `+${String(drift)}` : String(drift)}</span>. An exact
      prediction is never off, so this is a bug in the counting or in what was counted. Report it
      with this call.
    </Notice>
  );
}

type CallFactsProps = { context: ModelContext };

/** CallFacts is what the endpoint reported for a recorded call, with n/a for what it did not. */
export function CallFacts({ context }: CallFactsProps) {
  const call = context.call;
  if (call === undefined) return null;
  const predicted = call.predicted_prompt_tokens;
  const rows: [string, string, string?][] = [
    ["Prompt", formatReported(call.prompt_tokens), "Includes the cached part."],
    ["Cached", formatReported(call.cached_tokens), "The part of the prompt the endpoint reused."],
    ["Prefilled", formatReported(call.prefilled_tokens), "The part of the prompt it processed."],
    ["Completion", formatReported(call.completion_tokens), "Includes the reasoning."],
    [
      "Reasoning",
      call.reasoning_tokens === null
        ? "n/a"
        : `${call.reasoning_source === "tokenised" ? "~" : ""}${formatTokens(call.reasoning_tokens)}`,
      call.reasoning_source === "tokenised"
        ? "Counted from the reasoning text; the endpoint reported none."
        : undefined,
    ],
    [
      "Predicted",
      predicted === null || call.prediction === undefined
        ? "n/a"
        : `${tilde(call.prediction === "estimated")}${formatTokens(predicted)} ${call.prediction}`,
      call.drift === null ? undefined : `Drift ${String(call.drift)}.`,
    ],
  ];
  return (
    <dl className="flex flex-wrap gap-x-4 gap-y-0.5">
      {rows.map(([name, value, title]) => (
        <div key={name} className="flex gap-1.5">
          <dt className="text-muted-foreground" {...(title === undefined ? {} : { title })}>
            {name}
          </dt>
          <dd className="font-mono tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

type PartRowsProps = {
  context: ModelContext;
  parts: readonly Segment[];
  total: number;
  onOpen: (view: View) => void;
};

/**
 * PartRows lists the parts with their sizes, and the template's remainder as a
 * line of its own: n/a while there is nothing to state it against, negative
 * and flagged when the parts add up to more than the endpoint counted.
 */
export function PartRows({ context, parts, total, onOpen }: PartRowsProps) {
  const hasTemplate = parts.some((p) => p.kind === "template");
  const note = remainderNote(context);
  return (
    <ul aria-label="Parts" className="-mx-1">
      {parts.map((part) => {
        const negative = part.tokens < 0;
        const figures = (
          <>
            <Dot kind={part.kind} />
            <span className="min-w-0 flex-1 truncate">{part.label}</span>
            <span className="text-muted-foreground w-10 text-right font-mono tabular-nums">
              {negative ? "" : percent(part.tokens, total)}
            </span>
            <span
              className={cn(
                "w-14 text-right font-mono tabular-nums",
                negative && "text-destructive",
              )}
            >
              {tilde(part.approximate)}
              {negative ? `-${formatTokens(-part.tokens)}` : formatTokens(part.tokens)}
            </span>
          </>
        );
        return (
          <li key={part.kind}>
            {part.kind === "template" ? (
              <div className="flex w-full items-center gap-2 rounded-md px-1 py-0.5" title={note}>
                {figures}
              </div>
            ) : (
              <button
                type="button"
                onClick={() => {
                  onOpen(viewOf(part.kind));
                }}
                className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-md px-1 py-0.5 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none"
              >
                {figures}
              </button>
            )}
          </li>
        );
      })}
      {!hasTemplate && (
        <li>
          <div className="flex w-full items-center gap-2 rounded-md px-1 py-0.5" title={note}>
            <Dot kind="template" />
            <span className="min-w-0 flex-1 truncate">Template and formatting</span>
            <span className="text-muted-foreground w-10 text-right" />
            <span className="text-muted-foreground w-14 text-right font-mono">n/a</span>
          </div>
        </li>
      )}
    </ul>
  );
}

type PartsNotesProps = { context: ModelContext };

/** PartsNotes explains the marks on the parts: the tilde and the template line. */
export function PartsNotes({ context }: PartsNotesProps) {
  const b = context.breakdown;
  const approximate =
    b.sections.some((s) => s.approximate === true) ||
    b.tools.some((t) => t.approximate === true) ||
    b.messages.some((m) => m.approximate === true);
  return (
    <div className="text-muted-foreground space-y-1">
      <p>Template and formatting: {remainderNote(context)}</p>
      {approximate && (
        <p>
          A <span className="font-mono">~</span> marks a part the endpoint did not count itself:
          estimated from bytes, a picture&apos;s cost assumed, or a tool sized as JSON text.
        </p>
      )}
    </div>
  );
}
