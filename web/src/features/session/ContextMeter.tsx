/**
 * What the turn is costing: how much of the model's effective context window
 * the conversation fills, how much of it the endpoint reused from its cache,
 * what the turn has spent getting there, and how large the next request is
 * expected to be.
 *
 * The fill, the cache and the cost were measured by the endpoint and arrived
 * in a turn.progress or turn.end event (docs/api/events.md); nothing is
 * estimated from the text on screen. The next request's size is the token
 * counter's prediction (`GET /api/sessions/{id}/prediction`) and says how it
 * is known; an `estimated` one carries a tilde. Anything not reported is n/a,
 * never 0. How fast the turn ran is not in the bar: it belongs to the answer
 * that was produced at that speed, so `Speed.tsx` states it there.
 *
 * The tooltips state the numbers and nothing else.
 */

import type { NextPrediction } from "@/api/types";
import type { Meter } from "@/features/session/transcript";
import { contextFill, formatReported, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ContextMeterProps = {
  /** meter is what the last model call measured; absent before any call has. */
  meter?: Meter | undefined;
  /** prediction is how large the next request is expected to be and how that is known. */
  prediction?: NextPrediction | undefined;
};

/** fullBand is how the bar reads at a glance: fine, filling up, nearly full. */
function fullBand(fraction: number): { bar: string; text: string } {
  if (fraction >= 0.9) return { bar: "bg-destructive", text: "text-destructive" };
  if (fraction >= 0.75) return { bar: "bg-warning", text: "text-warning" };
  return { bar: "bg-primary", text: "" };
}

/** methodTitles say what each way of knowing the next request's size means. */
const methodTitles = {
  exact: "Counted by the endpoint's own tokenizer: a run now measures exactly this.",
  anchored:
    "The text is counted by the endpoint and what the template adds is learned from earlier calls.",
  estimated: "The endpoint has no tokenizer for this, so the size is estimated from bytes.",
} as const;

/**
 * ContextMeter is the status bar's account of the context: how much of the
 * effective window the last call filled, how much of its prompt the endpoint
 * served from its cache, and how large the next request is expected to be and
 * how that is known. Before any call has been measured the fill and the cache
 * say n/a and the prediction stands alone; a count the endpoint did not state
 * is n/a, never 0.
 */
export function ContextMeter({ meter, prediction }: ContextMeterProps) {
  const filled = meter === undefined ? undefined : contextFill(meter.context);
  const used = filled === undefined || filled === 0 ? undefined : filled;
  if (meter === undefined || used === undefined) {
    return prediction === undefined ? null : <WaitingMeter prediction={prediction} />;
  }
  const window = meter.contextWindow;
  const { input_tokens: prompt, output_tokens: response } = meter.context;
  const fraction = window > 0 ? Math.min(1, used / window) : 0;
  const percent = Math.round(fraction * 100);
  const band = fullBand(fraction);
  // The prompt is what a turn carries forward; the response is what it just
  // added to it. Two segments say which is which without a legend. An
  // endpoint that did not state the prompt cannot say where the split falls,
  // so the bar is one segment.
  const promptFraction = prompt === null ? fraction : fraction * (prompt / used);

  // The tooltip repeats the bar in words and adds the split it cannot show.
  const split = [
    ...(prompt === null ? [] : [`${formatTokens(prompt)} prompt`]),
    ...(response === null ? [] : [`${formatTokens(response)} response`]),
  ].join(", ");
  const title =
    window > 0
      ? `Context: ${formatTokens(used)} of ${formatTokens(window)} (${String(percent)}%)` +
        (split === "" ? "" : ` · ${split}`)
      : `Context: ${formatTokens(used)} · no window configured`;

  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
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
      <CachedTokens cached={meter.context.cached_tokens} />
      {prediction !== undefined && <NextRequest prediction={prediction} />}
    </span>
  );
}

type WaitingMeterProps = { prediction: NextPrediction };

/**
 * WaitingMeter is the meter before any call has been measured: an empty bar
 * against the effective window, n/a for what no call has reported, and the
 * prediction, which is the one number there is to show.
 */
function WaitingMeter({ prediction }: WaitingMeterProps) {
  const window = prediction.context_window;
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
      <span
        className="flex items-center gap-1.5"
        title="No model call has reported its usage yet, so nothing has been measured."
      >
        <span className="sr-only">Context used</span>
        <span aria-hidden className="bg-muted h-1.5 w-20 shrink-0 rounded-full" />
        <span className="font-mono tabular-nums">
          n/a
          {window > 0 && <span className="text-muted-foreground">/{formatTokens(window)}</span>}
        </span>
      </span>
      <CachedTokens cached={null} />
      <NextRequest prediction={prediction} />
    </span>
  );
}

type CachedTokensProps = { cached: number | null };

/** CachedTokens is how much of the last prompt the endpoint reused, n/a when it did not say. */
function CachedTokens({ cached }: CachedTokensProps) {
  return (
    <span title="Part of the last call's prompt the endpoint served from its cache.">
      cached <span className="font-mono tabular-nums">{formatReported(cached)}</span>
    </span>
  );
}

type NextRequestProps = { prediction: NextPrediction };

/** NextRequest is the expected size of the next request, with a tilde on a guess and the method that made it. */
function NextRequest({ prediction }: NextRequestProps) {
  return (
    <span
      title={`The next request, as the token counter sees it. ${methodTitles[prediction.method]}`}
    >
      next{" "}
      <span className="font-mono tabular-nums">
        {prediction.method === "estimated" ? "~" : ""}
        {formatTokens(prediction.tokens)}
      </span>{" "}
      {prediction.method}
    </span>
  );
}
