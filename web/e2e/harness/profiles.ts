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
  Session,
  ToolSchema,
} from "../../src/api/types.ts";
import type {
  ConfigLayer,
  Configuration,
  Profile,
  Profiles,
  ProfileSettings,
  SessionConfiguration,
} from "./legacyProfiles.ts";
import { classView, fixedNow, pathOf, windowOf } from "./world.ts";
import type { World } from "./world.ts";
import { builtinAgents } from "./agents.ts";

/** StoredProfile is a profile as the world keeps it, without what it inherits. */
export type StoredProfile = Omit<Profile, "inherited">;

/** RecordedCall is one model call a run made, and what it sent. */
export type RecordedCall = { record: ModelCall; context: ModelContext };

/** builtinPrompts is internal/agent's ChatPrompt. */
const builtinPrompts = {
  chat: `You are Eika, an assistant in a chat with the user.

Rules:
- Use only the tools you are given; there may be none. Do not offer to read files or run commands unless you have tools for them.
- When you use files or commands, they go through your tools, and paths are relative to your working directory.
- When you use the web, say where an answer came from and link the pages you relied on.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`,
};

/** noSettings sets nothing. */
export function noSettings(): ProfileSettings {
  return {
    chat_prompt: null,
    instructions: null,
    preserve_thinking: null,
    sampling: {},
  };
}

/** defaultProfile is the profile the harness's migration creates. */
export function defaultProfile(): StoredProfile {
  return {
    id: "prof-default",
    name: "Default",
    description: "",
    ...noSettings(),
    tools: null,
    created_at: "2026-03-01T09:00:00.000Z",
    updated_at: "2026-03-01T09:00:00.000Z",
  };
}

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

/** toolSchemaOf is the parameter schema a request sends for a tool: an MCP server's own, or a built-in one. */
function toolSchemaOf(w: World, name: string): unknown {
  for (const d of w.mcpServers) {
    const t = (d.tools ?? []).find((x) => x.exposed_name === name);
    if (t) return t.input_schema;
  }
  return builtinSchemas[name] ?? { type: "object", properties: {} };
}

/** toolTokens is internal/agent's ToolTokens: the estimate of the definition as JSON. */
export function toolTokens(name: string, description: string, schema: unknown): number {
  return estimate(JSON.stringify({ name, description, schema }));
}

/** builtinToolTokens sizes a built-in tool, whose schema is fixed. */
export function builtinToolTokens(name: string, description: string): number {
  return toolTokens(name, description, builtinSchemas[name] ?? { type: "object", properties: {} });
}

type Layer = { name: ConfigLayer; settings: ProfileSettings; tools: string[] | null };

/** Resolved is a configuration and the model row it resolved to. */
type Resolved = { config: Configuration; model: Model | undefined };

/** defaultProfileOf is the profile a session that chose none runs with. */
function defaultProfileOf(w: World): StoredProfile {
  const id = w.settings.settings.default_profile;
  return w.profiles.find((p) => p.id === id) ?? w.profiles[0] ?? defaultProfile();
}

/** profileOf is a session's profile and whether the session chose it. */
function profileOf(w: World, session: Session): { profile: StoredProfile; source: ConfigLayer } {
  const chosen = w.profiles.find((p) => p.id === session.profile_id);
  return chosen
    ? { profile: chosen, source: "session" }
    : { profile: defaultProfileOf(w), source: "default" };
}

/** samplingKeys are the parameters a sampling layer sets. */
function samplingKeys(s: Sampling): (keyof Sampling)[] {
  return (Object.keys(s) as (keyof Sampling)[]).filter((k) => s[k] !== undefined);
}

/** resolve is the one rule: each value from the first layer that sets it, then the model row, then the defaults. */
function resolve(
  w: World,
  profile: StoredProfile,
  profileSource: ConfigLayer,
  layers: Layer[],
): Resolved {
  let model = w.models.find((m) => m.name === w.defaultModel) ?? w.models[0];
  const sources: Record<string, ConfigLayer> = {
    profile: profileSource,
    model: "default",
    chat_prompt: "default",
    instructions: "default",
    preserve_thinking: "default",
    tools: "default",
  };
  const config: Configuration = {
    profile_id: profile.id,
    profile_name: profile.name,
    model_id: "",
    model: "",
    chat_prompt: builtinPrompts.chat,
    instructions: "",
    preserve_thinking: false,
    tools: null,
    sampling: {},
    sources,
  };
  let preserve: boolean | null = null;
  for (const l of layers.toReversed()) {
    const s = l.settings;
    const named = w.models.find((m) => m.id === s.model_id);
    if (named) [model, sources.model] = [named, l.name];
    if (s.chat_prompt !== null) [config.chat_prompt, sources.chat_prompt] = [s.chat_prompt, l.name];
    if (s.instructions !== null)
      [config.instructions, sources.instructions] = [s.instructions, l.name];
    if (s.preserve_thinking !== null)
      [preserve, sources.preserve_thinking] = [s.preserve_thinking, l.name];
    if (l.tools !== null) [config.tools, sources.tools] = [l.tools, l.name];
  }
  if (preserve !== null) config.preserve_thinking = preserve;
  else if (model)
    [config.preserve_thinking, sources.preserve_thinking] = [model.preserve_thinking, "model"];
  const row: Sampling = {};
  if (model) {
    row.max_output = model.max_output;
    if (model.reasoning_effort !== undefined && model.reasoning_effort !== "") {
      row.reasoning_effort = model.reasoning_effort;
    }
  }
  const sampling: Record<string, unknown> = { ...row };
  for (const key of samplingKeys(row)) sources[`sampling.${key}`] = "model";
  for (const l of layers.toReversed()) {
    for (const key of samplingKeys(l.settings.sampling)) {
      sampling[key] = l.settings.sampling[key];
      sources[`sampling.${key}`] = l.name;
    }
  }
  const effort = sampling.reasoning_effort;
  if (
    typeof effort === "string" &&
    effort !== "" &&
    sources["sampling.reasoning_effort"] !== "model" &&
    model !== undefined &&
    !model.reasoning_efforts.includes(effort)
  ) {
    config.dropped_effort = effort;
    Reflect.deleteProperty(sampling, "reasoning_effort");
    Reflect.deleteProperty(sources, "sampling.reasoning_effort");
    if (row.reasoning_effort !== undefined) {
      sampling.reasoning_effort = row.reasoning_effort;
      sources["sampling.reasoning_effort"] = "model";
    }
  }
  config.sampling = sampling;
  if (model) [config.model_id, config.model] = [model.id, model.name];
  return { config, model };
}

/** inherited is what layer i falls through to, as internal/server words it. */
function inherited(
  w: World,
  profile: StoredProfile,
  source: ConfigLayer,
  layers: Layer[],
  i: number,
): Configuration {
  const layer = layers[i];
  if (layer === undefined) throw new Error(`no layer ${String(i)}`);
  const own: Layer = {
    name: layer.name,
    settings: {
      ...noSettings(),
      ...(layer.settings.model_id ? { model_id: layer.settings.model_id } : {}),
    },
    tools: null,
  };
  const below = layers.slice(i + 1);
  const out = resolve(w, profile, source, [own, ...below]).config;
  const under = resolve(w, profile, source, below).config;
  out.model_id = under.model_id;
  out.model = under.model;
  if (out.sources) out.sources.model = under.sources?.model ?? "default";
  return out;
}

/** profileBody is a profile on the wire, with what it inherits. */
export function profileBody(w: World, p: StoredProfile): Profile {
  const layers: Layer[] = [{ name: "profile", settings: p, tools: p.tools }];
  return { ...p, inherited: inherited(w, p, "default", layers, 0) };
}

/** profilesBody is GET /api/profiles. */
export function profilesBody(w: World): Profiles {
  return {
    profiles: w.profiles.map((p) => profileBody(w, p)),
    default: defaultProfileOf(w).id,
    prompts: builtinPrompts,
  };
}

/** sessionLayers are a session's layers, top first, with the model a request names. */
function sessionLayers(
  w: World,
  session: Session,
  requested: string,
): { layers: Layer[]; profile: StoredProfile; source: ConfigLayer } {
  const { profile, source } = profileOf(w, session);
  const layers: Layer[] = [];
  const named = w.models.find((m) => m.name === requested);
  if (named)
    layers.push({
      name: "request",
      settings: { ...noSettings(), model_id: named.id },
      tools: null,
    });
  layers.push(
    {
      name: "session",
      settings: w.overrides[session.id] ?? noSettings(),
      tools: w.toolChoices[session.id] ?? null,
    },
    { name: "profile", settings: profile, tools: profile.tools },
  );
  return { layers, profile, source };
}

/**
 * ConfigurationDraft is the profile and model a session's editor has chosen
 * and not saved, as the profile_id and model_id query parameters give them.
 */
export type ConfigurationDraft = { profileId?: string; modelId?: string };

/** inheritedProfile is GET /api/profiles/inherited: a profile choosing modelId ("" for none). */
export function inheritedProfile(w: World, modelId: string): Configuration {
  const settings: ProfileSettings = { ...noSettings(), ...(modelId ? { model_id: modelId } : {}) };
  const layers: Layer[] = [{ name: "profile", settings, tools: null }];
  return inherited(w, defaultProfileOf(w), "default", layers, 0);
}

/**
 * standInPrompt is what a built-in agent's session sends as its base prompt
 * where no override is set. The real prompts are Markdown the product edits
 * often, and their size sets the status bar's "next ~N estimated" in every
 * baseline; /api/agents still serves the real text.
 */
const standInPrompt = "Stand-in prompt for a built-in agent. ".repeat(40);

/** resolveAgent uses the effective definition and its model class, without legacy layers. */
function resolveAgent(w: World, session: Session): Resolved {
  const builtin = builtinAgents.find((d) => d.name === session.agent);
  const override = w.agentOverrides[session.agent ?? ""];
  const cls = override?.class ?? builtin?.class;
  const selected = classView(w).classes.find((c) => c.class === cls)?.model;
  const model = w.models.find((m) => m.name === selected && m.kind === "chat");
  const profile = { ...defaultProfile(), id: "", name: "" };
  const result = resolve(
    { ...w, models: model ? [model] : [], defaultModel: model?.name ?? "" },
    profile,
    "agent",
    [
      {
        name: "agent",
        settings: {
          ...noSettings(),
          chat_prompt: override?.prompt ?? (builtin ? standInPrompt : ""),
        },
        tools: builtin?.tools ?? [],
      },
    ],
  );
  if (result.config.sources) result.config.sources.model = "class";
  return result;
}

/**
 * sessionConfiguration is GET /api/sessions/{id}/configuration. A draft
 * changes only what the overrides fall through to.
 */
export function sessionConfiguration(
  w: World,
  session: Session,
  draft: ConfigurationDraft = {},
): SessionConfiguration {
  if (session.agent) {
    const { config } = resolveAgent(w, session);
    return {
      session_id: session.id,
      overrides: noSettings(),
      tools: null,
      resolved: config,
      inherited: config,
    };
  }
  const { layers, profile, source } = sessionLayers(w, session, "");
  const drafted = { ...session };
  if (draft.profileId === "") Reflect.deleteProperty(drafted, "profile_id");
  else if (draft.profileId !== undefined) drafted.profile_id = draft.profileId;
  const under = sessionLayers(w, drafted, "");
  const top = under.layers[0];
  if (top && draft.modelId !== undefined) {
    top.settings = { ...top.settings };
    if (draft.modelId) top.settings.model_id = draft.modelId;
    else delete top.settings.model_id;
  }
  return {
    session_id: session.id,
    ...(session.profile_id === undefined ? {} : { profile_id: session.profile_id }),
    overrides: w.overrides[session.id] ?? noSettings(),
    tools: w.toolChoices[session.id] ?? null,
    resolved: resolve(w, profile, source, layers).config,
    inherited: inherited(w, under.profile, under.source, under.layers, 0),
  };
}

/** chosen reports whether a tool choice takes a tool, by name or by its server. */
function chosen(choice: string[] | null, name: string, server: string | undefined): boolean {
  if (choice === null) return true;
  return choice.includes(name) || (server !== undefined && choice.includes(`mcp__${server}__*`));
}

/**
 * syncSession recomputes what a session reports of its configuration: the
 * tools its next run offers and whether it overrides its profile.
 */
export function syncSession(w: World, session: Session): void {
  const { layers, profile, source } = sessionLayers(w, session, "");
  const { config } = session.agent ? resolveAgent(w, session) : resolve(w, profile, source, layers);
  const chat = session.computer_id === undefined;
  session.tools = w.tools
    .filter((t) => !(chat && t.needs_computer) && chosen(config.tools, t.name, t.server))
    .map((t) => t.name)
    .sort();
  if (session.agent) {
    session.overridden = false;
    return;
  }
  const overrides = w.overrides[session.id];
  session.overridden =
    w.toolChoices[session.id] !== undefined ||
    (overrides !== undefined && JSON.stringify(overrides) !== JSON.stringify(noSettings()));
}

/** isSettings reports whether a body has the shape of profile settings. */
export function settingsOf(body: Record<string, unknown>): ProfileSettings {
  const text = (v: unknown) => (typeof v === "string" ? v : null);
  const sampling =
    typeof body.sampling === "object" && body.sampling !== null ? (body.sampling as Sampling) : {};
  return {
    ...(typeof body.model_id === "string" && body.model_id !== ""
      ? { model_id: body.model_id }
      : {}),
    chat_prompt: text(body.chat_prompt),
    instructions: text(body.instructions),
    preserve_thinking: typeof body.preserve_thinking === "boolean" ? body.preserve_thinking : null,
    sampling,
  };
}

/** samplingProblem is the harness's refusal of a sampling parameter out of range, if any. */
export function samplingProblem(s: Sampling): string | undefined {
  const within = (v: number | undefined, lo: number, hi: number) =>
    v === undefined || (v >= lo && v <= hi);
  if (!within(s.temperature, 0, 2)) return "temperature must be from 0 to 2";
  if (!within(s.top_p, 0, 1)) return "top_p must be from 0 to 1";
  if (s.top_k !== undefined && s.top_k < 1) return "top_k must be at least 1";
  if (!within(s.min_p, 0, 1)) return "min_p must be from 0 to 1";
  if (!within(s.frequency_penalty, -2, 2)) return "frequency_penalty must be from -2 to 2";
  if (!within(s.presence_penalty, -2, 2)) return "presence_penalty must be from -2 to 2";
  if (s.max_output !== undefined && s.max_output < 1) return "max_output must be at least 1 token";
  return undefined;
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
  const { layers, profile, source } = sessionLayers(w, session, requested);
  const { config, model } = session.agent
    ? resolveAgent(w, session)
    : resolve(w, profile, source, layers);
  const sections: ContextSection[] = [];
  const base = config.chat_prompt.trim();
  if (base !== "") sections.push(section("base", base));
  if (config.instructions.trim() !== "")
    sections.push(section("instructions", config.instructions.trim()));
  syncSession(w, session);
  const tools: ToolSchema[] = w.tools
    .filter((t) => session.tools.includes(t.name))
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((t) => {
      const schema = toolSchemaOf(w, t.name);
      return {
        name: t.name,
        description: t.description,
        schema,
        source: t.name.startsWith("mcp_") ? ("mcp" as const) : ("builtin" as const),
        tokens: toolTokens(t.name, t.description, schema),
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
  const sources: Record<string, ConfigLayer> = {
    model: config.sources?.model ?? "default",
    preserve_thinking: config.sources?.preserve_thinking ?? "default",
  };
  for (const [key, layer] of Object.entries(config.sources ?? {})) {
    if (key.startsWith("sampling.")) sources[key] = layer;
  }
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
    ...(config.dropped_effort === undefined ? {} : { dropped_effort: config.dropped_effort }),
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
