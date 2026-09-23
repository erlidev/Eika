/**
 * How fast an answer was produced, under the answer itself.
 *
 * Two rates, never one: an endpoint reads a prompt and writes an answer at
 * speeds that differ by orders of magnitude, so a single figure over both
 * would describe neither. Generation comes first because it is the one a
 * reader waits on; prompt processing follows when the endpoint measured it,
 * which the harness cannot do for itself (docs/api/events.md).
 *
 * The line is off unless the reader asks for it in the settings, and it is
 * the smallest type in the app when it is on: it belongs to the answer above
 * it, not beside it.
 */

import { Gauge } from "lucide-react";

import type { Timings } from "@/api/events";
import { formatRate } from "@/lib/format";

export type SpeedProps = {
  timings: Timings | undefined;
};

/** timedBy says who measured the generation phase, for the tooltip. */
function timedBy(source: Timings["source"]): string {
  return source === "harness"
    ? "timed here, from the response's first streamed token"
    : "timed by the endpoint";
}

export function Speed({ timings }: SpeedProps) {
  const decode = formatRate(timings?.decode_tokens, timings?.decode_ms);
  const prompt = formatRate(timings?.prompt_tokens, timings?.prompt_ms);
  if (decode === "" && prompt === "") return null;

  return (
    <p className="text-muted-foreground mt-1.5 flex flex-wrap items-center gap-x-2 text-2xs">
      <Gauge aria-hidden className="size-3 shrink-0" />
      {decode !== "" && (
        <span title={`Generation: ${decode}, ${timedBy(timings?.source)}`}>
          <span className="sr-only">Generated at </span>
          <span className="font-mono">{decode}</span>
        </span>
      )}
      {prompt !== "" && (
        <span title={`Prompt processing: ${prompt}, timed by the endpoint`}>
          <span aria-hidden>· </span>
          <span className="font-mono">{prompt}</span> <span className="sr-only">reading the </span>
          prompt
        </span>
      )}
    </p>
  );
}
