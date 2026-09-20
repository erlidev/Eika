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
  SearchLimit,
  SearchStatus,
  Session,
  Workspace,
} from "../../src/api/types.ts";
import { entryKind, fixedNow, minutesAgo, pathOf, syncSystem } from "./world.ts";
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
  /** raw is the body as sent, for a route whose body is not JSON. */
  raw: string;
}) => Reply | Promise<Reply>;

type RouteDef = {
  method: string;
  /** key is the route as written, like `PATCH /api/providers/{id}`. */
  key: string;
  pattern: RegExp;
  auth: boolean;
  handle: Handler;
};

type Socket = { ws: WebSocketRoute; topics: Set<string> };

/** RequestLog is one API request the page made, for a report or an assertion. */
export type RequestLog = { method: string; path: string; status: number; body?: unknown };

/**
 * Failure is how a failing route answers: an HTTP status with the documented
 * error body (or `bare` for a proxy's body-less answer), `network` for a
 * request that never reaches the harness, or `hang` for one that never ends,
 * to show a loading state.
 */
export type Failure =
  { status: number; code?: string; message?: string; bare?: boolean } | "network" | "hang";

/** MockOptions tune how the mock harness behaves. */
export type MockOptions = {
  /** stepDelayMs is the pause between streamed events of a scripted reply. */
  stepDelayMs?: number;
  /**
   * failing makes routes fail from the first request, keyed as the route is
   * written: `GET /api/providers`, `PATCH /api/providers/{id}`. See failRoute.
   */
  failing?: Record<string, Failure>;
};

/** parseFailure reads a failure as a step or flag writes it: `500`, `502 bare`, `network`, `hang`. */
export function parseFailure(text: string): Failure {
  const t = text.trim();
  if (t === "network" || t === "hang") return t;
  const match = /^(\d{3})(?:\s+(bare|.+))?$/.exec(t);
  if (!match)
    throw new Error(`a failure is a status like 500, "502 bare", network, or hang; got "${t}"`);
  const status = Number(match[1]);
  const rest = match[2];
  if (rest === "bare") return { status, bare: true };
  return rest === undefined ? { status } : { status, message: rest };
}

/** statusCodes are the documented error codes by HTTP status. */
const statusCodes: Record<number, string> = {
  400: "invalid_request",
  401: "unauthorized",
  404: "not_found",
  409: "conflict",
  500: "internal",
};

/** terminalPath matches a workspace's terminal socket and captures its id. */
const terminalPath = /^\/api\/workspaces\/([^/]+)\/terminal$/;

/** mockCommit is the commit every commit and push reports. */
const mockCommit = "9b2e4c1f7a3d5e6b8c0a1f2e3d4c5b6a7f8e9d0c";

function ok(body: unknown, status = 200): Reply {
  return { status, body };
}

function fail(status: number, code: string, message: string): Reply {
  return { status, body: { error: { code, message } } };
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** strings narrows a JSON body field to the list of strings it should be. */
function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
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
  private readonly failing = new Map<string, Failure>();

  constructor(world: World, options: MockOptions = {}) {
    this.world = world;
    this.stepDelayMs = options.stepDelayMs ?? 40;
    this.routes = this.buildRoutes();
    for (const [route, failure] of Object.entries(options.failing ?? {})) {
      this.failRoute(route, failure);
    }
  }

  /**
   * failRoute makes every request to a route fail until healRoute. The route
   * is written as in docs/api/http.md, `GET /api/providers`; an unknown one
   * throws, so a typo cannot pass for a route that works.
   */
  failRoute(route: string, failure: Failure): void {
    const key = route.trim().replace(/\s+/g, " ");
    if (!this.routes.some((r) => r.key === key)) {
      throw new Error(
        `mock harness has no route "${key}"; routes: ${this.routes.map((r) => r.key).join(", ")}`,
      );
    }
    this.failing.set(key, failure);
  }

  /** healRoute lets a failed route answer normally again, as a Retry would find. */
  healRoute(route: string): void {
    this.failing.delete(route.trim().replace(/\s+/g, " "));
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
    await context.routeWebSocket(
      (url) => terminalPath.test(url.pathname),
      (ws) => {
        this.shell(ws);
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

  /**
   * shell plays a terminal that echoes what is typed and prints a prompt
   * after each line; `exit` ends it the way a shell that exits cleanly does,
   * with no exit code. A workspace that is not running refuses the socket.
   */
  private shell(ws: WebSocketRoute): void {
    const url = new URL(ws.url());
    const id = decodeURIComponent(terminalPath.exec(url.pathname)?.[1] ?? "");
    const workspace = this.world.workspaces.find((x) => x.id === id);
    if (url.searchParams.get("token") !== mockToken || workspace?.state !== "running") {
      void ws.close({ code: 1011, reason: "refused" });
      return;
    }
    const out = (text: string) => {
      ws.send(JSON.stringify({ type: "output", data: Buffer.from(text).toString("base64") }));
    };
    const prompt = `${workspace.name} $ `;
    out(`Connected to ${workspace.name} (${workspace.branch}).\r\n${prompt}`);
    let line = "";
    ws.onMessage((data) => {
      let frame: { type?: string; data?: string };
      try {
        frame = JSON.parse(typeof data === "string" ? data : data.toString()) as typeof frame;
      } catch {
        return;
      }
      if (frame.type !== "input" || frame.data === undefined) return;
      for (const ch of Buffer.from(frame.data, "base64").toString("utf8")) {
        if (ch === "\r") {
          if (line.trim() === "exit") {
            out("\r\n");
            ws.send(JSON.stringify({ type: "exit" }));
            void ws.close();
            return;
          }
          out(`\r\n${prompt}`);
          line = "";
        } else if (ch === "\x7f") {
          if (line !== "") out("\b \b");
          line = line.slice(0, -1);
        } else {
          line += ch;
          out(ch);
        }
      }
    });
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
      const raw = request.postData() ?? undefined;
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
        const failure = this.failing.get(def.key);
        if (failure === "hang") {
          // Left unanswered on purpose; the page is waiting, not the mock.
          this.requests.push({ method, path, status: 0 });
          return;
        }
        if (failure === "network") {
          this.requests.push({ method, path, status: 0 });
          await route.abort("connectionrefused");
          return;
        }
        if (failure !== undefined) {
          reply = failure.bare
            ? { status: failure.status }
            : fail(
                failure.status,
                failure.code ?? statusCodes[failure.status] ?? "internal",
                failure.message ??
                  (failure.status >= 500
                    ? "internal error"
                    : `refused by the mock (HTTP ${String(failure.status)})`),
              );
          break;
        }
        const authorised = request.headers().authorization === `Bearer ${mockToken}`;
        reply =
          def.auth && !authorised
            ? fail(401, "unauthorized", "missing or invalid token")
            : await def.handle({
                params: match.slice(1).map(decodeURIComponent),
                query: url.searchParams,
                body,
                raw: raw ?? "",
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
      routes.push({ method, key: `${method} ${path}`, pattern, auth, handle });
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

    // Search.
    const searchStatus = (): SearchStatus => {
      const order =
        (w.settings.settings.search_order as string[] | undefined) ??
        w.settings.defaults.search_order;
      const limits = {
        ...w.settings.defaults.search_limits,
        ...((w.settings.settings.search_limits as Record<string, SearchLimit> | undefined) ?? {}),
      };
      const keys = new Map(w.search.keys.map((k) => [k.name, k.set]));
      return {
        ...w.search,
        order,
        backends: w.search.backends.map((b) => {
          const keySet = b.key ? (keys.get(b.key) ?? false) : false;
          const limit = b.bucket ? (limits[b.bucket] ?? {}) : {};
          const state = b.key_required && !keySet ? "no API key" : b.state;
          return { ...b, key_set: keySet, limit, state };
        }),
      };
    };
    on("GET", "/api/search/status", () => ok(searchStatus()));
    on("PUT", "/api/search/keys/{name}", ({ params, body }) => {
      const row = w.search.keys.find((k) => k.name === params[0]);
      if (!row) return fail(404, "not_found", `no search key named ${params[0] ?? ""}`);
      const key = str(body.key);
      row.set = key !== "";
      if (key.length >= 16) row.hint = key.slice(-4);
      else delete row.hint;
      row.updated_at = now();
      return ok({ keys: w.search.keys });
    });
    on("POST", "/api/search", ({ body }) => {
      const query = str(body.query);
      const source = str(body.source) || "web";
      const results = [
        { title: `${query}: official documentation`, url: "https://docs.example.com/" },
        { title: `${query} on GitHub`, url: "https://github.com/example/project" },
      ];
      return ok({
        text: results.map((r, i) => `${String(i + 1)}. ${r.title}\n   ${r.url}`).join("\n"),
        is_error: false,
        details: {
          source,
          query,
          count: results.length,
          ...(source === "web" ? { providers: ["searxng"] } : {}),
          results,
          ms: 212,
        },
      });
    });

    // Providers and models.
    on("GET", "/api/providers", () => ok({ providers: w.providers, kinds: w.providerKinds }));
    on("POST", "/api/providers/probe", () => ok({ models: w.probeModels }));
    // As internal/server/providers.go validates a provider.
    const providerProblem = (name: string, baseUrl: string, id?: string): Reply | undefined => {
      if (name === "") return fail(400, "invalid_request", "name must be 1 to 64 characters");
      if (baseUrl === "") {
        return fail(
          400,
          "invalid_request",
          "base_url is required, such as https://api.openai.com/v1",
        );
      }
      let url: URL;
      try {
        url = new URL(baseUrl);
      } catch {
        url = new URL("invalid:");
      }
      if (url.protocol !== "http:" && url.protocol !== "https:") {
        return fail(
          400,
          "invalid_request",
          "base_url must be an http or https URL, such as https://api.openai.com/v1",
        );
      }
      if (url.username !== "" || url.password !== "") {
        return fail(
          400,
          "invalid_request",
          "base_url must not contain credentials; put the key in api_key",
        );
      }
      if (url.search !== "" || url.hash !== "" || baseUrl.includes("?") || baseUrl.includes("#")) {
        return fail(400, "invalid_request", "base_url must not contain a query string or fragment");
      }
      if (w.providers.some((p) => p.name === name && p.id !== id)) {
        return fail(409, "conflict", `a provider named "${name}" already exists`);
      }
      return undefined;
    };
    const keyHint = (key: string) => (key.length >= 16 ? { api_key_hint: key.slice(-4) } : {});

    on("POST", "/api/providers", ({ body }) => {
      const problem = providerProblem(str(body.name).trim(), str(body.base_url).trim());
      if (problem) return problem;
      const key = str(body.api_key);
      const row: Provider = {
        id: this.nextId("prov"),
        name: str(body.name),
        kind: str(body.kind) || "openai",
        base_url: str(body.base_url),
        api_key_set: key !== "",
        ...keyHint(key),
        created_at: now(),
        updated_at: now(),
      };
      w.providers.push(row);
      return ok(row, 201);
    });
    on("PATCH", "/api/providers/{id}", ({ params, body }) => {
      const row = find(w.providers, params[0], "provider");
      if ("status" in row) return row;
      const problem = providerProblem(
        typeof body.name === "string" ? body.name.trim() : row.name,
        typeof body.base_url === "string" ? body.base_url.trim() : row.base_url,
        row.id,
      );
      if (problem) return problem;
      if (typeof body.name === "string") row.name = body.name.trim();
      if (typeof body.base_url === "string") {
        // As the harness does: a new URL without a new key clears the key.
        if (body.base_url.trim() !== row.base_url && typeof body.api_key !== "string") {
          row.api_key_set = false;
          delete row.api_key_hint;
        }
        row.base_url = body.base_url.trim();
      }
      if (typeof body.api_key === "string") {
        row.api_key_set = body.api_key !== "";
        delete row.api_key_hint;
        Object.assign(row, keyHint(body.api_key));
      }
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
        reasoning_effort: str(body.reasoning_effort),
        reasoning_efforts: strings(body.reasoning_efforts),
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

    // Files, commit, and push: the Files, Terminal, and Changes panels.
    const running = (id: string | undefined) => {
      const row = find(w.workspaces, id, "workspace");
      if ("status" in row) return row;
      if (row.state !== "running") return fail(409, "conflict", "workspace is not running");
      return row;
    };
    const filesOf = (id: string) => (w.files[id] ??= {});
    const entry = (id: string, path: string) => {
      const files = filesOf(id);
      const isDir = !(path in files);
      const content = files[path] ?? "";
      return {
        name: path.split("/").at(-1) ?? path,
        path,
        size: isDir ? 4096 : Buffer.byteLength(content),
        mode: isDir ? 0o755 : 0o644,
        mod_time: minutesAgo(30),
        is_dir: isDir,
      };
    };
    const diffOf = (row: Workspace) =>
      (w.diffs[row.id] ??= {
        workspace_id: row.id,
        base_commit: row.base_commit,
        diff: "",
        status: "",
      });
    const changed = (row: Workspace) => {
      row.updated_at = now();
      this.emit(
        this.event("workspace.state", `workspace:${row.id}`, {
          workspace_id: row.id,
          project_id: row.project_id,
          state: row.state,
        }),
      );
    };
    on("GET", "/api/workspaces/{id}/files", ({ params, query }) => {
      const row = running(params[0]);
      if ("status" in row) return row;
      const dir = query.get("path") ?? "";
      const prefix = dir === "" ? "" : `${dir}/`;
      const children = new Set<string>();
      for (const path of Object.keys(filesOf(row.id))) {
        if (!path.startsWith(prefix)) continue;
        const name = path.slice(prefix.length).split("/")[0] ?? "";
        if (name !== "") children.add(prefix + name);
      }
      if (dir !== "" && children.size === 0) return fail(404, "not_found", "no such directory");
      const entries = [...children].map((path) => entry(row.id, path));
      entries.sort((a, b) =>
        a.is_dir !== b.is_dir
          ? a.is_dir
            ? -1
            : 1
          : a.name < b.name
            ? -1
            : a.name > b.name
              ? 1
              : 0,
      );
      return ok({ entries });
    });
    on("GET", "/api/workspaces/{id}/file", ({ params, query }) => {
      const row = running(params[0]);
      if ("status" in row) return row;
      const path = query.get("path") ?? "";
      const content = filesOf(row.id)[path];
      if (content === undefined) return fail(404, "not_found", "no such file");
      const size = Buffer.byteLength(content);
      const binary = content.includes("\u0000");
      const tooLarge = size > 2 << 20;
      return ok({
        path,
        size,
        binary,
        too_large: tooLarge,
        content: binary || tooLarge ? "" : content,
      });
    });
    on("PUT", "/api/workspaces/{id}/file", ({ params, query, raw }) => {
      const row = running(params[0]);
      if ("status" in row) return row;
      const path = query.get("path") ?? "";
      if (path === "" || path.startsWith("/") || path.split("/").includes("..")) {
        return fail(403, "forbidden", "path escapes the workspace");
      }
      const files = filesOf(row.id);
      const isNew = !(path in files);
      files[path] = raw;
      const diff = diffOf(row);
      if (!diff.status.split("\n").some((line) => line.slice(3) === path)) {
        diff.status += `${isNew ? "??" : " M"} ${path}\n`;
      }
      changed(row);
      return ok(entry(row.id, path));
    });
    on("POST", "/api/workspaces/{id}/commit", ({ params, body }) => {
      const row = running(params[0]);
      if ("status" in row) return row;
      if (str(body.message).trim() === "") {
        return fail(400, "invalid_request", "message is required");
      }
      const diff = diffOf(row);
      if (diff.status.trim() === "") return fail(409, "conflict", "nothing to commit");
      diff.status = "";
      changed(row);
      return ok({ commit: mockCommit });
    });
    on("POST", "/api/workspaces/{id}/push", ({ params, body }) => {
      const row = running(params[0]);
      if ("status" in row) return row;
      const upstream = body.upstream === true;
      const project = w.projects.find((p) => p.id === row.project_id);
      if (upstream && (project?.remote_url ?? "") === "") {
        return fail(400, "invalid_request", "the project has no remote to push to");
      }
      changed(row);
      return ok({ branch: row.branch, commit: mockCommit, upstream_pushed: upstream });
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
    // The harness measures usage and generation time and reports both; the
    // mock counts words and pauses, so a screenshot shows a meter with the
    // shape of a real one.
    let outputTokens = 0;
    let generationMs = 0;
    const progress = () => {
      generationMs += this.stepDelayMs;
      send("turn.progress", {
        usage: {
          input_tokens: 1842,
          output_tokens: outputTokens,
          total_tokens: 1842 + outputTokens,
        },
        context: {
          input_tokens: 1842,
          output_tokens: outputTokens,
          total_tokens: 1842 + outputTokens,
        },
        generation_ms: generationMs,
        context_window: 400_000,
      });
    };
    let reasoning = "";
    // The endpoint decides how a turn ends; a cut-off step changes it from
    // the model's own "stop" to the reason it ran out of room.
    let stopReason = "stop";
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
        if ("think" in step) {
          const words = step.think.split(/(?<= )/);
          const chunk = Math.max(1, Math.ceil(words.length / 3));
          for (let i = 0; i < words.length; i += chunk) {
            const part = words.slice(i, i + chunk).join("");
            send("reasoning.delta", { text: part });
            outputTokens += words.slice(i, i + chunk).length;
            progress();
            await pause();
          }
          // Reasoning belongs to the assistant message the model then writes.
          reasoning = step.think;
        } else if ("say" in step) {
          // Stream the prose in a few chunks so a mid-stream screenshot shows a partial reply.
          const words = step.say.split(/(?<= )/);
          const chunk = Math.max(1, Math.ceil(words.length / 4));
          for (let i = 0; i < words.length; i += chunk) {
            send("message.delta", { text: words.slice(i, i + chunk).join("") });
            outputTokens += words.slice(i, i + chunk).length;
            progress();
            await pause();
          }
          this.append(session, {
            role: "assistant",
            content: step.say,
            ...(reasoning === "" ? {} : { reasoning }),
          });
          reasoning = "";
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
        } else if ("cutOff" in step) {
          stopReason = step.cutOff;
          break;
        } else {
          this.busy -= 1;
          await new Promise<void>(() => undefined);
        }
      }
      await pause();
      if (run.state !== "running") return;
      this.finish(run, "done");
      const usage = {
        input_tokens: 1842,
        output_tokens: Math.max(outputTokens, 311),
        total_tokens: 1842 + Math.max(outputTokens, 311),
      };
      send("turn.end", {
        stop_reason: stopReason,
        usage,
        context: usage,
        generation_ms: Math.max(generationMs, 4000),
        context_window: 400_000,
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
