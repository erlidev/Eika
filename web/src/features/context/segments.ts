/**
 * What the Context panel's token bar is made of, as pure functions: one
 * request split into the parts that fill the context window, and the
 * estimate scaled to what the endpoint measured.
 */

import type { ModelContext, SectionKind } from "@/api/types";

/** SegmentKind names one part of a request, in the order the bar draws them. */
export type SegmentKind = SectionKind | "builtin_tools" | "mcp_tools" | "messages";

/** Segment is one part of a request and its estimated size. */
export type Segment = { kind: SegmentKind; label: string; tokens: number };

/** segmentLabels are how the panel names each part. */
export const segmentLabels: Record<SegmentKind, string> = {
  base: "Base prompt",
  context_files: "Context files",
  instructions: "Instructions",
  builtin_tools: "Built-in tools",
  mcp_tools: "MCP tools",
  messages: "Messages",
};

/** segmentOrder is the bar's order: the system prompt, the tools, the conversation. */
const segmentOrder: readonly SegmentKind[] = [
  "base",
  "context_files",
  "instructions",
  "builtin_tools",
  "mcp_tools",
  "messages",
];

/**
 * segments splits a request into the parts the bar draws, leaving out a
 * part the request does not have.
 */
export function segments(c: ModelContext): Segment[] {
  const tokens: Record<SegmentKind, number> = {
    base: 0,
    context_files: 0,
    instructions: 0,
    builtin_tools: 0,
    mcp_tools: 0,
    messages: c.message_tokens,
  };
  for (const s of c.sections ?? []) tokens[s.kind] += s.tokens;
  for (const t of c.tools ?? [])
    tokens[t.source === "mcp" ? "mcp_tools" : "builtin_tools"] += t.tokens;
  return segmentOrder
    .filter((kind) => tokens[kind] > 0 || (kind === "messages" && (c.messages ?? []).length > 0))
    .map((kind) => ({ kind, label: segmentLabels[kind], tokens: tokens[kind] }));
}

/** estimatedTotal is the estimated size of everything a request sends. */
export function estimatedTotal(parts: readonly Segment[]): number {
  return parts.reduce((n, s) => n + s.tokens, 0);
}

/**
 * scale is how much the endpoint's count exceeds the estimate, from a call
 * it measured, or undefined when none was measured. The estimates count
 * bytes; a tokenizer counts otherwise, so the ratio of one real call is the
 * best correction there is.
 */
export function scale(c: ModelContext): number | undefined {
  const m = c.calibration;
  if (m === undefined || m.input_tokens <= 0 || m.estimated_tokens <= 0) return undefined;
  return m.input_tokens / m.estimated_tokens;
}

/** share is a part's fraction of the whole, in percent, for the bar's width. */
export function share(tokens: number, total: number): number {
  return total <= 0 ? 0 : (tokens / total) * 100;
}
