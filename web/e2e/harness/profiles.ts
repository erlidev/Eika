/**
 * Profiles, a session's configuration, and the context view, as the mock
 * harness serves them: the layer resolution of internal/server's
 * configuration.go and the request assembly of internal/agent, over the
 * mock's world, close enough that the UI cannot tell them apart.
 */

import type {
  ConfigLayer,
  Configuration,
  ContextSection,
  Message,
  Model,
  ModelContext,
  ModelRequest,
  Profile,
  Profiles,
  ProfileSettings,
  Sampling,
  Session,
  SessionConfiguration,
  ToolSchema,
} from "../../src/api/types.ts";
import { fixedNow, pathOf } from "./world.ts";
import type { World } from "./world.ts";

/** StoredProfile is a profile as the world keeps it, without what it inherits. */
export type StoredProfile = Omit<Profile, "inherited">;

/** RecordedRequest is one model call a run made, and what it sent. */
export type RecordedRequest = { record: ModelRequest; context: ModelContext };

/** builtinPrompts are internal/agent's WorkspacePrompt and ChatPrompt. */
export const builtinPrompts = {
  workspace: `You are Eika, a coding agent working inside a sandboxed workspace.

Rules:
- Every file and command action goes through your tools. The workspace root is your working directory and paths are relative to it.
- Run the project's own build and test commands to check your work.
- Work in small, complete steps: change the code, run the tests, report what happened.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`,
  chat: `You are Eika, an assistant in a chat with the user.

Rules:
- This chat has no workspace: there are no files to read or change and no commands to run. Use only the tools you are given; there may be none.
- When you use the web, say where an answer came from and link the pages you relied on.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`,
};

/** noSettings sets nothing. */
export function noSettings(): ProfileSettings {
  return {
    workspace_prompt: null,
    chat_prompt: null,
    instructions: null,
    context_files: null,
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
export function estimate(text: string): number {
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
  list_agents: { type: "object", properties: {}, additionalProperties: false },
  spawn_agent: {
    type: "object",
    properties: {
      name: { type: "string", description: "A short name for the child. It names its branch." },
      task: { type: "string", description: "The whole task, as the child's first message." },
      model: {
        type: "string",
        description: "The model the child runs on. Empty uses the default.",
      },
      wait: { type: "boolean", description: "Wait for the child to finish. Defaults to true." },
    },
    required: ["name", "task"],
    additionalProperties: false,
  },
  wait_agents: {
    type: "object",
    properties: {
      ids: {
        type: "array",
        items: { type: "string" },
        description: "The agent ids spawn_agent returned.",
      },
    },
    required: ["ids"],
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
export function toolSchemaOf(w: World, name: string): unknown {
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
export function defaultProfileOf(w: World): StoredProfile {
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
    workspace_prompt: "default",
    chat_prompt: "default",
    instructions: "default",
    context_files: "default",
    preserve_thinking: "default",
    tools: "default",
  };
  const config: Configuration = {
    profile_id: profile.id,
    profile_name: profile.name,
    model_id: "",
    model: "",
    workspace_prompt: builtinPrompts.workspace,
    chat_prompt: builtinPrompts.chat,
    instructions: "",
    context_files: true,
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
    if (s.workspace_prompt !== null)
      [config.workspace_prompt, sources.workspace_prompt] = [s.workspace_prompt, l.name];
    if (s.chat_prompt !== null) [config.chat_prompt, sources.chat_prompt] = [s.chat_prompt, l.name];
    if (s.instructions !== null)
      [config.instructions, sources.instructions] = [s.instructions, l.name];
    if (s.context_files !== null)
      [config.context_files, sources.context_files] = [s.context_files, l.name];
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
 * sessionConfiguration is GET /api/sessions/{id}/configuration. A draft
 * changes only what the overrides fall through to.
 */
export function sessionConfiguration(
  w: World,
  session: Session,
  draft: ConfigurationDraft = {},
): SessionConfiguration {
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
  const { config } = resolve(w, profile, source, layers);
  const chat = session.workspace_id === undefined;
  session.tools = w.tools
    .filter((t) => !(chat && t.needs_workspace) && chosen(config.tools, t.name, t.server))
    .map((t) => t.name);
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
    workspace_prompt: text(body.workspace_prompt),
    chat_prompt: text(body.chat_prompt),
    instructions: text(body.instructions),
    context_files: typeof body.context_files === "boolean" ? body.context_files : null,
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
  const { config, model } = resolve(w, profile, source, layers);
  const chat = session.workspace_id === undefined;
  const sections: ContextSection[] = [];
  const base = (chat ? config.chat_prompt : config.workspace_prompt).trim();
  if (base !== "") sections.push(section("base", base));
  const agents =
    session.workspace_id === undefined ? undefined : w.files[session.workspace_id]?.["AGENTS.md"];
  // As the harness does: a stopped workspace's files cannot be read.
  const ws = w.workspaces.find((x) => x.id === session.workspace_id);
  const unread =
    !chat && config.context_files && ws !== undefined && ws.state !== "running"
      ? `workspace ${ws.id} is ${ws.state}, not running`
      : undefined;
  if (!chat && config.context_files && unread === undefined && agents !== undefined) {
    const files = [{ path: "AGENTS.md", text: agents, tokens: estimate(agents) }];
    sections.push({
      ...section("context_files", `# Project context\n\n## AGENTS.md\n\n${agents}`),
      files,
    });
  }
  if (config.instructions.trim() !== "")
    sections.push(section("instructions", config.instructions.trim()));
  const tools: ToolSchema[] = w.tools
    .filter((t) => session.tools.includes(t.name))
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
  if (model) sources.thinking_switch = "model";
  const last = (w.requests[session.id] ?? []).findLast((r) => r.record.input_tokens > 0);
  const sizes = messages.map((m) => estimate(JSON.stringify(m)));
  return {
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
    },
    sources,
    ...(config.dropped_effort === undefined ? {} : { dropped_effort: config.dropped_effort }),
    ...(unread === undefined ? {} : { context_files_unread: unread }),
    ...(last ? { calibration: calibration(last) } : {}),
    context_window: model?.context_window ?? 0,
  };
}

/** estimated is the estimated size of everything a request sends. */
function estimated(c: ModelContext): number {
  return (
    c.message_tokens +
    (c.sections ?? []).reduce((n, s) => n + s.tokens, 0) +
    (c.tools ?? []).reduce((n, t) => n + t.tokens, 0)
  );
}

/** calibration is a recorded call's measurement beside its estimate. */
function calibration(r: RecordedRequest): NonNullable<ModelContext["calibration"]> {
  return {
    request_id: r.record.id,
    input_tokens: r.record.input_tokens,
    estimated_tokens: estimated(r.context),
  };
}

/**
 * record keeps a model call a run made: what it sent and what the endpoint
 * measured. The mock measures a sixth more than the estimate, as an endpoint
 * that counts special tokens would.
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
): RecordedRequest {
  const inputTokens = Math.round(estimated(input.context) * 1.16);
  const model = w.models.find((m) => m.model === input.context.parameters.model);
  const recorded: RecordedRequest = {
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
      input_tokens: inputTokens,
      output_tokens: input.outputTokens,
      total_tokens: inputTokens + input.outputTokens,
      created_at: input.at ?? fixedNow,
    },
    context: input.context,
  };
  (w.requests[session.id] ??= []).push(recorded);
  return recorded;
}

/** recordedContext is GET /api/sessions/{id}/requests/{request_id}. */
export function recordedContext(r: RecordedRequest): ModelContext {
  return { ...r.context, request: r.record, calibration: calibration(r) };
}
