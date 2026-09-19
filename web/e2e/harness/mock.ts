/**
 * MockHarness answers every `/api` request and the `/api/events` WebSocket
 * from an in-memory World, inside the browser context, so the UI runs with no
 * Go harness, Postgres, or Docker. It implements the contract in
 * docs/api/http.md and docs/api/events.md closely enough that the UI cannot
 * tell the difference, and it plays scripted agent replies so a sent message
 * streams a turn like a real run.
 */

import type { BrowserContext, Route, WebSocketRoute } from "@playwright/test";

import type { EikaEvent, EventType } from "../../src/api/events.ts";
import type {
  Entry,
  Message,
  Model,
  Project,
  Provider,
  Run,
  Session,
  Workspace,
} from "../../src/api/types.ts";
import { entryKind, fixedNow, pathOf, syncSystem } from "./world.ts";
import type { ReplyStep, World } from "./world.ts";

/** mockToken is the bearer token the mock harness issues and accepts. */
export const mockToken = "mock-token";

/** wrongPassword is the one password sign-in refuses, to show the error. */
export const wrongPassword = "wrong";

type Reply = { status: number; body?: unknown };

type Handler = (req: {
  params: string[];
  query: URLSearchParams;
  body: Record<string, unknown>;
}) => Reply | Promise<Reply>;

type RouteDef = { method: string; pattern: RegExp; auth: boolean; handle: Handler };

type Socket = { ws: WebSocketRoute; topics: Set<string> };

/** RequestLog is one API request the page made, for a report or an assertion. */
export type RequestLog = { method: string; path: string; status: number; body?: unknown };

/** MockOptions tune how the mock harness behaves. */
export type MockOptions = {
  /** stepDelayMs is the pause between streamed events of a scripted reply. */
  stepDelayMs?: number;
};

function ok(body: unknown, status = 200): Reply {
  return { status, body };
}

function fail(status: number, code: string, message: string): Reply {
  return { status, body: { error: { code, message } } };
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export class MockHarness {
  readonly world: World;
  /** requests lists every API call in order. */
  readonly requests: RequestLog[] = [];
  /** unhandled lists API calls no route matched: a gap in the mock. */
  readonly unhandled: string[] = [];
  private readonly sockets = new Set<Socket>();
  private readonly routes: RouteDef[];
  private readonly stepDelayMs: number;
  private busy = 0;
  private seq = 1000;
  private readonly answers = new Map<string, (answer: string) => void>();

  constructor(world: World, options: MockOptions = {}) {
    this.world = world;
    this.stepDelayMs = options.stepDelayMs ?? 40;
    this.routes = this.buildRoutes();
  }

  /** install serves the API for every page of the context. */
  async install(context: BrowserContext): Promise<void> {
    if (this.world.signedIn) {
      // Once per tab: a reload after signing out must stay signed out.
      await context.addInitScript((token: string) => {
        if (sessionStorage.getItem("eika.mock.seeded") !== null) return;
        sessionStorage.setItem("eika.mock.seeded", "1");
        localStorage.setItem("eika.token", token);
      }, mockToken);
    }
    await context.routeWebSocket(
      (url) => url.pathname === "/api/events",
      (ws) => {
        this.accept(ws);
      },
    );
    await context.route(
      (url) => url.pathname.startsWith("/api/"),
      (route) => this.serve(route),
    );
  }

  /** idle resolves once no request is being answered and no reply is playing. */
  async idle(timeoutMs = 5000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    while (this.busy > 0 && Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 10));
    }
  }

  /** emit publishes an event to every stream subscribed to its topic. */
  emit(event: EikaEvent): void {
    const frame = JSON.stringify(event);
    for (const socket of this.sockets) {
      if (event.type === "bus.dropped" || socket.topics.has(event.topic)) socket.ws.send(frame);
    }
  }

  /** event builds an envelope stamped with the fixed clock. */
  event(type: EventType, topic: string, payload?: unknown): EikaEvent {
    return { type, topic, time: fixedNow, payload };
  }

  /** dropStreams closes every event stream, as a harness restart would. */
  dropStreams(): void {
    for (const socket of this.sockets) void socket.ws.close();
    this.sockets.clear();
  }

  private nextId(prefix: string): string {
    this.seq += 1;
    return `${prefix}-${String(this.seq)}`;
  }

  private accept(ws: WebSocketRoute): void {
    const url = new URL(ws.url());
    if (url.searchParams.get("token") !== mockToken) {
      void ws.close({ code: 1008, reason: "unauthorized" });
      return;
    }
    const topics = new Set((url.searchParams.get("topics") ?? "").split(",").filter(Boolean));
    const socket: Socket = { ws, topics };
    this.sockets.add(socket);
    ws.onClose(() => {
      this.sockets.delete(socket);
    });
    ws.onMessage((data) => {
      this.onFrame(socket, typeof data === "string" ? data : data.toString());
    });
    for (const e of this.world.eventsOnConnect) ws.send(JSON.stringify(e));
  }

  private onFrame(socket: Socket, data: string): void {
    let frame: { type?: string; topics?: string[]; session_id?: string; since?: string };
    try {
      frame = JSON.parse(data) as typeof frame;
    } catch {
      return;
    }
    if (frame.type === "subscribe") {
      socket.topics = new Set(frame.topics ?? []);
    } else if (frame.type === "session.replay") {
      const session = this.world.sessions.find((s) => s.id === frame.session_id);
      if (!session) return;
      let path = pathOf(this.world, session);
      if (frame.since) {
        const at = path.findIndex((e) => e.id === frame.since);
        path = at < 0 ? path : path.slice(at + 1);
      }
      for (const entry of path) {
        socket.ws.send(JSON.stringify(this.sessionMessage(session, entry)));
      }
    }
  }

  private sessionMessage(session: Session, entry: Entry): EikaEvent {
    return this.event("session.message", `session:${session.id}`, {
      session_id: session.id,
      entry_id: entry.id,
      parent_id: entry.parent_id,
      kind: entry.kind,
      commit: entry.commit,
      created_at: entry.created_at,
      message: entry.message,
    });
  }

  private async serve(route: Route): Promise<void> {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();
    const path = url.pathname;
    this.busy += 1;
    try {
      let body: Record<string, unknown> = {};
      const raw = request.postData();
      if (raw) {
        try {
          body = JSON.parse(raw) as Record<string, unknown>;
        } catch {
          body = {};
        }
      }
      let reply: Reply | undefined;
      for (const def of this.routes) {
        if (def.method !== method) continue;
        const match = def.pattern.exec(path);
        if (!match) continue;
        const authorised = request.headers().authorization === `Bearer ${mockToken}`;
        reply =
          def.auth && !authorised
            ? fail(401, "unauthorized", "missing or invalid token")
            : await def.handle({
                params: match.slice(1).map(decodeURIComponent),
                query: url.searchParams,
                body,
              });
        break;
      }
      if (!reply) {
        this.unhandled.push(`${method} ${path}`);
        reply = fail(404, "not_found", `mock harness has no route for ${method} ${path}`);
      }
      syncSystem(this.world);
      this.requests.push({ method, path, status: reply.status, body: reply.body });
      await route.fulfill(
        reply.body === undefined
          ? { status: reply.status, body: "" }
          : { status: reply.status, json: reply.body },
      );
    } finally {
      this.busy -= 1;
    }
  }

  private buildRoutes(): RouteDef[] {
    const w = this.world;
    const routes: RouteDef[] = [];
    const on = (method: string, path: string, handle: Handler, auth = true) => {
      const pattern = new RegExp(`^${path.replace(/\{[a-z_]+\}/g, "([^/]+)")}$`);
      routes.push({ method, pattern, auth, handle });
    };
    const find = <T extends { id: string }>(rows: T[], id: string | undefined, what: string) => {
      const row = rows.find((r) => r.id === id);
      return row ?? fail(404, "not_found", `${what} not found`);
    };
    const now = () => fixedNow;

    on("GET", "/api/healthz", () => ok({ status: "ok" }), false);

    // Sign-in.
    on("GET", "/api/auth/status", () => ok({ password_set: w.passwordSet }), false);
    on(
      "POST",
      "/api/auth/setup",
      ({ body }) => {
        if (w.passwordSet) return fail(409, "conflict", "a password is already set");
        if (str(body.password).length < 8) {
          return fail(400, "invalid_request", "password must be at least 8 characters");
        }
        w.passwordSet = true;
        return ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" });
      },
      false,
    );
    on(
      "POST",
      "/api/auth/login",
      ({ body }) =>
        str(body.password) === wrongPassword
          ? fail(401, "unauthorized", "wrong password")
          : ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }),
      false,
    );
    on("POST", "/api/auth/logout", () => ({ status: 204 }));
    on("PUT", "/api/auth/password", ({ body }) =>
      str(body.current_password) === wrongPassword
        ? fail(400, "invalid_request", "current password is wrong")
        : ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }),
    );

    // Settings and system.
    on("GET", "/api/settings", () => ok(w.settings));
    on("PUT", "/api/settings", ({ body }) => {
      for (const [key, value] of Object.entries(body)) {
        if (value === null) Reflect.deleteProperty(w.settings.settings, key);
        else w.settings.settings[key] = value;
      }
      if (typeof body.default_model === "string") w.defaultModel = body.default_model;
      return ok(w.settings);
    });
    on("GET", "/api/system", () => ok(w.system));

    // Providers and models.
    on("GET", "/api/providers", () => ok({ providers: w.providers, kinds: w.providerKinds }));
    on("POST", "/api/providers/probe", () => ok({ models: w.probeModels }));
    on("POST", "/api/providers", ({ body }) => {
      if (str(body.name) === "" || str(body.base_url) === "") {
        return fail(400, "invalid_request", "name and base_url are required");
      }
      const key = str(body.api_key);
      const row: Provider = {
        id: this.nextId("prov"),
        name: str(body.name),
        kind: str(body.kind) || "openai",
        base_url: str(body.base_url),
        api_key_set: key !== "",
        ...(key.length > 8 ? { api_key_hint: `…${key.slice(-4)}` } : {}),
        created_at: now(),
        updated_at: now(),
      };
      w.providers.push(row);
      return ok(row, 201);
    });
    on("PATCH", "/api/providers/{id}", ({ params, body }) => {
      const row = find(w.providers, params[0], "provider");
      if ("status" in row) return row;
      if (typeof body.name === "string") row.name = body.name;
      if (typeof body.base_url === "string") {
        // As the harness does: a new URL without a new key clears the key.
        if (body.base_url.trim() !== row.base_url && typeof body.api_key !== "string") {
          row.api_key_set = false;
          delete row.api_key_hint;
        }
        row.base_url = body.base_url.trim();
      }
      if (typeof body.api_key === "string") row.api_key_set = body.api_key !== "";
      row.updated_at = now();
      return ok(row);
    });
    on("DELETE", "/api/providers/{id}", ({ params }) => {
      w.providers = w.providers.filter((p) => p.id !== params[0]);
      w.models = w.models.filter((m) => m.provider_id !== params[0]);
      return { status: 204 };
    });
    on("GET", "/api/models", () =>
      ok({ models: w.models, ...(w.defaultModel ? { default: w.defaultModel } : {}) }),
    );
    on("POST", "/api/models/test", ({ body }) =>
      ok({ reply: `Hello from ${str(body.model)}.`, stop_reason: "stop", latency_ms: 412 }),
    );
    on("POST", "/api/models", ({ body }) => {
      const name = str(body.name) || str(body.model);
      if (w.models.some((m) => m.name === name)) {
        return fail(409, "conflict", `a model named ${name} exists`);
      }
      const row: Model = {
        id: this.nextId("mod"),
        provider_id: str(body.provider_id),
        name,
        model: str(body.model),
        context_window: Number(body.context_window) || 0,
        max_output: Number(body.max_output) || 0,
        reasoning_effort: str(body.reasoning_effort) as Model["reasoning_effort"],
        preserve_thinking: body.preserve_thinking === true,
        created_at: now(),
        updated_at: now(),
      };
      w.models.push(row);
      w.defaultModel ??= row.name;
      return ok(row, 201);
    });
    on("PATCH", "/api/models/{id}", ({ params, body }) => {
      const row = find(w.models, params[0], "model");
      if ("status" in row) return row;
      Object.assign(row, body, { updated_at: now() });
      return ok(row);
    });
    on("DELETE", "/api/models/{id}", ({ params }) => {
      w.models = w.models.filter((m) => m.id !== params[0]);
      return { status: 204 };
    });

    // Projects.
    on("GET", "/api/projects", () => ok({ projects: w.projects }));
    on("POST", "/api/projects", ({ body }) => {
      if (str(body.name) === "") return fail(400, "invalid_request", "name is required");
      if (w.projects.some((p) => p.name === body.name)) {
        return fail(409, "conflict", `a project named ${str(body.name)} exists`);
      }
      const row: Project = {
        id: this.nextId("proj"),
        name: str(body.name),
        kind: body.kind === "local" ? "local" : "remote",
        default_branch: str(body.default_branch) || "main",
        created_at: now(),
        ...(body.remote_url ? { remote_url: str(body.remote_url) } : {}),
        ...(body.host_path ? { host_path: str(body.host_path) } : {}),
        ...(body.remote_username ? { remote_username: str(body.remote_username) } : {}),
        ...(body.remote_password ? { remote_password_set: true } : {}),
      };
      w.projects.unshift(row);
      return ok(row, 201);
    });
    on("GET", "/api/projects/{id}", ({ params }) => {
      const row = find(w.projects, params[0], "project");
      return "status" in row ? row : ok(row);
    });
    on("PATCH", "/api/projects/{id}", ({ params, body }) => {
      const row = find(w.projects, params[0], "project");
      if ("status" in row) return row;
      if (typeof body.default_branch === "string") row.default_branch = body.default_branch;
      if (typeof body.remote_username === "string") row.remote_username = body.remote_username;
      if (typeof body.remote_password === "string") {
        row.remote_password_set = body.remote_password !== "";
      }
      return ok(row);
    });
    on("DELETE", "/api/projects/{id}", ({ params }) => {
      const gone = new Set(w.workspaces.filter((x) => x.project_id === params[0]).map((x) => x.id));
      w.projects = w.projects.filter((p) => p.id !== params[0]);
      w.workspaces = w.workspaces.filter((x) => !gone.has(x.id));
      w.sessions = w.sessions.filter((s) => !gone.has(s.workspace_id));
      return { status: 204 };
    });

    // Workspaces.
    on("GET", "/api/workspaces", ({ query }) => {
      const project = query.get("project_id");
      return ok({
        workspaces: project ? w.workspaces.filter((x) => x.project_id === project) : w.workspaces,
      });
    });
    on("POST", "/api/workspaces", ({ body }) => {
      const project = find(w.projects, str(body.project_id), "project");
      if ("status" in project) return project;
      const name = str(body.name);
      const row: Workspace = {
        id: this.nextId("ws"),
        project_id: project.id,
        name,
        branch: str(body.branch) || `eika/${name}`,
        base_commit: "3f9c2a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a",
        image: str(body.image) || w.settings.defaults.sandbox_image,
        state: "running",
        created_at: now(),
        updated_at: now(),
      };
      w.workspaces.unshift(row);
      return ok(row, 201);
    });
    on("GET", "/api/workspaces/{id}", ({ params }) => {
      const row = find(w.workspaces, params[0], "workspace");
      return "status" in row ? row : ok(row);
    });
    const setState = (id: string | undefined, state: Workspace["state"]) => {
      const row = find(w.workspaces, id, "workspace");
      if ("status" in row) return row;
      row.state = state;
      row.updated_at = now();
      this.emit(
        this.event("workspace.state", `workspace:${row.id}`, {
          workspace_id: row.id,
          project_id: row.project_id,
          state,
        }),
      );
      return ok(row);
    };
    on("POST", "/api/workspaces/{id}/start", ({ params }) => setState(params[0], "running"));
    on("POST", "/api/workspaces/{id}/stop", ({ params }) => setState(params[0], "stopped"));
    on("DELETE", "/api/workspaces/{id}", ({ params }) => {
      w.workspaces = w.workspaces.filter((x) => x.id !== params[0]);
      w.sessions = w.sessions.filter((s) => s.workspace_id !== params[0]);
      return { status: 204 };
    });
    on("GET", "/api/workspaces/{id}/diff", ({ params }) => {
      const row = find(w.workspaces, params[0], "workspace");
      if ("status" in row) return row;
      if (row.state !== "running") return fail(409, "conflict", "workspace is not running");
      return ok(
        w.diffs[row.id] ?? {
          workspace_id: row.id,
          base_commit: row.base_commit,
          diff: "",
          status: "",
        },
      );
    });

    // Sessions.
    on("GET", "/api/sessions", ({ query }) => {
      const ws = query.get("workspace_id");
      return ok({ sessions: ws ? w.sessions.filter((s) => s.workspace_id === ws) : w.sessions });
    });
    on("POST", "/api/sessions", ({ body }) => {
      const ws = find(w.workspaces, str(body.workspace_id), "workspace");
      if ("status" in ws) return ws;
      const row: Session = {
        id: this.nextId("ses"),
        workspace_id: ws.id,
        title: str(body.title) || "New session",
        created_at: now(),
        updated_at: now(),
      };
      w.sessions.unshift(row);
      w.entries[row.id] = [];
      return ok(row, 201);
    });
    on("GET", "/api/sessions/{id}", ({ params }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const head = (w.entries[row.id] ?? []).find((e) => e.id === row.head_entry_id);
      return ok({ session: row, ...(head ? { head } : {}) });
    });
    on("DELETE", "/api/sessions/{id}", ({ params }) => {
      w.sessions = w.sessions.filter((s) => s.id !== params[0]);
      return { status: 204 };
    });
    on("GET", "/api/sessions/{id}/outline", ({ params }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      return ok({
        session_id: row.id,
        head_entry_id: row.head_entry_id,
        nodes: (w.entries[row.id] ?? []).map((e) => ({
          id: e.id,
          parent_id: e.parent_id,
          kind: e.kind,
          preview: preview(e.message),
          commit: e.commit,
          created_at: e.created_at,
        })),
      });
    });
    on("GET", "/api/sessions/{id}/path", ({ params }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const entries = pathOf(w, row);
      return ok({ session_id: row.id, entries, messages: entries.map((e) => e.message) });
    });
    on("POST", "/api/sessions/{id}/head", ({ params, body }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const entry = find(w.entries[row.id] ?? [], str(body.entry_id), "entry");
      if ("status" in entry) return entry;
      row.head_entry_id = entry.id;
      return ok(row);
    });
    on("POST", "/api/sessions/{id}/fork", ({ params, body }) => {
      const source = find(w.sessions, params[0], "session");
      if ("status" in source) return source;
      const row: Session = {
        id: this.nextId("ses"),
        workspace_id: source.workspace_id,
        title: str(body.title) || `${source.title} (fork)`,
        parent_session_id: source.id,
        head_entry_id: str(body.entry_id),
        created_at: now(),
        updated_at: now(),
      };
      w.sessions.unshift(row);
      w.entries[row.id] = [...(w.entries[source.id] ?? [])];
      return ok(row, 201);
    });
    on("GET", "/api/sessions/{id}/agents", () => ok({ agents: [] }));

    // Runs, queues, and questions.
    on("GET", "/api/sessions/{id}/run", ({ params }) => {
      const id = params[0] ?? "";
      const active = w.activeRuns[id];
      const last = active
        ? w.runs[active]
        : Object.values(w.runs)
            .filter((r) => r.session_id === id)
            .at(-1);
      return ok({
        session_id: id,
        active: active !== undefined,
        ...(last ? { run: last } : {}),
        pending_steering: w.pendingSteering[id] ?? [],
        pending_follow_ups: w.pendingFollowUps[id] ?? [],
        questions: w.questions.filter((q) => q.session_id === id),
      });
    });
    on("POST", "/api/sessions/{id}/messages", ({ params, body }) => {
      const session = find(w.sessions, params[0], "session");
      if ("status" in session) return session;
      const text = str(body.text);
      if (text === "") return fail(400, "invalid_request", "text is required");
      const mode = str(body.mode) || "run";
      const active = w.activeRuns[session.id];
      if (mode !== "run") {
        if (!active) return fail(409, "conflict", "no run in progress");
        const queue = mode === "steer" ? w.pendingSteering : w.pendingFollowUps;
        (queue[session.id] ??= []).push(text);
        return ok(w.runs[active], 202);
      }
      if (active) return fail(409, "conflict", "a run is already going");
      if (w.models.length === 0) return fail(409, "conflict", "no model is configured");
      const run: Run = {
        id: this.nextId("run"),
        session_id: session.id,
        state: "running",
        started_at: now(),
      };
      w.runs[run.id] = run;
      w.activeRuns[session.id] = run.id;
      void this.play(session, run, text);
      return ok(run, 202);
    });
    on("POST", "/api/runs/{id}/abort", ({ params }) => {
      const run = w.runs[params[0] ?? ""];
      if (!run) return fail(404, "not_found", "run not found");
      if (run.state !== "running") return fail(409, "conflict", "run already finished");
      this.finish(run, "aborted");
      return ok(run);
    });
    on("POST", "/api/questions/{id}/answer", ({ params, body }) => {
      const answer = this.answers.get(params[0] ?? "");
      if (!answer) return fail(404, "not_found", "no run waits on that question");
      if (str(body.answer) === "") return fail(400, "invalid_request", "answer is required");
      answer(str(body.answer));
      return { status: 204 };
    });

    return routes;
  }

  private finish(run: Run, state: Run["state"], error?: string): void {
    run.state = state;
    run.finished_at = fixedNow;
    if (error !== undefined) run.error = error;
    Reflect.deleteProperty(this.world.activeRuns, run.session_id);
    this.world.questions = this.world.questions.filter((q) => q.run_id !== run.id);
  }

  private append(session: Session, message: Message): Entry {
    const list = (this.world.entries[session.id] ??= []);
    const entry: Entry = {
      id: this.nextId("ent"),
      seq: list.length + 1,
      kind: entryKind(message),
      created_at: fixedNow,
      message,
      ...(session.head_entry_id === undefined ? {} : { parent_id: session.head_entry_id }),
    };
    list.push(entry);
    session.head_entry_id = entry.id;
    session.updated_at = fixedNow;
    return entry;
  }

  /** play streams one scripted reply as a run's events and stores its entries. */
  private async play(session: Session, run: Run, text: string): Promise<void> {
    this.busy += 1;
    const topic = `session:${session.id}`;
    const turn = this.nextId("turn");
    const pause = () => new Promise((r) => setTimeout(r, this.stepDelayMs));
    const send = (type: EventType, payload: Record<string, unknown>) => {
      this.emit(this.event(type, topic, { run_id: turn, ...payload }));
    };
    const steps: ReplyStep[] = this.world.replies.shift() ?? [{ say: `Mock reply to: ${text}` }];
    try {
      this.append(session, { role: "user", content: text });
      send("turn.start", {
        session_id: session.id,
        workspace_id: session.workspace_id,
        message: text,
      });
      for (const step of steps) {
        await pause();
        if (run.state !== "running") return;
        if ("say" in step) {
          // Stream the prose in a few chunks so a mid-stream screenshot shows a partial reply.
          const words = step.say.split(/(?<= )/);
          const chunk = Math.max(1, Math.ceil(words.length / 4));
          for (let i = 0; i < words.length; i += chunk) {
            send("message.delta", { text: words.slice(i, i + chunk).join("") });
            await pause();
          }
          this.append(session, { role: "assistant", content: step.say });
        } else if ("tool" in step) {
          const callId = this.nextId("call");
          send("tool.call", { call_id: callId, name: step.tool, arguments: step.args });
          this.append(session, {
            role: "assistant",
            tool_calls: [{ id: callId, name: step.tool, arguments: step.args }],
          });
          if (step.output !== undefined) {
            await pause();
            send("tool.output", { call_id: callId, text: step.output });
          }
          await pause();
          send("tool.result", {
            call_id: callId,
            name: step.tool,
            content: step.result,
            is_error: step.isError === true,
            details: step.details,
            duration_ms: 1234,
          });
          this.append(session, {
            role: "tool",
            tool_call_id: callId,
            content: step.result,
            is_error: step.isError === true,
          });
        } else if ("ask" in step) {
          const questionId = this.nextId("q");
          const callId = this.nextId("call");
          const question = {
            id: questionId,
            session_id: session.id,
            run_id: run.id,
            call_id: callId,
            question: step.ask,
            ...(step.options ? { options: step.options } : {}),
            allow_free_text: step.allowFreeText ?? true,
            asked_at: fixedNow,
          };
          this.world.questions.push(question);
          send("tool.call", {
            call_id: callId,
            name: "ask_user",
            arguments: { question: step.ask, options: step.options },
          });
          send("question.asked", {
            session_id: session.id,
            call_id: callId,
            question_id: questionId,
            question: step.ask,
            options: step.options,
            allow_free_text: question.allow_free_text,
          });
          // Waiting on a person is not work the page is doing, so a
          // screenshot of the question must not wait for it.
          this.busy -= 1;
          const answer = await new Promise<string>((resolve) => {
            this.answers.set(questionId, resolve);
          });
          this.busy += 1;
          this.answers.delete(questionId);
          this.world.questions = this.world.questions.filter((q) => q.id !== questionId);
          send("tool.result", {
            call_id: callId,
            name: "ask_user",
            content: answer,
            is_error: false,
            duration_ms: 5000,
          });
        } else if ("fail" in step) {
          send("run.error", { message: step.fail, retryable: step.retryable ?? false });
          this.finish(run, "error", step.fail);
          return;
        } else {
          this.busy -= 1;
          await new Promise<void>(() => undefined);
        }
      }
      await pause();
      if (run.state !== "running") return;
      this.finish(run, "done");
      send("turn.end", {
        stop_reason: "stop",
        usage: { input_tokens: 1842, output_tokens: 311, total_tokens: 2153 },
      });
    } finally {
      this.busy -= 1;
    }
  }
}

function preview(message: Message): string {
  if (message.content) return message.content.slice(0, 80);
  const call = message.tool_calls?.[0];
  return call?.name ?? "";
}
