/**
 * What the Context panel and inspector draw, as pure functions: one request
 * split into the parts that fill the context window with the endpoint's own
 * count of each and what the template adds, the size of the whole request and
 * how it is known, the tools by where they come from and their parameters, the
 * request as one JSON document, and the setting behind each part.
 */

import type {
  ContextSection,
  MessageSize,
  ModelContext,
  PredictedSize,
  SectionKind,
  ToolSchema,
} from "@/api/types";

/** SegmentKind names one part of a request, in the order the bar draws them. */
export type SegmentKind =
  SectionKind | "builtin_tools" | "mcp_tools" | "messages" | "reasoning" | "template";

/**
 * Segment is one part of a request and its size. approximate says the size is
 * not the endpoint's own count of it. The template segment is the remainder,
 * which is negative when the parts add up to more than the endpoint counted.
 */
export type Segment = { kind: SegmentKind; label: string; tokens: number; approximate: boolean };

/** segmentLabels are how the panel names each part. */
export const segmentLabels: Record<SegmentKind, string> = {
  base: "Base prompt",
  instructions: "Instructions",
  preamble: "Preamble",
  builtin_tools: "Built-in tools",
  mcp_tools: "MCP tools",
  messages: "Messages",
  reasoning: "Replayed reasoning",
  template: "Template and formatting",
};

/** segmentOrder is the bar's order: the system prompt, the tools, the conversation, what the template adds. */
const segmentOrder: readonly SegmentKind[] = [
  "base",
  "instructions",
  "builtin_tools",
  "mcp_tools",
  "messages",
  "reasoning",
  "template",
];

/**
 * segments splits a request into the parts the bar draws, from the
 * breakdown's own counts, leaving out a part the request does not have. The
 * template segment is the remainder, left out while there is nothing to state
 * it against: it is the endpoint's count less the parts, never a share
 * scaled to fit.
 */
export function segments(c: ModelContext): Segment[] {
  const b = c.breakdown;
  const tokens: Record<SegmentKind, number> = {
    base: 0,
    instructions: 0,
    preamble: 0,
    builtin_tools: 0,
    mcp_tools: 0,
    messages: 0,
    reasoning: 0,
    template: b.remainder ?? 0,
  };
  const approximate: Record<SegmentKind, boolean> = {
    base: false,
    instructions: false,
    preamble: false,
    builtin_tools: false,
    mcp_tools: false,
    messages: false,
    reasoning: false,
    template: false,
  };
  for (const s of b.sections) {
    tokens[s.kind] += s.tokens;
    approximate[s.kind] ||= s.approximate === true;
  }
  b.tools.forEach((t, i) => {
    const kind = c.tools?.[i]?.source === "mcp" ? "mcp_tools" : "builtin_tools";
    tokens[kind] += t.tokens;
    approximate[kind] ||= t.approximate === true;
  });
  for (const m of b.messages) {
    tokens.messages += m.tokens;
    approximate.messages ||= m.approximate === true;
    tokens.reasoning += m.reasoning_tokens ?? 0;
    approximate.reasoning ||= m.approximate === true && (m.reasoning_tokens ?? 0) > 0;
  }
  return segmentOrder
    .filter(
      (kind) =>
        tokens[kind] > 0 ||
        (kind === "messages" && (c.messages ?? []).length > 0) ||
        (kind === "template" && b.remainder !== null),
    )
    .map((kind) => ({
      kind,
      label: segmentLabels[kind],
      tokens: tokens[kind],
      approximate: approximate[kind],
    }));
}

/**
 * barTotal is what the parts bar divides: every part and what the template
 * adds, which is nothing when the parts already add up to more than the
 * endpoint counted.
 */
export function barTotal(parts: readonly Segment[]): number {
  return parts.reduce((n, s) => n + Math.max(0, s.tokens), 0);
}

/** RequestSize is how large a request is and how that is known. */
export type RequestSize = {
  tokens: number;
  /**
   * basis is what the number is: `measured` by the endpoint for a recorded
   * call, `predicted` by the token counter, or only the `parts` added up,
   * which falls short of the request by what the template adds.
   */
  basis: "measured" | "predicted" | "parts";
  /** method is how a prediction was reached. */
  method?: PredictedSize["method"];
};

/** predictionOf is what the counter expected of a request: the next one's, or a recorded call's own. */
export function predictionOf(c: ModelContext): PredictedSize | undefined {
  if (c.prediction !== undefined) return c.prediction;
  const call = c.call;
  if (call?.predicted_prompt_tokens != null && call.prediction !== undefined) {
    return { tokens: call.predicted_prompt_tokens, method: call.prediction };
  }
  return undefined;
}

/**
 * requestSize is the best number for the whole request: what the endpoint
 * measured, else what the counter predicted, else the parts.
 */
export function requestSize(c: ModelContext): RequestSize {
  const b = c.breakdown;
  if (b.prompt_tokens !== null && b.prompt_source === "measured") {
    return { tokens: b.prompt_tokens, basis: "measured" };
  }
  const predicted = predictionOf(c);
  if (predicted !== undefined) {
    return { tokens: predicted.tokens, basis: "predicted", method: predicted.method };
  }
  return { tokens: b.parts, basis: "parts" };
}

/**
 * exactDrift is how far an exact prediction was off, or undefined when it was
 * not off (or was not exact). Any drift on an exact prediction is a bug in the
 * counting or in the request that was counted, so it is shown as one.
 */
export function exactDrift(c: ModelContext): number | undefined {
  const call = c.call;
  if (call?.prediction !== "exact" || call.drift === null || call.drift === 0) return undefined;
  return call.drift;
}

/** SizedSection is a section with the endpoint's count of it in place of the estimate. */
export type SizedSection = ContextSection & { approximate: boolean };

/** sizedSections are a request's sections with their counted sizes. */
export function sizedSections(c: ModelContext): SizedSection[] {
  return (c.sections ?? []).map((s, i) => ({
    ...s,
    tokens: c.breakdown.sections[i]?.tokens ?? 0,
    approximate: c.breakdown.sections[i]?.approximate === true,
  }));
}

/** SizedTool is a tool with the endpoint's count of it in place of the estimate. */
export type SizedTool = ToolSchema & { approximate: boolean };

/** sizedTools are a request's tools with their counted sizes. */
export function sizedTools(c: ModelContext): SizedTool[] {
  return (c.tools ?? []).map((t, i) => ({
    ...t,
    tokens: c.breakdown.tools[i]?.tokens ?? 0,
    approximate: c.breakdown.tools[i]?.approximate === true,
  }));
}

/** messageSizes are the counted sizes of a request's messages, one for each. */
export function messageSizes(c: ModelContext): MessageSize[] {
  return (c.messages ?? []).map((_, i) => c.breakdown.messages[i] ?? { tokens: 0 });
}

/** remainderNote says what the template's line is and what it is not. */
export function remainderNote(context: ModelContext): string {
  const b = context.breakdown;
  if (b.remainder === null) {
    return "n/a: no measurement of this request to state it against.";
  }
  const basis =
    b.prompt_source === "predicted"
      ? "the exact predicted size of the request"
      : "what the endpoint measured";
  const base = `${basis} less the parts above. Nothing is scaled to make them add up.`;
  return b.remainder < 0
    ? `${base} It is negative: the parts add up to more than the endpoint counted, so a part is counted wrongly.`
    : base;
}

/** tilde marks a size that is not the endpoint's own count. */
export function tilde(approximate: boolean): string {
  return approximate ? "~" : "";
}

/** share is a part's fraction of the whole, in percent, for the bar's width. */
export function share(tokens: number, total: number): number {
  return total <= 0 ? 0 : (tokens / total) * 100;
}

/** ToolGroup is the tools of a request that come from one place, and what they cost. */
export type ToolGroup = {
  /** id names the group: builtin, resources, or mcp:<server>. */
  id: string;
  label: string;
  /** server is the MCP server whose tools the group holds, if it is one. */
  server?: string;
  tools: SizedTool[];
  tokens: number;
  /** approximate says some tool of the group was not counted by the endpoint. */
  approximate: boolean;
};

/**
 * serverOf is the MCP server a tool reaches by its name, mcp__<server>__<tool>,
 * or undefined for a built-in tool and for the resource tools, which reach
 * every server of the run.
 */
export function serverOf(name: string): string | undefined {
  if (!name.startsWith("mcp__")) return undefined;
  const rest = name.slice("mcp__".length);
  const end = rest.indexOf("__");
  return end <= 0 ? undefined : rest.slice(0, end);
}

/**
 * toolGroupsOf sorts a request's tools by where they come from: the built-in
 * ones, then each MCP server's by name, then the resource tools.
 */
export function toolGroupsOf(tools: readonly SizedTool[]): ToolGroup[] {
  const builtin: SizedTool[] = [];
  const resources: SizedTool[] = [];
  const servers = new Map<string, SizedTool[]>();
  for (const t of tools) {
    const server = serverOf(t.name);
    if (server !== undefined) servers.set(server, [...(servers.get(server) ?? []), t]);
    else if (t.source === "mcp") resources.push(t);
    else builtin.push(t);
  }
  const group = (id: string, label: string, list: SizedTool[], server?: string): ToolGroup => ({
    id,
    label,
    ...(server === undefined ? {} : { server }),
    tools: list,
    tokens: list.reduce((n, t) => n + t.tokens, 0),
    approximate: list.some((t) => t.approximate),
  });
  return [
    ...(builtin.length > 0 ? [group("builtin", "Built-in", builtin)] : []),
    ...[...servers.keys()]
      .sort()
      .map((server) => group(`mcp:${server}`, server, servers.get(server) ?? [], server)),
    ...(resources.length > 0 ? [group("resources", "MCP resources", resources)] : []),
  ];
}

/** SchemaParameter is one parameter of a tool, as its JSON Schema describes it. */
export type SchemaParameter = {
  name: string;
  /** type is the parameter's type as a person reads it: string, integer, string[]. */
  type: string;
  required: boolean;
  description: string;
  /** values are the only values an enum parameter takes. */
  values?: string[];
};

/** asRecord is v as an object with string keys, or undefined when it is not one. */
function asRecord(v: unknown): Record<string, unknown> | undefined {
  return typeof v === "object" && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : undefined;
}

/** typeText words a property's type: its type or types, an array by its items, else any. */
function typeText(p: Record<string, unknown>): string {
  const t = p.type;
  if (t === "array") {
    const items = asRecord(p.items);
    return `${items === undefined ? "any" : typeText(items)}[]`;
  }
  if (typeof t === "string") return t;
  if (Array.isArray(t)) return t.filter((x) => typeof x === "string").join(" | ");
  if (Array.isArray(p.enum)) return "enum";
  return "any";
}

/**
 * schemaParameters reads the top-level parameters of a tool's JSON Schema,
 * required ones first, each in the order the schema lists it. A schema
 * that is not an object with properties has none.
 */
export function schemaParameters(schema: unknown): SchemaParameter[] {
  const root = asRecord(schema);
  const properties = asRecord(root?.properties);
  if (properties === undefined) return [];
  const required = new Set(
    Array.isArray(root?.required)
      ? root.required.filter((x): x is string => typeof x === "string")
      : [],
  );
  const out = Object.entries(properties).map(([name, raw]): SchemaParameter => {
    const p = asRecord(raw) ?? {};
    const values = Array.isArray(p.enum) ? p.enum.map((v) => String(v)) : undefined;
    return {
      name,
      type: typeText(p),
      required: required.has(name),
      description: typeof p.description === "string" ? p.description : "",
      ...(values === undefined ? {} : { values }),
    };
  });
  return [...out.filter((p) => p.required), ...out.filter((p) => !p.required)];
}

/** WindowUse is how a request fills its model's context window. */
export type WindowUse = {
  /** used is the request's size, and reserved the room its answer may take. */
  used: number;
  reserved: number;
  /** free is what neither takes; zero once they overflow the window. */
  free: number;
  window: number;
  /** overflows says the request and its answer do not fit. */
  overflows: boolean;
};

/**
 * windowUse is how a request of `used` tokens, whose answer may take
 * `reserved`, fills a window; with no window known it fills exactly what
 * it uses.
 */
export function windowUse(used: number, reserved: number, window: number): WindowUse {
  if (window <= 0) return { used, reserved: 0, free: 0, window: used, overflows: false };
  const free = Math.max(0, window - used - reserved);
  return { used, reserved, free, window, overflows: used + reserved > window };
}

/**
 * requestJSON is a request as one JSON document, the way the harness hands
 * it to a provider: the system prompt its sections make, the tool
 * definitions, the messages, and the parameters. A provider adapts it to its
 * own endpoint's shape, so this is what was asked for, not the wire bytes.
 */
export function requestJSON(c: ModelContext): Record<string, unknown> {
  const p = c.parameters;
  return {
    model: p.model,
    system: (c.sections ?? []).map((s) => s.text).join("\n\n"),
    tools: (c.tools ?? []).map((t) => ({
      name: t.name,
      description: t.description,
      parameters: t.schema,
    })),
    messages: c.messages ?? [],
    ...p.sampling,
    ...(p.thinking_switch === undefined ? {} : { thinking_switch: p.thinking_switch }),
    preserve_thinking: p.preserve_thinking,
    image_input: p.image_input,
  };
}

/** EditTarget is the setting a part of a request comes from, keyed as the request's sources are. */
export type EditTarget = { key: string };

/**
 * editTargetOf is the setting behind a part of the request, or undefined
 * for one no editor sets: the messages are the conversation's.
 */
export function editTargetOf(kind: SegmentKind): EditTarget | undefined {
  switch (kind) {
    case "base":
      return { key: "chat_prompt" };
    case "instructions":
      return { key: "instructions" };
    case "builtin_tools":
    case "mcp_tools":
      return { key: "tools" };
    case "messages":
    case "reasoning":
    case "template":
      return undefined;
  }
}

/**
 * parameterTarget is the setting behind one parameter a request sends, keyed
 * as the request's sources are, or undefined for one only a model row sets.
 */
export function parameterTarget(key: string): EditTarget | undefined {
  if (key === "model" || key === "preserve_thinking" || key.startsWith("sampling.")) {
    return { key };
  }
  return undefined;
}

/** segmentColors are the parts' colours, from the theme's chart tokens. */
export const segmentColors: Record<SegmentKind, string> = {
  base: "bg-chart-1",
  instructions: "bg-chart-3",
  preamble: "bg-chart-3",
  builtin_tools: "bg-chart-5",
  mcp_tools: "bg-chart-4",
  messages: "bg-primary/40",
  reasoning: "bg-chart-2",
  template: "bg-muted-foreground/40",
};

/** percent words a share of a whole, with a decimal below ten percent. */
export function percent(part: number, whole: number): string {
  if (whole <= 0) return "0%";
  const p = (part / whole) * 100;
  if (p > 0 && p < 0.1) return "<0.1%";
  return `${p.toFixed(p < 10 ? 1 : 0)}%`;
}

/**
 * View names what the inspector's pane shows: the system prompt, one of its
 * sections, the tools or one group of them, the messages,
 * or the parameters.
 */
export type View =
  | "system"
  | `section:${"base" | "instructions" | "preamble"}`
  | "tools"
  | `group:${string}`
  | "messages"
  | "parameters";

/** viewOf is the view a part of the bar opens. */
export function viewOf(kind: SegmentKind): View {
  switch (kind) {
    case "builtin_tools":
      return "group:builtin";
    case "mcp_tools":
      return "tools";
    case "messages":
    case "reasoning":
      return "messages";
    case "template":
      return "system";
    default:
      return `section:${kind}`;
  }
}
