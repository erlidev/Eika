/**
 * The in-memory world a mock harness serves: every table the HTTP API reads,
 * plus the session trees a replay walks. A scenario is a function that builds
 * one; routes then read and mutate it, so a flow driven through the UI (create
 * a project, send a message) changes what the next request sees.
 */

import type { EikaEvent } from "../../src/api/events.ts";
import type {
  Entry,
  Message,
  Model,
  Project,
  Provider,
  Question,
  Run,
  SearchStatus,
  Session,
  SettingsState,
  SystemStatus,
  Tool,
  Workspace,
  WorkspaceDiff,
} from "../../src/api/types.ts";

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
  | { fail: string; retryable?: boolean }
  /**
   * cutOff ends the turn on an incomplete stop reason, as an endpoint that
   * ran out of output room does. What was said before it is kept.
   */
  | { cutOff: "length" | "content_filter" }
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
  projects: Project[];
  workspaces: Workspace[];
  diffs: Record<string, WorkspaceDiff>;
  /**
   * files holds each workspace's files by path, keyed by workspace id; the
   * directories follow from the paths. Content with a NUL byte is binary.
   */
  files: Record<string, Record<string, string>>;
  sessions: Session[];
  /** tools is GET /api/tools: every tool a run can offer, sorted by name. */
  tools: Tool[];
  /** entries holds each session's tree, keyed by session id, in insertion order. */
  entries: Record<string, Entry[]>;
  runs: Record<string, Run>;
  /** activeRuns maps a session to the run that is going on it. */
  activeRuns: Record<string, string>;
  questions: Question[];
  pendingSteering: Record<string, string[]>;
  pendingFollowUps: Record<string, string[]>;
  /** replies are consumed one per run, oldest first; an empty list echoes. */
  replies: ReplyStep[][];
  /** probeModels is what "detect models" on any endpoint answers. */
  probeModels: { id: string; context_window?: number; max_output?: number }[];
  /** eventsOnConnect are sent once to every event stream that opens. */
  eventsOnConnect: EikaEvent[];
};

/** fixedNow is the clock every screenshot is taken at. */
export const fixedNow = "2026-03-14T15:00:00.000Z";

/** minutesAgo formats a time before fixedNow, for seeding timestamps. */
export function minutesAgo(minutes: number): string {
  return new Date(Date.parse(fixedNow) - minutes * 60_000).toISOString();
}

/** emptyWorld is a harness that is set up and signed in, with nothing configured. */
export function emptyWorld(): World {
  return {
    passwordSet: true,
    signedIn: true,
    settings: {
      settings: { setup_complete: true },
      defaults: {
        sandbox_image: "eika-sandbox:latest",
        subagent_max_depth: 2,
        subagent_max_children: 4,
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
      },
    },
    search: searchStatus(),
    system: {
      docker: { reachable: true },
      sandbox_image: { name: "eika-sandbox:latest", present: true },
      providers: 0,
      models: 0,
      projects: 0,
    },
    providers: [],
    providerKinds: ["openai", "anthropic"],
    models: [],
    projects: [],
    workspaces: [],
    diffs: {},
    files: {},
    sessions: [],
    tools: toolCatalog(),
    entries: {},
    runs: {},
    activeRuns: {},
    questions: [],
    pendingSteering: {},
    pendingFollowUps: {},
    replies: [],
    probeModels: [
      { id: "gpt-5", context_window: 400000, max_output: 128000 },
      { id: "gpt-5-mini", context_window: 400000, max_output: 128000 },
    ],
    eventsOnConnect: [],
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
  const tool = (name: string, description: string, needsWorkspace = true): Tool => ({
    name,
    description,
    needs_workspace: needsWorkspace,
  });
  return [
    tool("ask_user", "Ask the user a question and wait for the answer.", false),
    tool("bash", "Run a shell command in the workspace."),
    tool("edit", "Replace one exact string in a file."),
    tool("find", "Find files whose path matches a glob."),
    tool("grep", "Search file contents with a regular expression."),
    tool("list_agents", "List the child agents this session spawned."),
    tool("ls", "List a directory."),
    tool("read", "Read a text file."),
    tool("spawn_agent", "Start a child agent in a workspace of its own."),
    tool("wait_agents", "Wait for child agents to finish."),
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
    tool("write", "Write a file, replacing what it held."),
  ];
}

/**
 * sessionTools is the tool list a new session reports: every tool, or for a
 * chat every tool that needs no workspace, as the harness narrows it.
 */
export function sessionTools(world: World, chat: boolean): string[] {
  return world.tools.filter((t) => !chat || !t.needs_workspace).map((t) => t.name);
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
    return `${prefix}-${String(this.seq)}`;
  }

  provider(input: Partial<Provider> & { name: string }): Provider {
    const row: Provider = {
      id: this.id("prov"),
      kind: "openai",
      base_url: "https://api.openai.com/v1",
      api_key_set: true,
      api_key_hint: "a1b2",
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
      model: input.name,
      context_window: 400000,
      max_output: 128000,
      reasoning_efforts: [],
      preserve_thinking: false,
      created_at: minutesAgo(590),
      updated_at: minutesAgo(590),
      ...input,
    };
    this.world.models.push(row);
    this.world.defaultModel ??= row.name;
    return row;
  }

  project(input: Partial<Project> & { name: string }): Project {
    const row: Project = {
      id: this.id("proj"),
      kind: "remote",
      remote_url: `https://github.com/example/${input.name}.git`,
      default_branch: "main",
      created_at: minutesAgo(500),
      ...input,
    };
    this.world.projects.unshift(row);
    return row;
  }

  workspace(project: Project, input: Partial<Workspace> & { name: string }): Workspace {
    const row: Workspace = {
      id: this.id("ws"),
      project_id: project.id,
      branch: `eika/${input.name}`,
      base_commit: "3f9c2a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a",
      image: "eika-sandbox:latest",
      state: "running",
      container_id: "c0ffee",
      created_at: minutesAgo(400),
      updated_at: minutesAgo(30),
      ...input,
    };
    this.world.workspaces.unshift(row);
    return row;
  }

  session(workspace: Workspace, input: Partial<Session> & { title: string }): Session {
    const row: Session = {
      id: this.id("ses"),
      workspace_id: workspace.id,
      kind: "user",
      tools: sessionTools(this.world, false),
      created_at: minutesAgo(120),
      updated_at: minutesAgo(5),
      ...input,
    };
    this.world.sessions.unshift(row);
    this.world.entries[row.id] ??= [];
    return row;
  }

  /** chat adds a session with no workspace, offering the tools that need none. */
  chat(input: Partial<Session> & { title: string }): Session {
    const row: Session = {
      id: this.id("chat"),
      kind: "user",
      tools: sessionTools(this.world, true),
      created_at: minutesAgo(90),
      updated_at: minutesAgo(5),
      ...input,
    };
    this.world.sessions.unshift(row);
    this.world.entries[row.id] ??= [];
    return row;
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

/** syncSystem recounts the system status from the tables. */
export function syncSystem(world: World): void {
  world.system.providers = world.providers.length;
  world.system.models = world.models.length;
  world.system.projects = world.projects.length;
}
