/**
 * What the Context panel and inspector draw, as pure functions: one request
 * split into the parts that fill the context window, the estimate scaled to
 * what the endpoint measured, the tools by where they come from and their
 * parameters, the request as one JSON document, and the setting behind each
 * part.
 */

import type { ModelContext, SectionKind, ToolSchema } from "@/api/types";
import type { EditorSection } from "@/features/profiles/form";

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

/** ToolGroup is the tools of a request that come from one place, and what they cost. */
export type ToolGroup = {
  /** id names the group: builtin, resources, or mcp:<server>. */
  id: string;
  label: string;
  /** server is the MCP server whose tools the group holds, if it is one. */
  server?: string;
  tools: ToolSchema[];
  tokens: number;
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
export function toolGroupsOf(tools: readonly ToolSchema[]): ToolGroup[] {
  const builtin: ToolSchema[] = [];
  const resources: ToolSchema[] = [];
  const servers = new Map<string, ToolSchema[]>();
  for (const t of tools) {
    const server = serverOf(t.name);
    if (server !== undefined) servers.set(server, [...(servers.get(server) ?? []), t]);
    else if (t.source === "mcp") resources.push(t);
    else builtin.push(t);
  }
  const group = (id: string, label: string, list: ToolSchema[], server?: string): ToolGroup => ({
    id,
    label,
    ...(server === undefined ? {} : { server }),
    tools: list,
    tokens: list.reduce((n, t) => n + t.tokens, 0),
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
  };
}

/** EditTarget is the setting a part of a request comes from, and the editor tab that holds it. */
export type EditTarget = { key: string; section: EditorSection };

/**
 * editTargetOf is the setting behind a part of the request, or undefined
 * for one no editor sets: the messages are the conversation's.
 */
export function editTargetOf(kind: SegmentKind, chat: boolean): EditTarget | undefined {
  switch (kind) {
    case "base":
      return { key: chat ? "chat_prompt" : "workspace_prompt", section: "prompt" };
    case "context_files":
      return { key: "context_files", section: "prompt" };
    case "instructions":
      return { key: "instructions", section: "prompt" };
    case "builtin_tools":
    case "mcp_tools":
      return { key: "tools", section: "tools" };
    case "messages":
      return undefined;
  }
}

/**
 * parameterTarget is the setting behind one parameter a request sends, keyed
 * as the request's sources are, or undefined for one only a model row sets.
 */
export function parameterTarget(key: string): EditTarget | undefined {
  if (key === "model" || key === "preserve_thinking") return { key, section: "model" };
  if (key.startsWith("sampling.")) {
    const name = key.slice("sampling.".length);
    const section = name === "max_output" || name === "reasoning_effort" ? "model" : "sampling";
    return { key, section };
  }
  return undefined;
}

/** segmentColors are the parts' colours, from the theme's chart tokens. */
export const segmentColors: Record<SegmentKind, string> = {
  base: "bg-chart-1",
  context_files: "bg-chart-2",
  instructions: "bg-chart-3",
  builtin_tools: "bg-chart-5",
  mcp_tools: "bg-chart-4",
  messages: "bg-primary/40",
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
 * sections or context files, the tools or one group of them, the messages,
 * or the parameters.
 */
export type View =
  | "system"
  | `section:${"base" | "context_files" | "instructions"}`
  | `file:${string}`
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
      return "messages";
    default:
      return `section:${kind}`;
  }
}
