/** The pure part of the Inbox: flagged text split for highlighting, and what an answer's failure says. */

import { ApiError } from "@/api/client";
import type { MemoryScreenSpan } from "@/api/types";
import { failureText } from "@/lib/failure";

/** Segment is a run of a field's text, flagged or not. */
export type Segment = { text: string; flagged: boolean };

/**
 * segments splits a field's text at the screen's spans of that field. The
 * harness counts spans in bytes of the UTF-8 text, so the split works on
 * bytes; overlapping spans merge and a span outside the text is dropped.
 */
export function segments(
  text: string,
  field: MemoryScreenSpan["field"],
  spans: readonly MemoryScreenSpan[],
): Segment[] {
  const bytes = new TextEncoder().encode(text);
  const decoder = new TextDecoder();
  const ranges = spans
    .filter((s) => s.field === field && s.start >= 0 && s.start < s.end && s.start < bytes.length)
    .map((s) => ({ start: s.start, end: Math.min(s.end, bytes.length) }))
    .sort((a, b) => a.start - b.start);
  const out: Segment[] = [];
  let at = 0;
  for (const range of ranges) {
    const start = Math.max(range.start, at);
    if (range.end <= start) continue;
    if (start > at) out.push({ text: decoder.decode(bytes.slice(at, start)), flagged: false });
    const text = decoder.decode(bytes.slice(start, range.end));
    const last = out[out.length - 1];
    if (last?.flagged === true && start === at) last.text += text;
    else out.push({ text, flagged: true });
    at = range.end;
  }
  if (at < bytes.length) out.push({ text: decoder.decode(bytes.slice(at)), flagged: false });
  return out.length === 0 ? [{ text, flagged: false }] : out;
}

/** Answer is what the user did to a pending memory. */
export type Answer = "accept" | "reject";

/**
 * answerFailure says plainly why an answer did not go through. A stale or
 * repeated answer (409) was refused and nothing changed, a credential (400)
 * names itself in the harness's reason, and 503 is memory or the injection
 * screen not being ready: the memory stays pending in every case.
 */
export function answerFailure(answer: Answer, error: unknown): string {
  const verb = answer === "accept" ? "accept" : "reject";
  if (error instanceof ApiError) {
    if (error.status === 409) {
      return "This memory changed or was already answered, possibly on another device, so nothing was saved. The card now shows its current state.";
    }
    if (error.status === 400) return `Not ${verb}ed: ${error.message}`;
    if (error.status === 503) {
      return "Memory is not ready, or the injection screen it requires is not answering. The memory stays pending; try again in a moment.";
    }
  }
  return failureText(`${verb} the memory`, error);
}
