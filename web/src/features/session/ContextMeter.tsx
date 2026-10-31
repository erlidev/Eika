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
 * that was produced at that speed, so `Speed.tsx` states it there, and the
 * breakdown below repeats it for a reader who asked for it.
 *
 * The tooltips state the numbers and nothing else; the run panel's breakdown
 * is where they are explained.
 */

import type { NextPrediction } from "@/api/types";
import { useTranscriptPreferences } from "@/features/session/preferences";
import type { Meter } from "@/features/session/transcript";
import { contextFill, formatRate, formatReported, formatTokens } from "@/lib/format";
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
  // What the turn cost belongs in the run panel: it is a different number,
  // and one the endpoint only updates when a model call ends.
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

export type ContextBreakdownProps = {
  meter: Meter | undefined;
  /** prediction is how large the next request is expected to be and how that is known. */
  prediction?: NextPrediction | undefined;
};

/**
 * ContextBreakdown is the meter with room to explain itself: the same bar,
 * larger, with what each part of it is and what the turn cost. The status bar
 * has space for a number; the run panel has space for the answer to "why is
 * it that number".
 */
export function ContextBreakdown({ meter, prediction }: ContextBreakdownProps) {
  const showSpeed = useTranscriptPreferences((s) => s.speed);
  const used = meter ? contextFill(meter.context) : undefined;
  if (!meter || used === undefined || used === 0) {
    return (
      <div className="space-y-2">
        <p className="text-muted-foreground">No model call has reported its usage yet.</p>
        {prediction !== undefined && (
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
            <NextRequestRow prediction={prediction} />
          </dl>
        )}
      </div>
    );
  }
  const window = meter.contextWindow;
  const fraction = window > 0 ? Math.min(1, used / window) : 0;
  const band = fullBand(fraction);
  const prompt = meter.context.input_tokens;
  const promptFraction = prompt === null ? fraction : fraction * (prompt / used);

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
        <dd className="font-mono tabular-nums">{formatReported(meter.context.input_tokens)}</dd>
        <dt className="text-muted-foreground">Response</dt>
        <dd className="font-mono tabular-nums">{formatReported(meter.context.output_tokens)}</dd>
        <dt
          className="text-muted-foreground"
          title="Part of the prompt the endpoint served from its cache."
        >
          Cached
        </dt>
        <dd className="font-mono tabular-nums">{formatReported(meter.context.cached_tokens)}</dd>
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
          {formatReported(meter.usage.input_tokens)} in ·{" "}
          {formatReported(meter.usage.output_tokens)} out
        </dd>
        {prediction !== undefined && <NextRequestRow prediction={prediction} />}
        {showSpeed && <SpeedRows meter={meter} />}
      </dl>
    </div>
  );
}

type NextRequestRowProps = { prediction: NextPrediction };

/** NextRequestRow states the next request's expected size and how it is known. */
function NextRequestRow({ prediction }: NextRequestRowProps) {
  return (
    <>
      <dt className="text-muted-foreground" title={methodTitles[prediction.method]}>
        Next request
      </dt>
      <dd className="font-mono tabular-nums">
        {prediction.method === "estimated" ? "~" : ""}
        {formatTokens(prediction.tokens)} <span className="font-sans">{prediction.method}</span>
      </dd>
    </>
  );
}

type SpeedRowsProps = { meter: Meter };

/**
 * SpeedRows are the last model call's two rates, for a reader who turned them
 * on. They are rows of the breakdown's list rather than a block of their own,
 * so they read as two more measurements of the same call.
 */
function SpeedRows({ meter }: SpeedRowsProps) {
  const decode = formatRate(meter.timings?.decode_tokens, meter.timings?.decode_ms);
  const prompt = formatRate(meter.timings?.prompt_tokens, meter.timings?.prompt_ms);
  return (
    <>
      {decode !== "" && (
        <>
          <dt
            className="text-muted-foreground"
            title={
              meter.timings?.source === "harness"
                ? "Timed here, from the response's first streamed token."
                : "Timed by the endpoint."
            }
          >
            Generation
          </dt>
          <dd className="font-mono tabular-nums">{decode}</dd>
        </>
      )}
      {prompt !== "" && (
        <>
          <dt className="text-muted-foreground" title="Timed by the endpoint.">
            Prompt processing
          </dt>
          <dd className="font-mono tabular-nums">{prompt}</dd>
        </>
      )}
    </>
  );
}
