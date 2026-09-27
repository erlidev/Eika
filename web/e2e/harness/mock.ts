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
  Compaction,
  ContentDetail,
  Elicitation,
  ElicitationAnswer,
  Entry,
  Message,
  MessageImage,
  Run,
  Session,
} from "../../src/api/types.ts";
import { check, contract, matchRoute } from "./contract.ts";
import { previewContext, record } from "./profiles.ts";
import { authRoutes, mockToken } from "./routes/auth.ts";
import { fail } from "./routes/context.ts";
import type { Handler, Reply, RouteContext } from "./routes/context.ts";
import { fileRoutes } from "./routes/files.ts";
import { mcpRoutes } from "./routes/mcp.ts";
import { profileRoutes } from "./routes/profiles.ts";
import { projectRoutes } from "./routes/projects.ts";
import { providerRoutes } from "./routes/providers.ts";
import { runRoutes } from "./routes/runs.ts";
import { searchRoutes } from "./routes/search.ts";
import { sessionRoutes } from "./routes/sessions.ts";
import { settingsRoutes } from "./routes/settings.ts";
import { workspaceRoutes } from "./routes/workspaces.ts";
import { entryKind, fixedNow, pathOf, syncSystem } from "./world.ts";
import type { ReplyStep, World } from "./world.ts";

export { mockToken };
export type { Reply } from "./routes/context.ts";

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

  /**
   * nameSession titles an untitled session after its first message, as the
   * harness does when the settings assign the session title task a model.
   * The mock's model titles a message with its first six words.
   */
  private nameSession(session: Session, text: string): void {
    const w = this.world;
    const assigned = w.settings.settings.utility_models;
    const model =
      typeof assigned === "object" && assigned !== null && "session_title" in assigned
        ? assigned.session_title
        : undefined;
    if (!w.untitled.includes(session.id) || !w.models.some((m) => m.name === model)) return;
    const first = (w.entries[session.id] ?? []).find((e) => e.kind === "user")?.message.content;
    session.title = (first ?? text).split(/\s+/).slice(0, 6).join(" ");
    session.updated_at = fixedNow;
    w.untitled = w.untitled.filter((id) => id !== session.id);
    this.emit(
      this.event("session.title", "global", {
        session_id: session.id,
        ...(session.workspace_id === undefined ? {} : { workspace_id: session.workspace_id }),
        title: session.title,
      }),
    );
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
      // A compaction entry's payload is the compaction, as the harness stores it.
      message: entry.kind === "compaction" ? entry.compaction : entry.message,
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
    const running = (id: string | undefined) => {
      const row = find(w.workspaces, id, "workspace");
      if ("status" in row) return row;
      if (row.state !== "running") return fail(409, "conflict", "workspace is not running");
      return row;
    };
    const ctx: RouteContext = {
      w,
      on,
      find,
      now,
      running,
      nextId: (prefix) => this.nextId(prefix),
      emit: (event) => {
        this.emit(event);
      },
      event: (type, topic, payload) => this.event(type, topic, payload),
      nameSession: (session, text) => {
        this.nameSession(session, text);
      },
      finish: (run, state, error) => {
        this.finish(run, state, error);
      },
      play: (session, run, text, images) => this.play(session, run, text, images),
      compact: (session, run, instructions) => this.compact(session, run, instructions),
      answers: this.answers,
      elicitAnswers: this.elicitAnswers,
      authorizations: this.authorizations,
    };
    authRoutes(ctx);
    settingsRoutes(ctx);
    searchRoutes(ctx);
    providerRoutes(ctx);
    projectRoutes(ctx);
    workspaceRoutes(ctx);
    fileRoutes(ctx);
    sessionRoutes(ctx);
    profileRoutes(ctx);
    runRoutes(ctx);
    mcpRoutes(ctx);

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

  private append(session: Session, message: Message, compaction?: Compaction): Entry {
    const list = (this.world.entries[session.id] ??= []);
    const entry: Entry = {
      id: this.nextId("ent"),
      seq: list.length + 1,
      kind: compaction === undefined ? entryKind(message) : "compaction",
      created_at: fixedNow,
      message,
      ...(compaction === undefined ? {} : { compaction }),
      ...(session.head_entry_id === undefined ? {} : { parent_id: session.head_entry_id }),
    };
    list.push(entry);
    session.head_entry_id = entry.id;
    session.updated_at = fixedNow;
    return entry;
  }

  /**
   * compact plays a manual compaction as the harness does: a run of its own
   * that reports its start and end and stores one compaction entry, which
   * keeps the last two messages.
   */
  private async compact(session: Session, run: Run, instructions: string): Promise<void> {
    this.busy += 1;
    const topic = `session:${session.id}`;
    const turn = this.nextId("turn");
    const send = (type: EventType, payload: Record<string, unknown>) => {
      this.emit(this.event(type, topic, { run_id: turn, ...payload }));
    };
    try {
      const tokensBefore = 48_213;
      send("compaction.start", { reason: "manual", tokens_before: tokensBefore });
      await new Promise((r) => setTimeout(r, this.stepDelayMs));
      if (run.state !== "running") return;
      const focus = instructions === "" ? "" : `\n\n## Focus\n- ${instructions}`;
      const compaction: Compaction = {
        summary: `## Goal\nMake webhook delivery retry with backoff.\n\n## Progress\n### Done\n- [x] Read \`internal/webhook/deliver.go\`\n\n## Next Steps\n1. Add tests for the backoff.${focus}`,
        kept: 2,
        tokens_before: tokensBefore,
        tokens_after: 6_412,
        reason: "manual",
        usage: { input_tokens: 612, output_tokens: 431, total_tokens: 1043 },
      };
      this.append(session, { role: "user" }, compaction);
      this.finish(run, "done");
      const { reason, tokens_before, tokens_after, summary, kept, usage } = compaction;
      send("compaction.end", { reason, tokens_before, tokens_after, summary, kept, usage });
    } finally {
      this.busy -= 1;
    }
  }

  /** play streams one scripted reply as a run's events and stores its entries. */
  private async play(
    session: Session,
    run: Run,
    text: string,
    images: MessageImage[],
  ): Promise<void> {
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
      this.append(session, {
        role: "user",
        ...(text === "" ? {} : { content: text }),
        ...(images.length === 0 ? {} : { images }),
      });
      // What the model call is sent, which the harness records once the
      // response is in.
      const sent = previewContext(this.world, session, "");
      const sentAt = session.head_entry_id;
      send("turn.start", {
        session_id: session.id,
        workspace_id: session.workspace_id,
        message: text,
        ...(images.length === 0 ? {} : { images }),
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
