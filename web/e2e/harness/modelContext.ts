import type { Session } from "./world.ts";
/**
 * Profiles, a session's configuration, and the context view, as the mock
 * harness serves them: the layer resolution of internal/server's
 * configuration.go and the request assembly of internal/agent, over the
 * mock's world, close enough that the UI cannot tell them apart.
 */

import type {
  Breakdown,
  ContextSection,
  Message,
  Model,
  ModelContext,
  ModelCall,
  PredictedSize,
  Sampling,
  ToolSchema,
} from "../../src/api/types.ts";
import { classView, fixedNow, pathOf, windowOf } from "./world.ts";
import type { World } from "./world.ts";
import { builtinAgents } from "./agents.ts";

/** RecordedCall holds a model call and its context. */
export type RecordedCall = { record: ModelCall; context: ModelContext };

/** estimate is internal/agent's EstimateTokens: four bytes to a token. */
function estimate(text: string): number {
  return Math.ceil(new TextEncoder().encode(text).length / 4);
}

/** builtinSchemas are the parameter schemas of internal/tool/builtin's tools. */
const builtinSchemas: Record<string, unknown> = {
  ask_user: {
    type: "object",
    properties: {
      question: { type: "string", description: "The question, as one sentence." },
      options: {
        type: "array",
        items: { type: "string" },
        description: "The answers to choose from. Omit for an open question.",
      },
      allow_free_text: {
        type: "boolean",
        description:
          "Accept an answer outside the options. Defaults to false when options are given.",
      },
    },
    required: ["question"],
    additionalProperties: false,
  },
  bash: {
    type: "object",
    properties: {
      command: { type: "string", description: "The command line to run with sh -c." },
      timeout: {
        type: "integer",
        description: "Seconds to allow the command. Defaults to 120, maximum 600.",
      },
    },
    required: ["command"],
    additionalProperties: false,
  },
  file_read: {
    type: "object",
    properties: { path: { type: "string" } },
    required: ["path"],
    additionalProperties: false,
  },
  file_write: {
    type: "object",
    properties: { path: { type: "string" }, content: { type: "string" } },
    required: ["path", "content"],
    additionalProperties: false,
  },
  file_edit: {
    type: "object",
    properties: {
      path: { type: "string" },
      old_text: { type: "string" },
      new_text: { type: "string" },
    },
    required: ["path", "old_text", "new_text"],
    additionalProperties: false,
  },
  web_fetch: {
    type: "object",
    properties: {
      url: { type: "string", description: "Absolute http(s) URL." },
      section: { type: "string", description: "Return only this section of the page." },
      format: {
        type: "string",
        enum: ["markdown", "text", "raw"],
        description: "markdown (default), text (markup stripped), or raw.",
      },
    },
    required: ["url"],
    additionalProperties: false,
  },
  web_search: {
    type: "object",
    properties: {
      query: { type: "string", description: "Search query." },
      source: {
        type: "string",
        enum: ["web", "wikipedia", "arxiv", "github_code", "github_repos", "github_issues"],
        description: "Where to search. Defaults to web.",
      },
      count: {
        type: "integer",
        minimum: 1,
        maximum: 25,
        description: "Results to return, 1-25. Defaults to 10.",
      },
    },
    required: ["query"],
    additionalProperties: false,
  },
  mcp_list_resources: { type: "object", properties: {}, additionalProperties: false },
  mcp_read_resource: {
    type: "object",
    properties: { uri: { type: "string", description: "The resource's URI." } },
    required: ["uri"],
    additionalProperties: false,
  },
};

/** toolTokens is internal/agent's ToolTokens: the estimate of the definition as JSON. */
export function toolTokens(name: string, description: string, schema: unknown): number {
  return estimate(JSON.stringify({ name, description, schema }));
}

/** builtinToolTokens sizes a built-in tool, whose schema is fixed. */
export function builtinToolTokens(name: string, description: string): number {
  return toolTokens(name, description, builtinSchemas[name] ?? { type: "object", properties: {} });
}

/** agentModel selects the chat model assigned to the session's agent class. */
export function agentModel(w: World, session: Session): Model | undefined {
  const builtin = builtinAgents.find((d) => d.name === session.agent);
  if (!builtin) return undefined;
  const cls = w.agentOverrides[builtin.name]?.class ?? builtin.class;
  const selected = classView(w).classes.find((c) => c.class === cls)?.model;
  return w.models.find((m) => m.name === selected && m.kind === "chat");
}

export function knownAgent(session: Session): boolean {
  return builtinAgents.some((d) => d.name === session.agent);
}

/** syncSession sets the tools allowed by the agent definition. */
export function syncSession(w: World, session: Session): void {
  const builtin = builtinAgents.find((d) => d.name === session.agent);
  session.tools = (builtin?.tools ?? [])
    .filter((name) => {
      const tool = w.tools.find((candidate) => candidate.name === name);
      return session.computer_id !== undefined || tool?.needs_computer !== true;
    })
    .toSorted();
}

/** section is a system prompt section carrying text. */
function section(kind: ContextSection["kind"], text: string): ContextSection {
  return { kind, text, tokens: estimate(text) };
}

/**
 * previewContext is GET /api/sessions/{id}/context: the request the next run
 * of the session would send, as internal/agent assembles it.
 */
export function previewContext(w: World, session: Session, requested: string): ModelContext {
  const builtin = builtinAgents.find((d) => d.name === session.agent);
  if (!builtin || requested !== "") throw new Error("invalid agent configuration");
  const model = agentModel(w, session);
  const sampling: Sampling = {};
  if (model) {
    if (model.max_output > 0) sampling.max_output = model.max_output;
    if (model.reasoning_effort) sampling.reasoning_effort = model.reasoning_effort;
  }
  const config = {
    chat_prompt: w.agentOverrides[builtin.name]?.prompt ?? builtin.prompt,
    preserve_thinking: model?.preserve_thinking ?? false,
    sampling,
  };
  const parameterSources: Record<string, string> = {
    model: "class",
    preserve_thinking: model ? "model" : "default",
  };
  for (const key of Object.keys(sampling)) parameterSources[`sampling.${key}`] = "model";
  const sections: ContextSection[] = [];
  const base = config.chat_prompt.trim();
  if (base !== "") sections.push(section("base", base));
  syncSession(w, session);
  const tools: ToolSchema[] = session.tools.map((name) => {
    const tool = w.tools.find((candidate) => candidate.name === name);
    const description = tool?.description ?? name;
    const schema = builtinSchemas[name] ?? { type: "object", properties: {} };
    return {
      name,
      description,
      schema,
      source: "builtin" as const,
      tokens: toolTokens(name, description, schema),
    };
  });
  const preserve = config.preserve_thinking;
  const messages: Message[] = pathOf(w, session)
    .map((e) => e.message)
    .filter((m) => m.role !== "system")
    .map((m) => {
      const sent: Message = { ...m };
      delete sent.metrics;
      if (!preserve) delete sent.reasoning;
      return sent;
    });
  const sources = parameterSources;
  if (model) {
    sources.thinking_switch = "model";
    sources.image_input = "model";
  }
  const sizes = messages.map((m) => estimate(JSON.stringify(m)));
  const request: Unsized = {
    sections,
    tools,
    messages,
    message_tokens: sizes.reduce((n, size) => n + size, 0),
    message_sizes: sizes,
    parameters: {
      model: model?.model ?? "",
      sampling: config.sampling,
      ...(model ? { thinking_switch: model.thinking_switch } : {}),
      preserve_thinking: preserve,
      image_input: model?.image_input ?? false,
    },
    sources,
    context_window: model ? windowOf(w, model).effective : 0,
  };
  return { ...request, ...countedNext(request, countingOf(w, model)) };
}

/** Unsized is a request before it is counted. */
type Unsized = Omit<ModelContext, "breakdown" | "prediction">;

/** Counting is how an endpoint counts tokens, as the probe records it. */
type Counting = "chat" | "text" | "none";

/** countingOf is how the endpoint of a model counts tokens. */
function countingOf(w: World, model: Model | undefined): Counting {
  const provider = w.providers.find((p) => p.id === model?.provider_id);
  return provider?.capabilities.counting ?? "none";
}

/**
 * The mock endpoint's tokenizer and template: a token is 3.5 bytes, a message
 * costs 5 tokens of markup, a request 16, and the template's wrapper around the
 * tool list 24 (which the first tool carries). An endpoint that tokenises
 * nothing is estimated at four bytes to a token, which is wrong by a little,
 * as the real estimate is.
 */
const template = { request: 16, message: 5, toolsWrapper: 24, toolBoundary: 3 };

function bytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

function tokenize(text: string): number {
  return text === "" ? 0 : Math.ceil(bytes(text) / 3.5);
}

/** textOf is the text a message sends: its content and the name and arguments of its tool calls. */
function textOf(m: Message): string[] {
  return [
    m.content ?? "",
    ...(m.tool_calls ?? []).flatMap((c) => [c.name, JSON.stringify(c.arguments)]),
  ];
}

/** Parts are the sizes of a request's parts as an endpoint of some kind counts them. */
type Parts = Pick<Breakdown, "sections" | "tools" | "messages" | "parts">;

/**
 * sizeParts counts the parts of a request as internal/agent's MeasureParts
 * does: text exactly where the endpoint tokenises it, the tools by rendering
 * where it renders requests and as JSON text where it does not, and everything
 * estimated, and marked so, where it tokenises nothing.
 */
function sizeParts(c: Unsized, counting: Counting): Parts {
  const exact = counting !== "none";
  const count = (text: string) => (exact ? tokenize(text) : Math.ceil(bytes(text) / 4));
  const approximate = exact ? {} : { approximate: true as const };
  const sections = (c.sections ?? []).map((s) => ({
    kind: s.kind,
    tokens: count(s.text),
    ...approximate,
  }));
  const tools = (c.tools ?? []).map((t, i) => {
    const text = count(
      JSON.stringify({ name: t.name, description: t.description, schema: t.schema }),
    );
    return counting === "chat"
      ? { name: t.name, tokens: text + (i === 0 ? template.toolsWrapper : template.toolBoundary) }
      : { name: t.name, tokens: text, approximate: true as const };
  });
  const messages = (c.messages ?? []).map((m) => ({
    tokens: textOf(m).reduce((n, text) => n + count(text), 0),
    ...(m.reasoning ? { reasoning_tokens: count(m.reasoning) } : {}),
    ...approximate,
  }));
  const parts =
    sections.reduce((n, s) => n + s.tokens, 0) +
    tools.reduce((n, t) => n + t.tokens, 0) +
    messages.reduce((n, m) => n + m.tokens + (m.reasoning_tokens ?? 0), 0);
  return { sections, tools, messages, parts };
}

/** promptOf is what the mock endpoint counts a request as: every part, tokenised, and the template. */
function promptOf(c: Unsized): number {
  const parts = sizeParts(c, "chat");
  return parts.parts + template.request + template.message * (c.messages ?? []).length;
}

/** breakdownOf states a remainder against a prompt count, or leaves it null when there is none. */
function breakdownOf(
  parts: Parts,
  prompt: number | null,
  source: "measured" | "predicted",
): Breakdown {
  return {
    ...parts,
    prompt_tokens: prompt,
    ...(prompt === null ? {} : { prompt_source: source }),
    remainder: prompt === null ? null : prompt - parts.parts,
  };
}

/**
 * countedNext counts the next request: its parts, and its prediction by the
 * best method the endpoint offers. The remainder is stated against the
 * prediction only when that is exact.
 */
function countedNext(
  c: Unsized,
  counting: Counting,
): { breakdown: Breakdown; prediction: PredictedSize } {
  const parts = sizeParts(c, counting);
  const full = promptOf(c);
  const prediction: PredictedSize =
    counting === "chat"
      ? { tokens: full, method: "exact" }
      : counting === "text"
        ? { tokens: full, method: "anchored" }
        : { tokens: parts.parts, method: "estimated" };
  return {
    breakdown: breakdownOf(parts, prediction.method === "exact" ? full : null, "predicted"),
    prediction,
  };
}

/**
 * record keeps a model call a run made: what it sent and what the endpoint
 * measured. The mock endpoint measures what its tokenizer and template make of
 * the request, and states how much of it came from its cache: the whole of
 * the session's last prompt, which is the prefix this one extends.
 */
export function record(
  w: World,
  session: Session,
  input: {
    id: string;
    runId: string;
    context: ModelContext;
    outputTokens: number;
    /** entryId is where the call's conversation ended; the session's head when absent. */
    entryId?: string;
    at?: string;
  },
): RecordedCall {
  const model = w.models.find((m) => m.model === input.context.parameters.model);
  const inputTokens = promptOf(input.context);
  const predicted = countedNext(input.context, countingOf(w, model)).prediction;
  const previous = (w.calls[session.id] ?? []).at(-1)?.record.prompt_tokens ?? 0;
  const endpoint = w.providers.find((p) => p.id === model?.provider_id)?.name;
  const recorded: RecordedCall = {
    record: {
      id: input.id,
      session_id: session.id,
      run_id: input.runId,
      ...((input.entryId ?? session.head_entry_id) === undefined
        ? {}
        : { entry_id: input.entryId ?? session.head_entry_id }),
      ...(model ? { model_id: model.id } : {}),
      model: model?.name ?? input.context.parameters.model,
      message_tokens: input.context.message_tokens,
      prompt_tokens: inputTokens,
      completion_tokens: input.outputTokens,
      total_tokens: inputTokens + input.outputTokens,
      cached_tokens: Math.min(previous, inputTokens),
      prefilled_tokens: inputTokens - Math.min(previous, inputTokens),
      reasoning_tokens: null,
      predicted_prompt_tokens: predicted.tokens,
      prediction: predicted.method,
      drift: predicted.tokens - inputTokens,
      context_window: model ? windowOf(w, model).effective : null,
      ...(endpoint === undefined ? {} : { endpoint }),
      queue_ms: null,
      slot: null,
      prefill_ms: null,
      decode_ms: null,
      created_at: input.at ?? fixedNow,
    },
    context: input.context,
  };
  (w.calls[session.id] ??= []).push(recorded);
  return recorded;
}

/**
 * recordedContext is GET /api/sessions/{id}/calls/{call_id}: what the call
 * sent, its parts counted as the endpoint counts them now, against what the
 * endpoint measured then.
 */
export function recordedContext(w: World, r: RecordedCall): ModelContext {
  const model = w.models.find((m) => m.model === r.context.parameters.model);
  const sent: ModelContext = { ...r.context };
  delete sent.prediction;
  return {
    ...sent,
    call: r.record,
    breakdown: breakdownOf(
      sizeParts(r.context, countingOf(w, model)),
      r.record.prompt_tokens,
      "measured",
    ),
  };
}
