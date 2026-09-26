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
  ContentDetail,
  Elicitation,
  ElicitationAnswer,
  Entry,
  MCPServerDetails,
  Message,
  Model,
  Project,
  Provider,
  Run,
  Sandbox,
  SearchLimit,
  SearchStatus,
  Session,
  ThinkingSwitch,
  Workspace,
} from "../../src/api/types.ts";
import { check, contract, matchRoute } from "./contract.ts";
import {
  inheritedProfile,
  previewContext,
  profileBody,
  profilesBody,
  record,
  recordedContext,
  samplingProblem,
  sessionConfiguration,
  settingsOf,
  syncSession,
} from "./profiles.ts";
import type { ConfigurationDraft } from "./profiles.ts";
import {
  entryKind,
  fixedNow,
  minutesAgo,
  pathOf,
  sessionTools,
  syncMCPTools,
  syncSystem,
} from "./world.ts";
import { defaultSandbox } from "./world.ts";
import type { ReplyStep, World } from "./world.ts";

/** mockToken is the bearer token the mock harness issues and accepts. */
export const mockToken = "mock-token";

/** wrongPassword is the one password sign-in refuses, to show the error. */
export const wrongPassword = "wrong";

/** Reply is how the mock answers a request: a status and a JSON body, if any. */
export type Reply = { status: number; body?: unknown };

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

/** noEgressControl is the harness's refusal of restricted egress it cannot give. */
const noEgressControl =
  "restricting a sandbox's network needs the internal sandbox network, which this harness is not configured with";

/** isObject narrows a JSON value to an object. */
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** isSandbox narrows a request body to a sandbox. */
function isSandbox(value: unknown): value is Sandbox {
  return isObject(value) && isObject(value.limits) && isObject(value.egress);
}

/** restricted reports whether a sandbox keeps its workspace off the open network. */
function restricted(sandbox: Sandbox): boolean {
  return sandbox.egress.mode !== "open";
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** strings narrows a JSON body field to the list of strings it should be. */
function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

/** thinkingSwitch narrows a JSON body field to a switch, standard when absent. */
function thinkingSwitch(value: unknown): ThinkingSwitch {
  return value === "chat_template_kwargs" || value === "thinking" ? value : "reasoning_effort";
}

export class MockHarness {
  readonly world: World;
  /** requests lists every API call in order. */
  readonly requests: RequestLog[] = [];
  /** unhandled lists API calls no route matched: a gap in the mock. */
  readonly unhandled: string[] = [];
  /**
   * contractBreaks lists what the page sent, or the mock answered, that
   * docs/api/contract.json says the harness would refuse or never send.
   */
  readonly contractBreaks: string[] = [];
  private readonly sockets = new Set<Socket>();
  private readonly routes: RouteDef[];
  private readonly stepDelayMs: number;
  private busy = 0;
  private seq = 1000;
  private readonly answers = new Map<string, (answer: string) => void>();
  private readonly elicitAnswers = new Map<string, (answer: ElicitationAnswer) => void>();
  /** authorizations maps an OAuth state the mock handed out to the server it authorizes. */
  private readonly authorizations = new Map<string, string>();
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
    this.checkEvent(event);
    const frame = JSON.stringify(event);
    for (const socket of this.sockets) {
      if (event.type === "bus.dropped" || socket.topics.has(event.topic)) socket.ws.send(frame);
    }
  }

  /** event builds an envelope stamped with the fixed clock. */
  event(type: EventType, topic: string, payload?: unknown): EikaEvent {
    return { type, topic, time: fixedNow, payload };
  }

  /** checkEvent records an event whose payload the harness would never send. */
  private checkEvent(event: EikaEvent): void {
    const c = contract();
    if (!(event.type in c.events)) {
      this.contractBreaks.push(`event ${event.type}: not in the contract`);
      return;
    }
    const shape = c.events[event.type];
    const problems = shape
      ? check(shape, event.payload, { types: c.types })
      : event.payload === undefined
        ? []
        : ["$: a payload, want none"];
    for (const p of problems) this.contractBreaks.push(`event ${event.type}: ${p}`);
  }

  /**
   * checkRequest is how the harness's decoder would answer a request body:
   * undefined when it accepts it, or the 400 it refuses it with. A refusal is
   * recorded, since the page sent something the harness does not take.
   */
  private checkRequest(key: string, method: string, path: string, raw: string): Reply | undefined {
    const route = matchRoute(contract(), method, path);
    if (route === undefined) {
      this.contractBreaks.push(`${key}: not in the contract`);
      return undefined;
    }
    if (route.route.request === undefined) return undefined;
    let problems: string[];
    try {
      problems =
        raw === ""
          ? ["$: no body"]
          : check(route.route.request, JSON.parse(raw), { request: true });
    } catch {
      problems = ["$: not JSON"];
    }
    if (problems.length === 0) return undefined;
    for (const p of problems) this.contractBreaks.push(`${key} request: ${p}`);
    return fail(400, "invalid_request", `decode request body: ${problems.join("; ")}`);
  }

  /** checkReply records a reply the harness would never give to method and path. */
  private checkReply(key: string, method: string, path: string, reply: Reply): void {
    const c = contract();
    const route = matchRoute(c, method, path)?.route;
    if (route === undefined) return;
    const record = (problems: string[]) => {
      for (const p of problems) this.contractBreaks.push(`${key} response: ${p}`);
    };
    if (reply.status < 300) {
      if (reply.status !== route.status) {
        record([`status ${String(reply.status)}, want ${String(route.status)}`]);
      }
      if (route.response === undefined) {
        if (reply.body !== undefined) record(["a body, want none"]);
      } else {
        record(check(route.response, reply.body, { types: c.types }));
      }
      return;
    }
    // A bare failure is a proxy's, not the harness's.
    if (reply.body === undefined) return;
    record(check(c.error, reply.body, { types: c.types }));
    const code = (reply.body as { error?: { code?: unknown } }).error?.code;
    if (typeof code !== "string" || !c.error_codes.includes(code)) {
      record([`error code ${String(code)} is not one of ${c.error_codes.join(", ")}`]);
    }
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
    for (const e of this.world.eventsOnConnect) {
      this.checkEvent(e);
      ws.send(JSON.stringify(e));
    }
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
        const event = this.sessionMessage(session, entry);
        this.checkEvent(event);
        socket.ws.send(JSON.stringify(event));
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
    this.busy += 1;
    try {
      const reply = await this.answer(
        request.method(),
        request.url(),
        request.headers().authorization,
        request.postData() ?? "",
      );
      if (reply === "hang") return;
      if (reply === "network") {
        await route.abort("connectionrefused");
        return;
      }
      await route.fulfill(
        reply.body === undefined
          ? { status: reply.status, body: "" }
          : { status: reply.status, json: reply.body },
      );
    } finally {
      this.busy -= 1;
    }
  }

  /**
   * answer is the mock's reply to one API request, or the failure that
   * leaves it without one: `hang` never answers, `network` never connects.
   * The page reaches it through install; a test may call it directly.
   */
  async answer(
    method: string,
    href: string,
    authorization: string | undefined,
    raw: string,
  ): Promise<Reply | "hang" | "network"> {
    const url = new URL(href, "http://mock.invalid");
    const path = url.pathname;
    let body: Record<string, unknown> = {};
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
      if (failure === "hang" || failure === "network") {
        // Left unanswered on purpose; the page is waiting, not the mock.
        this.requests.push({ method, path, status: 0 });
        return failure;
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
      reply =
        def.auth && authorization !== `Bearer ${mockToken}`
          ? fail(401, "unauthorized", "missing or invalid token")
          : (this.checkRequest(def.key, method, path, raw) ??
            (await def.handle({
              params: match.slice(1).map(decodeURIComponent),
              query: url.searchParams,
              body,
              raw,
            })));
      this.checkReply(def.key, method, path, reply);
      break;
    }
    if (!reply) {
      this.unhandled.push(`${method} ${path}`);
      reply = fail(404, "not_found", `mock harness has no route for ${method} ${path}`);
    }
    syncSystem(this.world);
    this.requests.push({ method, path, status: reply.status, body: reply.body });
    return reply;
  }

  /** routeKeys are the routes the mock serves, each as `METHOD /path`, with whether it needs a token. */
  routeKeys(): { key: string; auth: boolean }[] {
    return this.routes.map((r) => ({ key: r.key, auth: r.auth }));
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
        return ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }, 201);
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
      const named = body.default_profile;
      if (typeof named === "string" && named !== "" && !w.profiles.some((p) => p.id === named)) {
        return fail(
          400,
          "invalid_request",
          `default_profile names "${named}", which is not a profile`,
        );
      }
      for (const [key, value] of Object.entries(body)) {
        if (value === null) Reflect.deleteProperty(w.settings.settings, key);
        else w.settings.settings[key] = value;
      }
      if (typeof body.default_model === "string") w.defaultModel = body.default_model;
      // A new default profile can change what every session offers.
      for (const session of w.sessions) syncSession(w, session);
      return ok(w.settings);
    });
    on("GET", "/api/system", () => ok(w.system));
    on("GET", "/api/tools", () => ok({ tools: w.tools }));

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
        thinking_switch: thinkingSwitch(body.thinking_switch),
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
      w.sessions = w.sessions.filter(
        (s) => s.workspace_id === undefined || !gone.has(s.workspace_id),
      );
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
        sandbox: isSandbox(body.sandbox) ? body.sandbox : newSandbox(),
        created_at: now(),
        updated_at: now(),
      };
      if (restricted(row.sandbox) && !w.system.sandbox.egress_control) {
        return fail(400, "invalid_request", noEgressControl);
      }
      w.workspaces.unshift(row);
      return ok(row, 201);
    });
    // A new workspace's sandbox: the settings' defaults, as the harness reads them.
    const newSandbox = (): Sandbox => {
      const base = defaultSandbox();
      const limits = w.settings.settings.sandbox_limits;
      const egress = w.settings.settings.sandbox_egress;
      return {
        limits: isObject(limits)
          ? { ...w.settings.defaults.sandbox_limits, ...limits }
          : w.settings.defaults.sandbox_limits,
        egress: isObject(egress)
          ? { ...w.settings.defaults.sandbox_egress, ...egress }
          : w.settings.defaults.sandbox_egress,
        ports: base.ports,
      };
    };
    on("PUT", "/api/workspaces/{id}/sandbox", ({ params, body }) => {
      const row = find(w.workspaces, params[0], "workspace");
      if ("status" in row) return row;
      if (!isSandbox(body))
        return fail(400, "invalid_request", "a sandbox has limits, egress, and ports");
      if (restricted(body) && !w.system.sandbox.egress_control) {
        return fail(400, "invalid_request", noEgressControl);
      }
      row.sandbox = { ...body, ports: body.ports ?? [] };
      row.updated_at = now();
      this.emit(
        this.event("workspace.state", `workspace:${row.id}`, {
          workspace_id: row.id,
          project_id: row.project_id,
          state: row.state,
        }),
      );
      return ok(row);
    });
    on("GET", "/api/workspaces/{id}/usage", ({ params }) => {
      const row = find(w.workspaces, params[0], "workspace");
      if ("status" in row) return row;
      if (row.state !== "running") {
        return fail(409, "conflict", `workspace ${row.id} is ${row.state}, not running`);
      }
      const limits = row.sandbox.limits;
      const memoryLimit =
        limits.memory_mb > 0 ? limits.memory_mb * 2 ** 20 : w.system.sandbox.memory_bytes;
      return ok({
        cpu_percent: 37.5,
        cpus: limits.cpus > 0 ? limits.cpus : w.system.sandbox.cpus,
        memory_bytes: 612 * 2 ** 20,
        memory_limit_bytes: memoryLimit,
        pids: 23,
        pids_limit: limits.pids,
        network_rx_bytes: 48_300_000,
        network_tx_bytes: 2_150_000,
        sampled_at: now(),
        blocked: w.blocked[row.id] ?? [],
      });
    });
    on("POST", "/api/workspaces/{id}/ports/{port}/preview", ({ params }) => {
      const row = find(w.workspaces, params[0], "workspace");
      if ("status" in row) return row;
      const port = Number(params[1]);
      if (!(row.sandbox.ports ?? []).some((p) => p.port === port)) {
        return fail(404, "not_found", `workspace ${row.id} does not forward port ${String(port)}`);
      }
      if (row.state !== "running") {
        return fail(409, "conflict", `workspace ${row.id} is ${row.state}, not running`);
      }
      return ok({
        url: `http://${String(port)}-${row.id}.localhost:8080/__eika/preview?ticket=mock`,
      });
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
      if (query.get("chats") === "true") {
        if (ws) return fail(400, "invalid_request", "a chat has no workspace");
        // Oldest first, as the harness lists them.
        const chats = w.sessions.filter((s) => s.workspace_id === undefined);
        return ok({ sessions: chats.toSorted((a, b) => a.created_at.localeCompare(b.created_at)) });
      }
      if (!ws) return ok({ sessions: w.sessions });
      const listed = w.sessions.filter((s) => s.workspace_id === ws);
      if (query.get("descendants") !== "true") return ok({ sessions: listed });
      // The forks and child agents those sessions led to, wherever they run,
      // as the harness's recursive listing returns them.
      const reached = new Set(listed.map((s) => s.id));
      for (let more = true; more;) {
        more = false;
        for (const s of w.sessions) {
          const parent = s.parent_session_id;
          if (parent !== undefined && reached.has(parent) && !reached.has(s.id)) {
            reached.add(s.id);
            more = true;
          }
        }
      }
      return ok({ sessions: w.sessions.filter((s) => reached.has(s.id)) });
    });
    on("POST", "/api/sessions", ({ body }) => {
      const chat = body.chat === true;
      if (chat && str(body.workspace_id) !== "") {
        return fail(400, "invalid_request", "a chat has no workspace");
      }
      const ws = chat ? undefined : find(w.workspaces, str(body.workspace_id), "workspace");
      if (ws !== undefined && "status" in ws) return ws;
      const row: Session = {
        id: this.nextId(chat ? "chat" : "ses"),
        ...(ws ? { workspace_id: ws.id } : {}),
        title: str(body.title) || (chat ? "New chat" : "New session"),
        kind: "user",
        tools: sessionTools(w, chat),
        overridden: false,
        created_at: now(),
        updated_at: now(),
      };
      syncSession(w, row);
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
        nodes: resumableEntries(w.entries[row.id] ?? []).map(({ entry: e, resumable }) => ({
          id: e.id,
          parent_id: e.parent_id,
          kind: e.kind,
          preview: preview(e.message),
          commit: e.commit,
          resumable,
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
      // An empty entry id clears the head, which is what rewinding to the
      // session's first message means: the next run starts a new root.
      if (str(body.entry_id) === "") {
        delete row.head_entry_id;
        return ok(row);
      }
      const entry = find(w.entries[row.id] ?? [], str(body.entry_id), "entry");
      if ("status" in entry) return entry;
      const refused = midTurn(w.entries[row.id] ?? [], entry.id);
      if (refused) return refused;
      row.head_entry_id = entry.id;
      return ok(row);
    });
    on("POST", "/api/sessions/{id}/fork", ({ params, body }) => {
      const source = find(w.sessions, params[0], "session");
      if ("status" in source) return source;
      const refused = midTurn(w.entries[source.id] ?? [], str(body.entry_id));
      if (refused) return refused;
      const row: Session = {
        id: this.nextId(source.workspace_id === undefined ? "chat" : "ses"),
        ...(source.workspace_id === undefined ? {} : { workspace_id: source.workspace_id }),
        title: str(body.title) || `${source.title} (fork)`,
        kind: "fork",
        tools: [...source.tools],
        overridden: source.overridden,
        ...(source.profile_id === undefined ? {} : { profile_id: source.profile_id }),
        parent_session_id: source.id,
        head_entry_id: str(body.entry_id),
        created_at: now(),
        updated_at: now(),
      };
      // A fork runs as its source is configured to.
      const overrides = w.overrides[source.id];
      if (overrides !== undefined) w.overrides[row.id] = overrides;
      const choice = w.toolChoices[source.id];
      if (choice !== undefined) w.toolChoices[row.id] = [...choice];
      w.sessions.unshift(row);
      w.entries[row.id] = [...(w.entries[source.id] ?? [])];
      return ok(row, 201);
    });
    on("PUT", "/api/sessions/{id}/tools", ({ params, body }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      if (body.tools === null) {
        // Null clears the session's choice, and its profile's applies.
        Reflect.deleteProperty(w.toolChoices, row.id);
        syncSession(w, row);
        return ok(row);
      }
      if (!Array.isArray(body.tools)) return fail(400, "invalid_request", "tools is required");
      const names = [...new Set(body.tools.map((name) => str(name)))].sort();
      for (const name of names) {
        const server = /^mcp__(.+)__\*$/.exec(name)?.[1];
        if (server !== undefined) {
          if (!w.mcpServers.some((d) => d.server.name === server)) {
            return fail(400, "invalid_request", `there is no MCP server "${server}"`);
          }
          continue;
        }
        const known = w.tools.find((t) => t.name === name);
        if (!known) return fail(400, "invalid_request", `there is no tool "${name}"`);
        if (row.workspace_id === undefined && known.needs_workspace) {
          return fail(400, "invalid_request", `${name} needs a workspace, and a chat has none`);
        }
      }
      w.toolChoices[row.id] = names;
      syncSession(w, row);
      row.updated_at = now();
      return ok(row);
    });
    on("GET", "/api/sessions/{id}/configuration", ({ params, query }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const draft: ConfigurationDraft = {};
      const profileId = query.get("profile_id");
      const modelId = query.get("model_id");
      if (profileId !== null) {
        if (profileId !== "" && !w.profiles.some((p) => p.id === profileId)) {
          return fail(400, "invalid_request", `profile ${profileId} does not exist`);
        }
        draft.profileId = profileId;
      }
      if (modelId !== null) {
        if (modelId !== "" && !w.models.some((m) => m.id === modelId)) {
          return fail(400, "invalid_request", `model ${modelId} does not exist`);
        }
        draft.modelId = modelId;
      }
      return ok(sessionConfiguration(w, row, draft));
    });
    on("PUT", "/api/sessions/{id}/profile", ({ params, body }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const id = str(body.profile_id);
      if (id === "") Reflect.deleteProperty(row, "profile_id");
      else if (w.profiles.some((p) => p.id === id)) row.profile_id = id;
      else return fail(400, "invalid_request", `profile ${id} does not exist`);
      syncSession(w, row);
      return ok(sessionConfiguration(w, row));
    });
    on("PUT", "/api/sessions/{id}/overrides", ({ params, body }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const settings = settingsOf(body);
      const problem = samplingProblem(settings.sampling);
      if (problem !== undefined) return fail(400, "invalid_request", problem);
      w.overrides[row.id] = settings;
      syncSession(w, row);
      return ok(sessionConfiguration(w, row));
    });
    on("GET", "/api/sessions/{id}/context", ({ params, query }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      const model = query.get("model") ?? "";
      if (model !== "" && !w.models.some((m) => m.name === model)) {
        return fail(400, "invalid_request", `unknown model "${model}"`);
      }
      return ok(previewContext(w, row, model));
    });
    on("GET", "/api/sessions/{id}/requests", ({ params }) => {
      const row = find(w.sessions, params[0], "session");
      if ("status" in row) return row;
      return ok({ session_id: row.id, requests: (w.requests[row.id] ?? []).map((r) => r.record) });
    });
    on("GET", "/api/sessions/{id}/requests/{request_id}", ({ params }) => {
      const found = (w.requests[params[0] ?? ""] ?? []).find((r) => r.record.id === params[1]);
      if (!found) return fail(404, "not_found", "model request not found");
      return ok(recordedContext(found));
    });

    // Profiles.
    const profileInput = (body: Record<string, unknown>) => {
      const name = str(body.name).trim();
      if (name === "") return fail(400, "invalid_request", "name must be 1 to 64 characters");
      const settings = settingsOf(body);
      const problem = samplingProblem(settings.sampling);
      if (problem !== undefined) return fail(400, "invalid_request", problem);
      if (settings.model_id !== undefined && !w.models.some((m) => m.id === settings.model_id)) {
        return fail(400, "invalid_request", `model ${settings.model_id} does not exist`);
      }
      const tools = Array.isArray(body.tools) ? [...new Set(strings(body.tools))].sort() : null;
      return { name, description: str(body.description).trim(), ...settings, tools };
    };
    on("GET", "/api/profiles", () => ok(profilesBody(w)));
    on("GET", "/api/profiles/inherited", ({ query }) => {
      const modelId = query.get("model_id") ?? "";
      if (modelId !== "" && !w.models.some((m) => m.id === modelId)) {
        return fail(400, "invalid_request", `model ${modelId} does not exist`);
      }
      return ok(inheritedProfile(w, modelId));
    });
    on("POST", "/api/profiles", ({ body }) => {
      const input = profileInput(body);
      if ("status" in input) return input;
      if (w.profiles.some((p) => p.name === input.name)) {
        return fail(409, "conflict", `a profile named "${input.name}" already exists`);
      }
      const row = { id: this.nextId("prof"), ...input, created_at: now(), updated_at: now() };
      w.profiles.push(row);
      return ok(profileBody(w, row), 201);
    });
    on("PUT", "/api/profiles/{id}", ({ params, body }) => {
      const row = find(w.profiles, params[0], "profile");
      if ("status" in row) return row;
      const input = profileInput(body);
      if ("status" in input) return input;
      if (w.profiles.some((p) => p.name === input.name && p.id !== row.id)) {
        return fail(409, "conflict", `a profile named "${input.name}" already exists`);
      }
      const updated = { ...input, id: row.id, created_at: row.created_at, updated_at: now() };
      w.profiles = w.profiles.map((p) => (p.id === row.id ? updated : p));
      for (const session of w.sessions) syncSession(w, session);
      return ok(profileBody(w, updated));
    });
    on("DELETE", "/api/profiles/{id}", ({ params }) => {
      const row = find(w.profiles, params[0], "profile");
      if ("status" in row) return row;
      if (w.profiles.length === 1) {
        return fail(
          409,
          "conflict",
          `profile ${row.id} is the last one, and a run always needs a profile`,
        );
      }
      w.profiles = w.profiles.filter((p) => p.id !== row.id);
      for (const session of w.sessions) {
        if (session.profile_id === row.id) Reflect.deleteProperty(session, "profile_id");
        syncSession(w, session);
      }
      return { status: 204 };
    });

    on("GET", "/api/sessions/{id}/agents", ({ params }) =>
      ok({ session_id: params[0], agents: [] }),
    );

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
        elicitations: w.elicitations.filter((e) => e.session_id === id),
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

    // MCP servers, as internal/server/mcp.go serves them. An authorization
    // server is not mocked: the authorization URL is the callback itself,
    // as an authorization server that approves at once would redirect.
    const mcpNamePattern = /^[A-Za-z0-9-]+(_[A-Za-z0-9-]+)*$/;
    const mcpServer = (id: string | undefined) => {
      const row = w.mcpServers.find((d) => d.server.id === id);
      return row ?? fail(404, "not_found", "mcp server not found");
    };
    const mcpChanged = (d: MCPServerDetails, state: string = d.server.state) => {
      syncMCPTools(w);
      d.server.updated_at = now();
      this.emit(
        this.event("mcp.server", "global", {
          server_id: d.server.id,
          name: d.server.name,
          state,
          ...(d.server.error === undefined ? {} : { error: d.server.error }),
        }),
      );
    };
    const mcpNameProblem = (name: string, id?: string): Reply | undefined => {
      if (name === "" || name.length > 32 || !mcpNamePattern.test(name)) {
        return fail(
          400,
          "invalid_request",
          "name must be 1 to 32 letters, digits, hyphens, and single underscores between them",
        );
      }
      if (w.mcpServers.some((d) => d.server.name === name && d.server.id !== id)) {
        return fail(409, "conflict", `an MCP server named ${name} exists`);
      }
      return undefined;
    };
    const mcpURLProblem = (raw: string): Reply | undefined => {
      let url: URL;
      try {
        url = new URL(raw);
      } catch {
        return fail(400, "invalid_request", "url must be an http or https URL");
      }
      if ((url.protocol !== "http:" && url.protocol !== "https:") || url.hash !== "") {
        return fail(400, "invalid_request", "url must be an http or https URL with no fragment");
      }
      return undefined;
    };
    const names = (value: unknown) =>
      typeof value === "object" && value !== null ? Object.keys(value).sort() : [];
    const connectHTTP = (d: MCPServerDetails) => {
      if (!d.server.enabled) {
        d.server.state = "disabled";
      } else if (d.auth.challenged && !d.auth.authorized) {
        d.server.state = "unauthorized";
        d.server.error = "the server needs authorization";
      } else {
        d.server.state = "connected";
        delete d.server.error;
      }
    };
    on("GET", "/api/mcp/servers", () => ok({ servers: w.mcpServers.map((d) => d.server) }));
    on("POST", "/api/mcp/servers", ({ body }) => {
      const name = str(body.name);
      const problem = mcpNameProblem(name);
      if (problem) return problem;
      const kind = str(body.kind);
      if (kind !== "http" && kind !== "stdio") {
        return fail(400, "invalid_request", "kind must be http or stdio");
      }
      if (kind === "http") {
        const bad = mcpURLProblem(str(body.url));
        if (bad) return bad;
      } else if (str(body.command) === "") {
        return fail(400, "invalid_request", "a stdio server needs a command");
      }
      const d: MCPServerDetails = {
        server: {
          id: this.nextId("mcp"),
          name,
          kind,
          url: kind === "http" ? str(body.url) : "",
          header_names: names(body.headers),
          command: str(body.command),
          args: strings(body.args),
          env_names: names(body.env),
          enabled: body.enabled !== false,
          disabled_tools: strings(body.disabled_tools).sort(),
          oauth_client_id: str(body.oauth_client_id),
          oauth_client_secret_set: str(body.oauth_client_secret) !== "",
          state: "idle",
          workspaces: [],
          created_at: now(),
          updated_at: now(),
        },
        tools: [],
        excluded_tools: [],
        resources: [],
        resource_templates: [],
        prompts: [],
        list_errors: {},
        logs: [],
        auth: { challenged: false, authorized: false, has_refresh_token: false },
      };
      if (!d.server.enabled) d.server.state = "disabled";
      else if (kind === "http") connectHTTP(d);
      w.mcpServers.push(d);
      w.mcpServers.sort((a, b) => a.server.name.localeCompare(b.server.name));
      mcpChanged(d);
      return ok(d.server, 201);
    });
    on("GET", "/api/mcp/servers/{id}", ({ params }) => {
      const d = mcpServer(params[0]);
      return "status" in d ? d : ok(d);
    });
    on("PATCH", "/api/mcp/servers/{id}", ({ params, body }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      const s = d.server;
      if (typeof body.name === "string") {
        const problem = mcpNameProblem(body.name, s.id);
        if (problem) return problem;
      }
      if (typeof body.url === "string") {
        if (s.kind !== "http") return fail(400, "invalid_request", "a stdio server has no url");
        const bad = mcpURLProblem(body.url);
        if (bad) return bad;
      }
      if (typeof body.command === "string" && s.kind !== "stdio") {
        return fail(400, "invalid_request", "an http server has no command");
      }
      if (typeof body.name === "string") {
        // As the harness does: tool choices follow the server's new name.
        const from = `mcp__${s.name}__`;
        const to = `mcp__${body.name}__`;
        const rename = (choice: string[] | null) =>
          choice?.map((e) => (e.startsWith(from) ? to + e.slice(from.length) : e)) ?? null;
        for (const p of w.profiles) p.tools = rename(p.tools);
        for (const [id, choice] of Object.entries(w.toolChoices))
          w.toolChoices[id] = rename(choice) ?? choice;
        s.name = body.name;
        for (const t of d.tools ?? []) t.exposed_name = `mcp__${s.name}__${t.name}`;
      }
      if (typeof body.url === "string" && body.url !== s.url) {
        s.url = body.url;
        // As the harness does: the old URL's headers and tokens do not follow it.
        if (body.headers === undefined) s.header_names = [];
        d.auth = { ...d.auth, authorized: false, has_refresh_token: false };
      }
      if (body.headers !== undefined) s.header_names = names(body.headers);
      if (typeof body.command === "string") s.command = body.command;
      if (Array.isArray(body.args)) s.args = strings(body.args);
      if (body.env !== undefined) s.env_names = names(body.env);
      if (typeof body.enabled === "boolean") s.enabled = body.enabled;
      if (Array.isArray(body.disabled_tools)) {
        s.disabled_tools = strings(body.disabled_tools).sort();
        for (const t of d.tools ?? []) t.enabled = !s.disabled_tools.includes(t.name);
      }
      if (typeof body.oauth_client_id === "string") {
        s.oauth_client_id = body.oauth_client_id;
        if (body.oauth_client_id === "") s.oauth_client_secret_set = false;
      }
      if (typeof body.oauth_client_secret === "string") {
        s.oauth_client_secret_set = body.oauth_client_secret !== "";
      }
      if (!s.enabled) {
        s.state = "disabled";
        s.workspaces = [];
      } else if (s.kind === "http") {
        connectHTTP(d);
      } else if (s.state === "disabled") {
        s.state = "idle";
      }
      mcpChanged(d);
      return ok(s);
    });
    on("DELETE", "/api/mcp/servers/{id}", ({ params }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return { status: 204 };
      w.mcpServers = w.mcpServers.filter((x) => x !== d);
      mcpChanged(d, "removed");
      return { status: 204 };
    });
    on("POST", "/api/mcp/servers/{id}/connect", ({ params, body }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      if (!d.server.enabled) return fail(409, "conflict", "the server is off");
      if (d.server.kind === "stdio") {
        const id = str(body.workspace_id);
        if (id === "") {
          return fail(400, "invalid_request", "a stdio server runs in a workspace: name one");
        }
        const ws = running(id);
        if ("status" in ws) return ws;
        d.server.state = "connected";
        d.server.workspaces = [...new Set([...(d.server.workspaces ?? []), ws.id])];
      } else {
        connectHTTP(d);
      }
      (d.logs ??= []).push({
        time: now(),
        source: "eika",
        level: "info",
        text:
          d.server.state === "connected" ? "connected" : `did not connect: ${d.server.error ?? ""}`,
      });
      mcpChanged(d);
      return ok(d);
    });
    on("POST", "/api/mcp/servers/{id}/authorize", ({ params, body }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      if (d.server.kind !== "http") {
        return fail(400, "invalid_request", "a stdio server is not authorized with OAuth");
      }
      let redirect: URL;
      try {
        redirect = new URL(str(body.redirect_uri));
      } catch {
        return fail(400, "invalid_request", "redirect_uri must be an http or https URL");
      }
      if (redirect.pathname !== "/mcp/callback" || redirect.search !== "") {
        return fail(
          400,
          "invalid_request",
          "redirect_uri must be the /mcp/callback page of the web UI",
        );
      }
      const state = this.nextId("state");
      this.authorizations.set(state, d.server.id);
      const issuer = d.auth.issuer ?? "https://auth.example.com";
      redirect.search = new URLSearchParams({ code: "mock-code", state, iss: issuer }).toString();
      return ok({ authorization_url: redirect.toString() });
    });
    on("POST", "/api/mcp/oauth/callback", ({ body }) => {
      const id = this.authorizations.get(str(body.state));
      if (id === undefined) return fail(404, "not_found", "no authorization waits for that state");
      this.authorizations.delete(str(body.state));
      if (str(body.error) !== "") {
        return fail(400, "invalid_request", `the authorization server refused: ${str(body.error)}`);
      }
      const d = mcpServer(id);
      if ("status" in d) return d;
      d.auth = {
        ...d.auth,
        authorized: true,
        has_refresh_token: true,
        issuer: d.auth.issuer ?? "https://auth.example.com",
        expires_at: "2026-03-14T16:00:00.000Z",
        updated_at: now(),
        registration: d.auth.registration ?? "dynamic",
        client_id: d.auth.client_id ?? "eika-mock-client",
      };
      connectHTTP(d);
      mcpChanged(d);
      return ok({ server_id: d.server.id });
    });
    on("DELETE", "/api/mcp/servers/{id}/authorization", ({ params }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      d.auth = { ...d.auth, authorized: false, has_refresh_token: false };
      delete d.auth.expires_at;
      connectHTTP(d);
      mcpChanged(d);
      return { status: 204 };
    });
    on("POST", "/api/mcp/servers/{id}/resources/read", ({ params, body }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      const uri = str(body.uri);
      const found = (d.resources ?? []).find((r) => r.uri === uri);
      if (!found) return fail(400, "invalid_request", `read the resource: no resource ${uri}`);
      const content: ContentDetail = {
        type: "resource",
        uri,
        ...(found.mime_type === undefined ? {} : { mime_type: found.mime_type }),
        text: `# ${found.title ?? found.name}\n\n${found.description ?? ""}\n`,
      };
      return ok({ contents: [content] });
    });
    on("POST", "/api/mcp/servers/{id}/prompts/get", ({ params, body }) => {
      const d = mcpServer(params[0]);
      if ("status" in d) return d;
      const prompt = (d.prompts ?? []).find((p) => p.name === str(body.name));
      if (!prompt)
        return fail(400, "invalid_request", `get the prompt: no prompt ${str(body.name)}`);
      const given = (body.arguments ?? {}) as Record<string, unknown>;
      for (const a of prompt.arguments ?? []) {
        if (a.required && str(given[a.name]) === "") {
          return fail(400, "invalid_request", `get the prompt: ${a.name} is required`);
        }
      }
      const filled = Object.entries(given)
        .map(([k, v]) => `${k}: ${str(v)}`)
        .join("\n");
      return ok({
        ...(prompt.description === undefined ? {} : { description: prompt.description }),
        messages: [
          {
            role: "user",
            content: { type: "text", text: `${prompt.description ?? prompt.name}\n\n${filled}` },
          },
        ],
      });
    });
    on("POST", "/api/elicitations/{id}/answer", ({ params, body }) => {
      const answer = this.elicitAnswers.get(params[0] ?? "");
      if (!answer) return fail(404, "not_found", "nothing waits on that elicitation");
      const action = str(body.action);
      if (action !== "accept" && action !== "decline" && action !== "cancel") {
        return fail(400, "invalid_request", "action must be accept, decline, or cancel");
      }
      const elicitation = w.elicitations.find((e) => e.id === params[0]);
      if (body.content !== undefined && (action !== "accept" || elicitation?.mode !== "form")) {
        return fail(400, "invalid_request", "content goes only with an accepted form");
      }
      answer({
        action,
        ...(typeof body.content === "object" && body.content !== null
          ? { content: body.content as Record<string, unknown> }
          : {}),
      });
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
    this.world.elicitations = this.world.elicitations.filter((e) => e.run_id !== run.id);
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
    // A local engine that measures both of its phases, as llama.cpp does, so
    // the transcript's speed line has something true to state.
    const timings = () => ({
      prompt_tokens: 1842,
      prompt_ms: 420,
      decode_tokens: Math.max(outputTokens - 1, 1),
      decode_ms: Math.max(generationMs, 1),
      source: "endpoint" as const,
    });
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
        timings: timings(),
      });
    };
    let reasoning = "";
    // The endpoint decides how a turn ends; a cut-off step changes it from
    // the model's own "stop" to the reason it ran out of room.
    let stopReason = "stop";
    try {
      this.append(session, { role: "user", content: text });
      // What the model call is sent, which the harness records once the
      // response is in.
      const sent = previewContext(this.world, session, "");
      const sentAt = session.head_entry_id;
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
            metrics: {
              run_id: turn,
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
              timings: timings(),
            },
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
        } else if ("mcp" in step) {
          const callId = this.nextId("call");
          const name = `mcp__${step.mcp.server}__${step.mcp.tool}`;
          send("tool.call", { call_id: callId, name, arguments: step.args });
          this.append(session, {
            role: "assistant",
            tool_calls: [{ id: callId, name, arguments: step.args }],
          });
          let declined = "";
          if (step.elicit) {
            const elicitation: Elicitation = {
              id: this.nextId("eli"),
              session_id: session.id,
              run_id: run.id,
              call_id: callId,
              server: step.mcp.server,
              asked_at: fixedNow,
              ...step.elicit,
            };
            this.world.elicitations.push(elicitation);
            await pause();
            send("mcp.elicitation", {
              elicitation_id: elicitation.id,
              session_id: session.id,
              call_id: callId,
              server: elicitation.server,
              mode: elicitation.mode,
              message: elicitation.message,
              ...(elicitation.requested_schema === undefined
                ? {}
                : { requested_schema: elicitation.requested_schema }),
              ...(elicitation.url === undefined ? {} : { url: elicitation.url }),
            });
            // As with a question, waiting on a person is not the page's work.
            this.busy -= 1;
            const answer = await new Promise<ElicitationAnswer>((resolve) => {
              this.elicitAnswers.set(elicitation.id, resolve);
            });
            this.busy += 1;
            this.elicitAnswers.delete(elicitation.id);
            this.world.elicitations = this.world.elicitations.filter(
              (e) => e.id !== elicitation.id,
            );
            if (answer.action !== "accept") declined = `The user chose to ${answer.action}.`;
          }
          await pause();
          const content: ContentDetail[] =
            declined === "" ? step.content : [{ type: "text", text: declined }];
          const text = content
            .map((c) =>
              c.type === "text" || c.type === "resource"
                ? (c.text ?? "")
                : `[${c.type} ${c.mime_type ?? ""}: shown to the user, not to you]`,
            )
            .join("\n\n");
          send("tool.result", {
            call_id: callId,
            name,
            content: text,
            is_error: step.isError === true,
            details: {
              server: step.mcp.server,
              tool: step.mcp.tool,
              content,
              ...(step.structured === undefined ? {} : { structured_content: step.structured }),
              ...(step.isError === true ? { is_error: true } : {}),
            },
            duration_ms: 842,
          });
          this.append(session, {
            role: "tool",
            tool_call_id: callId,
            content: text,
            is_error: step.isError === true,
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
      record(this.world, session, {
        id: this.nextId("req"),
        runId: run.id,
        context: sent,
        outputTokens: Math.max(outputTokens, 311),
        ...(sentAt === undefined ? {} : { entryId: sentAt }),
      });
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
        timings: {
          prompt_tokens: 1842,
          prompt_ms: 420,
          decode_tokens: usage.output_tokens - 1,
          decode_ms: Math.max(generationMs, 4000),
          source: "endpoint" as const,
        },
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

/**
 * resumableEntries pairs each entry with whether a run can continue from it,
 * the way `internal/session` does: an entry is resumable when the path down
 * to it leaves no tool call unanswered.
 */
function resumableEntries(entries: Entry[]): { entry: Entry; resumable: boolean }[] {
  const pending = new Map<string, string[]>();
  return entries.map((entry) => {
    const parent = pending.get(entry.parent_id ?? "") ?? [];
    const message = entry.message;
    let left = parent;
    if (message.tool_calls && message.tool_calls.length > 0) {
      left = [...parent, ...message.tool_calls.map((c) => c.id)];
    } else if (message.role === "tool" && message.tool_call_id !== undefined) {
      left = parent.filter((id) => id !== message.tool_call_id);
    }
    pending.set(entry.id, left);
    return { entry, resumable: left.length === 0 };
  });
}

/**
 * midTurn is the refusal the harness answers with when a head or a fork names
 * an entry that leaves tool calls unanswered, and nothing when it does not.
 */
function midTurn(entries: Entry[], entryId: string): Reply | undefined {
  const found = resumableEntries(entries).find(({ entry }) => entry.id === entryId);
  if (found === undefined || found.resumable) return undefined;
  return fail(
    400,
    "invalid_request",
    `entry ${entryId} leaves tool calls unanswered, so a run cannot continue from it`,
  );
}
