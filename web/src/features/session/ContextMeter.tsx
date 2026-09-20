/**
 * What the turn is costing: how much of the model's context window the
 * conversation fills, and how fast the endpoint is decoding.
 *
 * Every number here was measured by the harness and arrived in a turn.progress
 * or turn.end event (docs/api/events.md). Nothing is estimated from the text
 * on screen, so the meter is absent until an endpoint reports usage, and the
 * rate is absent until two samples have been measured. An endpoint that never
 * reports usage shows nothing rather than something made up.
 */

import { Gauge } from "lucide-react";

import type { Meter } from "@/features/session/transcript";
import { formatRate, formatTokens } from "@/lib/format";
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

  const title =
    window > 0
      ? `Context: ${formatTokens(used)} of ${formatTokens(window)} tokens (${String(percent)}%), ` +
        `as the endpoint reported them: ${formatTokens(meter.context.input_tokens)} prompt and ` +
        `${formatTokens(meter.context.output_tokens)} response. This turn has cost ` +
        `${formatTokens(meter.usage.total_tokens)} tokens over every model call it made.`
      : `Context: ${formatTokens(used)} tokens, as the endpoint reported them. ` +
        `The model's window is not configured, so there is nothing to compare it with.`;

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

export type DecodeRateProps = {
  meter: Meter;
};

/**
 * DecodeRate is the endpoint's output speed, measured between the two most
 * recent usage reports. It disappears rather than guessing when the endpoint
 * reports usage only once, at the end, before which nothing has been measured.
 */
export function DecodeRate({ meter }: DecodeRateProps) {
  const rate = meter.tokensPerSecond;
  if (rate === undefined) return null;
  return (
    <span
      className={cn("flex items-center gap-1", meter.live && "text-foreground")}
      title={
        meter.live
          ? "Output tokens a second, measured between the endpoint's two most recent usage reports."
          : "Output tokens a second over the most recent measured interval of the completed turn."
      }
    >
      <Gauge aria-hidden className="size-3.5" />
      <span className="font-mono tabular-nums">{formatRate(rate)} tok/s</span>
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
    return (
      <p className="text-muted-foreground">
        No model call has reported its usage yet. Nothing here is estimated, so there is nothing to
        show until one does.
      </p>
    );
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
        <dt className="text-muted-foreground">Turn cost</dt>
        <dd className="font-mono tabular-nums">
          {formatTokens(meter.usage.input_tokens)} in · {formatTokens(meter.usage.output_tokens)}{" "}
          out
        </dd>
        <dt className="text-muted-foreground">Decode</dt>
        <dd className="font-mono tabular-nums">
          {meter.tokensPerSecond === undefined
            ? "not measured yet"
            : `${formatRate(meter.tokensPerSecond)} tok/s`}
        </dd>
      </dl>
      <p className="text-muted-foreground">
        The prompt is what the turn carries forward and the response is what the last model call
        added to it, both as the endpoint reported them.
      </p>
    </div>
  );
}
