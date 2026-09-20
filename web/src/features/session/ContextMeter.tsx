/**
 * What the turn is costing: how much of the model's context window the
 * conversation fills, and what it has spent getting there.
 *
 * Every number here was measured by the endpoint and arrived in a
 * turn.progress or turn.end event (docs/api/events.md). Nothing is estimated
 * from the text on screen, so the meter is absent until an endpoint reports
 * usage. There is no decode rate: a Chat Completions endpoint reports its
 * token counts when a response ends, never while one streams, so a rate shown
 * during a turn could only be a guess dressed up as a measurement.
 *
 * The tooltips state the numbers and nothing else; the run panel's breakdown
 * is where they are explained.
 */

import type { Meter } from "@/features/session/transcript";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ContextMeterProps = {
  meter: Meter;
};

/** fullBand is how the bar reads at a glance: fine, filling up, nearly full. */
function fullBand(fraction: number): { bar: string; text: string } {
  if (fraction >= 0.9) return { bar: "bg-destructive", text: "text-destructive" };
  if (fraction >= 0.75) return { bar: "bg-warning", text: "text-warning" };
  return { bar: "bg-primary", text: "" };
}

export function ContextMeter({ meter }: ContextMeterProps) {
  const window = meter.contextWindow;
  const used = meter.context.total_tokens;
  if (used === 0) return null;
  const fraction = window > 0 ? Math.min(1, used / window) : 0;
  const percent = Math.round(fraction * 100);
  const band = fullBand(fraction);
  // The prompt is what a turn carries forward; the response is what it just
  // added to it. Two segments say which is which without a legend.
  const promptFraction = used === 0 ? 0 : fraction * (meter.context.input_tokens / used);

  // The tooltip repeats the bar in words and adds the split it cannot show.
  // What the turn cost belongs in the run panel: it is a different number,
  // and one the endpoint only updates when a model call ends.
  const title =
    window > 0
      ? `Context: ${formatTokens(used)} of ${formatTokens(window)} (${String(percent)}%) · ` +
        `${formatTokens(meter.context.input_tokens)} prompt, ` +
        `${formatTokens(meter.context.output_tokens)} response`
      : `Context: ${formatTokens(used)} · no window configured`;

  return (
    <span className="flex items-center gap-1.5" title={title}>
      <span className="sr-only">Context used</span>
      <span
        aria-hidden
        className="bg-muted relative h-1.5 w-20 shrink-0 overflow-hidden rounded-full"
      >
        <span
          className={cn("absolute inset-y-0 left-0 rounded-full transition-[width]", band.bar)}
          style={{ width: `${String(promptFraction * 100)}%` }}
        />
        <span
          className={cn("absolute inset-y-0 rounded-full opacity-50 transition-all", band.bar)}
          style={{
            left: `${String(promptFraction * 100)}%`,
            width: `${String((fraction - promptFraction) * 100)}%`,
          }}
        />
      </span>
      <span className={cn("font-mono tabular-nums", band.text)}>
        {formatTokens(used)}
        {window > 0 && (
          <>
            <span className="text-muted-foreground">/{formatTokens(window)}</span>{" "}
            <span className={band.text === "" ? "text-muted-foreground" : undefined}>
              {percent}%
            </span>
          </>
        )}
      </span>
    </span>
  );
}

export type ContextBreakdownProps = {
  meter: Meter | undefined;
};

/**
 * ContextBreakdown is the meter with room to explain itself: the same bar,
 * larger, with what each part of it is and what the turn cost. The status bar
 * has space for a number; the run panel has space for the answer to "why is
 * it that number".
 */
export function ContextBreakdown({ meter }: ContextBreakdownProps) {
  if (!meter || meter.context.total_tokens === 0) {
    return <p className="text-muted-foreground">No model call has reported its usage yet.</p>;
  }
  const window = meter.contextWindow;
  const used = meter.context.total_tokens;
  const fraction = window > 0 ? Math.min(1, used / window) : 0;
  const band = fullBand(fraction);
  const promptFraction = fraction * (meter.context.input_tokens / used);

  return (
    <div className="space-y-2">
      {window > 0 && (
        <>
          <span aria-hidden className="bg-muted relative flex h-2 overflow-hidden rounded-full">
            <span
              className={cn("rounded-full", band.bar)}
              style={{ width: `${String(promptFraction * 100)}%` }}
            />
            <span
              className={cn("rounded-full opacity-50", band.bar)}
              style={{ width: `${String((fraction - promptFraction) * 100)}%` }}
            />
          </span>
          <p className={cn("font-mono tabular-nums", band.text)}>
            {formatTokens(used)} of {formatTokens(window)} · {Math.round(fraction * 100)}% full
          </p>
        </>
      )}
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
        <dt className="text-muted-foreground">Prompt</dt>
        <dd className="font-mono tabular-nums">{formatTokens(meter.context.input_tokens)}</dd>
        <dt className="text-muted-foreground">Response</dt>
        <dd className="font-mono tabular-nums">{formatTokens(meter.context.output_tokens)}</dd>
        {window > 0 && (
          <>
            <dt className="text-muted-foreground">Free</dt>
            <dd className="font-mono tabular-nums">{formatTokens(Math.max(0, window - used))}</dd>
          </>
        )}
        <dt
          className="text-muted-foreground"
          title="Every model call the turn has made, as the endpoint reported them. A call in flight is counted when it ends."
        >
          Turn cost
        </dt>
        <dd className="font-mono tabular-nums">
          {formatTokens(meter.usage.input_tokens)} in · {formatTokens(meter.usage.output_tokens)}{" "}
          out
        </dd>
      </dl>
    </div>
  );
}
