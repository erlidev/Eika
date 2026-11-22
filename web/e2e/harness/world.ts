/**
 * The in-memory world a mock harness serves: every table the HTTP API reads,
 * plus the session trees a replay walks. A scenario is a function that builds
 * one; routes then read and mutate it, so a flow driven through the UI (create
 * a computer, send a message) changes what the next request sees.
 */

import type { EikaEvent } from "../../src/api/events.ts";
import {
  builtinToolTokens,
  previewContext,
  record,
  syncSession,
  toolTokens,
} from "./modelContext.ts";
import type { RecordedCall } from "./modelContext.ts";
import type {
  AgentOverride,
  Project,
  ContentDetail,
  Elicitation,
  Entry,
  Halt,
  MCPServer,
  MCPServerDetails,
  Message,
  Model,
  ModelClass,
  ModelClassName,
  QueuedMessage,
  Withheld,
  Provider,
  ProbeFindings,
  Question,
  Run,
  Sandbox,
  SearchStatus,
  Session as WireSession,
  SettingsState,
  SystemStatus,
  Tool,
  Computer,
} from "../../src/api/types.ts";

export type Session = WireSession;
export function wireSession(session: Session): WireSession {
  return session;
}

/**
 * Reply is what the mock agent does when a message starts a run: the events
 * it streams, in order, before the turn ends. Entries are derived from them.
 */
export type ReplyStep =
  | { say: string }
  /** think streams reasoning, which the transcript shows apart from the answer. */
  | { think: string }
  | {
      tool: string;
      args: Record<string, unknown>;
      output?: string;
      result: string;
      isError?: boolean;
      details?: Record<string, unknown>;
    }
  | { ask: string; options?: string[]; allowFreeText?: boolean }
  /**
   * mcp calls a tool of an MCP server, `mcp__<server>__<tool>`, whose result
   * carries the server's content blocks. With `elicit` the server first asks
   * the user and waits; an answer other than accept makes the result say so.
   */
  | {
      mcp: { server: string; tool: string };
      args: Record<string, unknown>;
      content: ContentDetail[];
      structured?: unknown;
      isError?: boolean;
      elicit?: Pick<Elicitation, "mode" | "message" | "requested_schema" | "url">;
    }
  | { fail: string; retryable?: boolean }
  /**
   * cutOff ends the turn on an incomplete stop reason, as an endpoint that
   * ran out of output room does. What was said before it is kept.
   */
  | { cutOff: "length" | "content_filter" | "max_calls" | "max_wall_time" }
  /** hang leaves the run going: no turn.end is sent. */
  | { hang: true };

/** World is everything the mock harness knows. */
export type World = {
  /** passwordSet false shows the setup wizard's password step. */
  passwordSet: boolean;
  /** signedIn stores a token before the page loads. */
  signedIn: boolean;
  settings: SettingsState;
  system: SystemStatus;
  /** search is GET /api/search/status before settings apply: order and limits come from settings. */
  search: SearchStatus;
  providers: Provider[];
  providerKinds: string[];
  models: Model[];
  defaultModel?: string;
  computers: Computer[];
  sessions: Session[];
  /** untitled are the sessions created without a title, which their first run names. */
  untitled: string[];
  /** tools is GET /api/tools: every tool a run can offer, sorted by name. */
  tools: Tool[];
  /** entries holds each session's tree, keyed by session id, in insertion order. */
  entries: Record<string, Entry[]>;
  runs: Record<string, Run>;
  /** activeRuns maps a session to the run that is going on it. */
  activeRuns: Record<string, string>;
  questions: Question[];
  /** elicitations are what MCP servers ask during the runs that are going. */
  elicitations: Elicitation[];
  /** mcpServers are the configured MCP servers, each with all the harness knows of it. */
  mcpServers: MCPServerDetails[];
  pendingSteering: Record<string, QueuedMessage[]>;
  pendingFollowUps: Record<string, QueuedMessage[]>;
  /** replies are consumed one per run, oldest first; an empty list echoes. */
  replies: ReplyStep[][];
  /** probeModels is what "detect models" on any endpoint answers. */
  probeModels: { id: string; context_window?: number; max_output?: number }[];
  /** probeFinds is what a probe says any endpoint is, as the harness would find it. */
  probeFinds: ProbeFindings;
  /** eventsOnConnect are sent once to every event stream that opens. */
  eventsOnConnect: EikaEvent[];
  /** agentOverrides holds only user overrides of the built-in definitions. */
  agentOverrides: Record<string, AgentOverride>;
  projects: Project[];
  instructions: { about_me: string; rules: string };
  projectInstructions: Record<string, string>;
  projectInstructionVersions: Record<string, number>;
  /** withheld is GET /api/withheld: every removal, in any order; the route sorts. */
  withheld: Withheld[];
  guard: "unknown" | "up" | "down";
  /** trustedSources holds the exact hosts the user trusts. */
  trustedSources: string[];
  /** blockedHosts holds exact hosts refused by the proxy. */
  blockedHosts: string[];
  /** halt is GET /api/halt; the routes change it and a refused run reads it. */
  halt: Halt;
  /** calls are each session's recorded model calls, oldest first. */
  calls: Record<string, RecordedCall[]>;
};

/** fixedNow is the clock every screenshot is taken at. */
export const fixedNow = "2026-03-14T15:00:00.000Z";

/** minutesAgo formats a time before fixedNow, for seeding timestamps. */
export function minutesAgo(minutes: number): string {
  return new Date(Date.parse(fixedNow) - minutes * 60_000).toISOString();
}

/** emptyWorld is a harness that is set up and signed in, with nothing configured. */
export function emptyWorld(): World {
  const world: World = {
    passwordSet: true,
    signedIn: true,
    settings: {
      settings: { setup_complete: true },
      // The routes resolve these from the model_classes setting when they serve it.
      classes: [],
      titles_model: "",
      defaults: {
        screen_required: false,
        screen_threshold: 0.9,
        sandbox_image: "eika-sandbox:latest",
        computer_idle_minutes: 15,
        compact_idle_fill: 60,
        compact_idle_minutes: 30,
        subagent_max_running: 4,
        maintenance_idle_seconds: 120,
        aging_seconds: 300,
        preempt_running: false,
        sandbox_limits: { cpus: 0, memory_mb: 0, pids: 4096 },
        sandbox_egress: { mode: "public", allow: [...defaultAllowlist] },
        // As the harness documents them in docs/api/http.md.
        search_order: ["searxng", "exa", "tavily", "brave", "marginalia"],
        search_limits: {
          searxng: {},
          exa: { month: 900 },
          tavily: { month: 1000 },
          brave: { month: 2000 },
          marginalia: { day: 100 },
          github: {},
        },
        compaction: {
          auto: true,
          reserve_tokens: 16384,
          keep_recent_tokens: 20000,
          // The start of each built-in prompt; the harness sends them whole.
          prompts: {
            summary:
              "The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.",
            update:
              "The conversation above begins with an existing summary of earlier work, in <summary> tags; the messages after it are NEW. Update the existing summary with the new messages.",
            turn_prefix:
              "The most recent user request above began a turn that is too large to keep whole. Reply with a summary of the prefix alone, as context for the retained suffix.",
          },
        },
      },
    },
    search: searchStatus(),
    system: {
      docker: { reachable: true },
      sandbox_image: { name: "eika-sandbox:latest", present: true },
      sandbox: { cpus: 8, memory_bytes: 16 * 2 ** 30, egress_control: true },
      providers: 0,
      models: 0,
    },
    providers: [],
    providerKinds: ["openai", "anthropic"],
    models: [],
    computers: [],
    sessions: [],
    untitled: [],
    tools: toolCatalog(),
    entries: {},
    runs: {},
    activeRuns: {},
    questions: [],
    elicitations: [],
    mcpServers: [],
    pendingSteering: {},
    pendingFollowUps: {},
    replies: [],
    probeModels: [
      { id: "gpt-5", context_window: 400000, max_output: 128000 },
      { id: "gpt-5-mini", context_window: 400000, max_output: 128000 },
    ],
    probeFinds: {
      backend: "openai",
      slots: 8,
      kv: "unknown",
      api: "chat",
      capabilities: { counting: "none", cache_metrics: "unknown", slot_hints: false },
    },
    eventsOnConnect: [],
    agentOverrides: {},
    projects: [],
    instructions: { about_me: "", rules: "" },
    projectInstructions: {},
    projectInstructionVersions: {},
    withheld: [],
    guard: "up",
    trustedSources: [],
    blockedHosts: [],
    halt: { halted: false },
    calls: {},
  };
  // The harness makes the home session when it starts, so every world has it.
  const home = homeSession();
  world.sessions.push(home);
  world.entries[home.id] = [];
  return world;
}

/** homeSession is the local owner's persistent conversation, empty, as the harness makes it. */
function homeSession(): Session {
  return {
    id: "home-main",
    title: "Home",
    kind: "home",
    owner: "local",
    name: "home",
    agent: "eika",
    state: "idle",
    last_active_at: minutesAgo(5),
    tools: ["web_search"],
    pinned: false,
    archived: false,
    created_at: minutesAgo(90),
    updated_at: minutesAgo(5),
  };
}

/** defaultAllowlist is the allowlist the harness suggests (egress.DefaultAllowlist). */
const defaultAllowlist = [
  "github.com",
  "*.github.com",
  "*.githubusercontent.com",
  "gitlab.com",
  "registry.npmjs.org",
  "pypi.org",
  "files.pythonhosted.org",
  "proxy.golang.org",
  "sum.golang.org",
  "crates.io",
  "*.crates.io",
];

/** defaultSandbox is what a computer gets from the harness's own defaults. */
export function defaultSandbox(): Sandbox {
  return {
    limits: { cpus: 0, memory_mb: 0, pids: 4096 },
    egress: { mode: "public", allow: [...defaultAllowlist] },
  };
}

/** searchStatus is a deployment with SearXNG up and no search keys stored. */
function searchStatus(): SearchStatus {
  const usage = (dayUsed = 0, monthUsed = 0) => ({
    day: "2026-03-14",
    day_used: dayUsed,
    month: "2026-03",
    month_used: monthUsed,
  });
  const web = (name: string, key?: string) => ({
    name,
    web: true,
    ...(key ? { key, key_required: true } : {}),
    key_set: false,
    bucket: name,
    state: "ready",
    usage: usage(),
    limit: {},
  });
  const source = (name: string, bucket?: string) => ({
    name,
    web: false,
    key_set: false,
    ...(bucket ? { bucket } : {}),
    state: "ready",
    usage: usage(),
    limit: {},
  });
  return {
    order: [],
    backends: [
      { ...web("searxng"), usage: usage(12, 340), probe: "up" },
      web("exa", "exa"),
      web("tavily", "tavily"),
      web("brave", "brave"),
      { ...web("marginalia"), usage: usage(3, 41) },
      source("wikipedia"),
      source("arxiv"),
      { ...source("github_code", "github"), key: "github", key_required: true },
      source("github_repos", "github"),
      source("github_issues", "github"),
    ],
    keys: ["exa", "tavily", "brave", "github"].map((name) => ({ name, set: false })),
    cached_searches: 128,
    cached_pages: 37,
    searxng_url: "http://searxng:8080",
  };
}

/** toolCatalog is the harness's built-in tools, as GET /api/tools lists them. */
function toolCatalog(): Tool[] {
  const tool = (name: string, description: string, needsComputer = true): Tool => ({
    name,
    description,
    needs_computer: needsComputer,
    tokens: builtinToolTokens(name, description),
  });
  return [
    tool("ask_user", "Ask the user a question and wait for the answer.", false),
    tool(
      "bash",
      "Run a shell command in the computer root. This is also how you explore, read, and edit files.",
    ),
    tool(
      "file_edit",
      "Replace exactly one occurrence of old_text with new_text in a UTF-8 file in the working directory. Read the file first. Both the original and result must be at most 1048576 bytes. No match or multiple matches leave the file unchanged. An empty new_text deletes the match.",
    ),
    tool(
      "file_read",
      "Read a UTF-8 text file of at most 32768 bytes in the working directory. Use bash for larger files or binary data.",
    ),
    tool(
      "file_write",
      "Create or replace a UTF-8 text file of at most 1048576 bytes in the working directory. Creates parent directories. An empty content string creates an empty file.",
    ),
    tool(
      "web_fetch",
      "Fetch a web page or file by URL and return its content as Markdown.\nUse it to read a page you already have a URL for.",
      false,
    ),
    tool(
      "web_search",
      "Search the web, Wikipedia, arXiv or GitHub. Returns titles, URLs and snippets.",
      false,
    ),
  ];
}

/** WorldBuilder adds rows with consistent ids and timestamps. */
export class WorldBuilder {
  readonly world: World;
  private seq = 0;

  constructor(world: World = emptyWorld()) {
    this.world = world;
  }

  private id(prefix: string): string {
    this.seq += 1;
    if (prefix === "ent") {
      const digits = Array.from(
        this.seq.toString(32),
        (c) => "abcdefghijklmnopqrstuvwxyz234567"[parseInt(c, 32)],
      ).join("");
      return `a${digits.padStart(19, "a")}`;
    }
    return `${prefix}-${String(this.seq)}`;
  }

  provider(input: Partial<Provider> & { name: string }): Provider {
    const row: Provider = {
      id: this.id("prov"),
      kind: "openai",
      base_url: "https://api.openai.com/v1",
      api_key_set: true,
      api_key_hint: "a1b2",
      // A hosted API, probed when it was added.
      backend: "openai",
      pool: input.name,
      slots: 8,
      kv: "unknown",
      api: "chat",
      capabilities: { counting: "none", cache_metrics: "unknown", slot_hints: false },
      overridden: [],
      probed_at: minutesAgo(30),
      created_at: minutesAgo(600),
      updated_at: minutesAgo(600),
      ...input,
    };
    this.world.providers.push(row);
    return row;
  }

  model(provider: Provider, input: Partial<Model> & { name: string }): Model {
    const row: Model = {
      id: this.id("mod"),
      provider_id: provider.id,
      kind: "chat",
      model: input.name,
      context_window: 400000,
      // The routes recompute this from the provider's probe when they serve it.
      effective_window: input.context_window ?? 400000,
      max_output: 128000,
      reasoning_efforts: [],
      thinking_switch: "reasoning_effort",
      preserve_thinking: false,
      image_input: false,
      created_at: minutesAgo(590),
      updated_at: minutesAgo(590),
      ...input,
    };
    this.world.models.push(row);
    if (row.kind === "chat") this.world.defaultModel ??= row.name;
    return row;
  }

  computer(input: Partial<Computer> & { name: string }): Computer {
    const row: Computer = {
      id: this.id("cx"),
      image: "eika-sandbox:latest",
      state: "running",
      container_id: "c0ffee",
      sandbox: defaultSandbox(),
      home: false,
      created_at: minutesAgo(400),
      updated_at: minutesAgo(30),
      ...input,
    };
    this.world.computers.unshift(row);
    return row;
  }

  session(computer: Computer, input: Partial<Session> & { title: string }): Session {
    const row: Session = {
      id: this.id("ses"),
      computer_id: computer.id,
      kind: "routine",
      agent: "eika",
      owner: "local",
      state: "idle",
      last_active_at: minutesAgo(5),
      tools: [],
      pinned: false,
      archived: false,
      created_at: minutesAgo(120),
      updated_at: minutesAgo(5),
      ...input,
    };
    syncSession(this.world, row);
    this.world.sessions.unshift(row);
    this.world.entries[row.id] ??= [];
    return row;
  }

  /** chat adds a session with no computer, offering the tools that need none. */
  chat(input: Partial<Session> & { title: string }): Session {
    const row: Session = {
      id: this.id("chat"),
      kind: "routine",
      agent: "eika",
      owner: "local",
      state: "idle",
      last_active_at: minutesAgo(5),
      tools: [],
      pinned: false,
      archived: false,
      created_at: minutesAgo(90),
      updated_at: minutesAgo(5),
      ...input,
    };
    syncSession(this.world, row);
    this.world.sessions.unshift(row);
    this.world.entries[row.id] ??= [];
    return row;
  }

  /** home changes the local owner's persistent conversation, which every world starts with. */
  home(input: Partial<Session> = {}): Session {
    const row = this.world.sessions.find((s) => s.kind === "home");
    if (row === undefined) throw new Error("the world has no home session");
    Object.assign(row, input);
    syncSession(this.world, row);
    return row;
  }

  /** subagent adds a named session under home. */
  subagent(parent: Session, input: Partial<Session> & { name: string; agent: string }): Session {
    return this.chat({
      title: input.name,
      ...input,
      kind: "subagent",
      owner: parent.owner,
      parent_session_id: parent.id,
    });
  }

  /**
   * mcpServer adds an MCP server with nothing listed yet; pass the details it
   * has on top. Its tools join GET /api/tools as the harness offers them.
   */
  mcpServer(
    server: Partial<MCPServer> & Pick<MCPServer, "name" | "kind">,
    details: Partial<Omit<MCPServerDetails, "server">> = {},
  ): MCPServerDetails {
    const row: MCPServerDetails = {
      server: {
        id: this.id("mcp"),
        url: "",
        header_names: [],
        command: "",
        args: [],
        env_names: [],
        enabled: true,
        disabled_tools: [],
        oauth_client_id: "",
        oauth_client_secret_set: false,
        state: "idle",
        computers: [],
        created_at: minutesAgo(300),
        updated_at: minutesAgo(300),
        ...server,
      },
      tools: [],
      excluded_tools: [],
      resources: [],
      resource_templates: [],
      prompts: [],
      list_errors: {},
      logs: [],
      auth: { challenged: false, authorized: false, has_refresh_token: false },
      ...details,
    };
    this.world.mcpServers.push(row);
    this.world.mcpServers.sort((a, b) => a.server.name.localeCompare(b.server.name));
    syncMCPTools(this.world);
    return row;
  }

  /**
   * call records the model call a session's run made when its path ended
   * where it ends now, as the harness keeps one per call.
   */
  call(session: Session, minutes: number, outputTokens: number): RecordedCall {
    return record(this.world, session, {
      id: this.id("req"),
      runId: this.id("run"),
      context: previewContext(this.world, session, ""),
      outputTokens,
      at: minutesAgo(minutes),
    });
  }

  /**
   * conversation appends messages to a session's head, one entry each, and
   * moves the head. Tool calls and results become their own entries, as the
   * harness stores them.
   */
  conversation(session: Session, messages: Message[]): Entry[] {
    const list = (this.world.entries[session.id] ??= []);
    const added: Entry[] = [];
    let at = 60;
    for (const message of messages) {
      const parent = session.head_entry_id;
      const entry: Entry = {
        id: this.id("ent"),
        seq: list.length + 1,
        kind: entryKind(message),
        created_at: minutesAgo(at),
        message,
        ...(session.kind === "home" && (message.role === "user" || message.role === "assistant")
          ? { channel: "web" }
          : {}),
        ...(parent === undefined ? {} : { parent_id: parent }),
      };
      at = Math.max(1, at - 3);
      list.push(entry);
      added.push(entry);
      session.head_entry_id = entry.id;
    }
    return added;
  }
}

/** entryKind is the entry kind the harness stores a message under. */
export function entryKind(message: Message): Entry["kind"] {
  switch (message.role) {
    case "":
      throw new Error("cannot record an empty provider message");
    case "user":
      return "user";
    case "system":
      return "system";
    case "tool":
      return "tool_result";
    case "assistant":
      return "assistant";
  }
}

/** pathOf walks a session's tree from its head back to the root. */
export function pathOf(world: World, session: Session): Entry[] {
  const list = world.entries[session.id] ?? [];
  const byId = new Map(list.map((e) => [e.id, e]));
  const path: Entry[] = [];
  let cursor = session.head_entry_id;
  while (cursor !== undefined) {
    const entry = byId.get(cursor);
    if (!entry) break;
    path.unshift(entry);
    cursor = entry.parent_id;
  }
  return path;
}

/**
 * windowOf is a model's windows as internal/server/window.go states them: the
 * per-request context its provider's probe read, if any, and the smaller of
 * that and the model's configured window.
 */
export function windowOf(
  world: World,
  model: Model,
): { probed: number | undefined; effective: number } {
  const stated = world.providers.find((p) => p.id === model.provider_id)?.capabilities.window;
  const probed = stated !== undefined && stated > 0 ? stated : undefined;
  return {
    probed,
    effective:
      probed !== undefined && probed < model.context_window ? probed : model.context_window,
  };
}

/** syncSystem recounts the system status from the tables. */
export function syncSystem(world: World): void {
  world.system.providers = world.providers.length;
  world.system.models = world.models.filter((m) => m.kind === "chat").length;
}

/**
 * modelClassNames is every class, in the order the settings list them. The
 * harness cannot import the UI's own list: node loads this file without the
 * UI's path aliases.
 */
export const modelClassNames: readonly ModelClassName[] = ["deep", "main", "fast", "decision"];

/** classFallback is the class that serves a class with no model, as classes.go has it. */
const classFallback: Partial<Record<ModelClassName, ModelClassName>> = {
  deep: "main",
  fast: "main",
};

/**
 * classView resolves every model class and the model that makes titles from
 * the model_classes setting, as internal/server/classes.go does: a class with
 * no model falls back (deep and fast to main, main to the default model), and
 * titles are skipped when fast falls back onto the model assigned to deep, if
 * that is on a hosted API.
 */
export function classView(world: World): Pick<SettingsState, "classes" | "titles_model"> {
  const stored = world.settings.settings.model_classes;
  const assigned: Partial<Record<string, string>> =
    typeof stored === "object" && stored !== null ? stored : {};
  const resolve = (
    asked: ModelClassName,
  ): { model: Model; via: ModelClassName | "default" } | undefined => {
    for (let c: ModelClassName | undefined = asked; c !== undefined; c = classFallback[c]) {
      const kind = c === "decision" ? "decision" : "chat";
      const model = world.models.find((m) => m.name === assigned[c] && m.kind === kind);
      if (model !== undefined) return { model, via: c };
    }
    // A chat class with no model of its own or a fallback gets the default model.
    const fallback = world.models.find((m) => m.name === world.defaultModel && m.kind === "chat");
    return asked !== "decision" && fallback !== undefined
      ? { model: fallback, via: "default" }
      : undefined;
  };
  const classes = modelClassNames.map((name): ModelClass => {
    const found = resolve(name);
    const provider = world.providers.find((p) => p.id === found?.model.provider_id);
    return {
      class: name,
      kind: name === "decision" ? "decision" : "chat",
      model: found?.model.name ?? "",
      ...(found === undefined || found.via === name ? {} : { via: found.via }),
      ...(provider === undefined ? {} : { provider_id: provider.id, pool: provider.pool }),
    };
  });
  const fast = resolve("fast");
  const deep = assigned.deep;
  const hosted =
    fast !== undefined &&
    fast.via !== "fast" &&
    fast.model.name === deep &&
    world.providers.find((p) => p.id === fast.model.provider_id)?.backend === "openai";
  return { classes, titles_model: fast === undefined || hosted ? "" : fast.model.name };
}

/**
 * syncMCPTools rebuilds GET /api/tools from the built-in tools and what each
 * enabled MCP server last listed, as the harness offers them: a stdio
 * server's tools need a computer, and a disabled tool is left out.
 */
export function syncMCPTools(world: World): void {
  const builtIn = world.tools.filter((t) => t.server === undefined && !t.name.startsWith("mcp_"));
  const offered = world.mcpServers
    .filter((d) => d.server.enabled)
    .flatMap((d) =>
      (d.tools ?? [])
        .filter((t) => t.enabled)
        .map((t) => ({
          name: t.exposed_name,
          description: t.description ?? "",
          needs_computer: d.server.kind === "stdio",
          server: d.server.name,
          tokens: toolTokens(t.exposed_name, t.description ?? "", t.input_schema),
        })),
    );
  const resources = world.mcpServers.some((d) => d.server.enabled && (d.resources ?? []).length > 0)
    ? [
        {
          name: "mcp_list_resources",
          description: "List the resources the MCP servers of this run offer.",
          needs_computer: false,
          tokens: builtinToolTokens(
            "mcp_list_resources",
            "List the resources the MCP servers of this run offer.",
          ),
        },
        {
          name: "mcp_read_resource",
          description: "Read one resource of an MCP server by its URI.",
          needs_computer: false,
          tokens: builtinToolTokens(
            "mcp_read_resource",
            "Read one resource of an MCP server by its URI.",
          ),
        },
      ]
    : [];
  world.tools = [...builtIn, ...offered, ...resources].sort((a, b) =>
    a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
  );
}
