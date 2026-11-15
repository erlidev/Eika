// @vitest-environment node
/**
 * The mock harness against the API contract, route by route. The specs hold
 * the mock to docs/api/contract.json on every request the UI makes, but only
 * for the routes a spec happens to reach; this calls each one directly, so a
 * route no spec uses cannot drift either.
 */

import { describe, expect, it } from "vitest";

import type { Entry, Session } from "../../src/api/types.ts";
import { eventTypes } from "../../src/api/events.ts";
import { contract } from "./contract.ts";
import { MockHarness, mockToken } from "./mock.ts";
import type { Reply } from "./mock.ts";
import { instructionRoutes } from "./routes/instructions.ts";
import type { Handler, RouteContext } from "./routes/context.ts";
import { scenario } from "./scenarios.ts";

/**
 * unmocked are the routes the mock does not serve, with why: the UI never
 * calls them. A test that calls one sees the mock's 404.
 */
const unmocked: Record<string, string> = {
  "GET /api/sessions/{id}/path": "the transcript reads the outline instead",
  "GET /api/events": "a WebSocket, which install routes itself",
  "GET /api/inference": "the Activity page that reads the queue arrives in phase 3",
};

/** open builds a mock on a named scenario whose replies play without pauses. */
function open(name: string): MockHarness {
  return new MockHarness(scenario(name).build(), { stepDelayMs: 0 });
}

/** call sends one request as the UI would, with the token, and fails on no answer. */
async function call(
  mock: MockHarness,
  method: string,
  path: string,
  body?: unknown,
  raw?: string,
): Promise<Reply> {
  const reply = await mock.answer(
    method,
    path,
    `Bearer ${mockToken}`,
    raw ?? (body === undefined ? "" : JSON.stringify(body)),
  );
  if (reply === "hang" || reply === "network") throw new Error(`${method} ${path}: ${reply}`);
  return reply;
}

/** statusOf is the status the harness answers a route's success with. */
function statusOf(key: string): number | undefined {
  return contract().routes[key]?.status;
}

describe("the mock serves the contract's routes", () => {
  it("serves only routes the harness has, each behind the token or not as it is", () => {
    const routes = contract().routes;
    for (const { key, auth } of open("workbench").routeKeys()) {
      const route = routes[key];
      expect(route, `${key} is not in the contract`).toBeDefined();
      expect(auth, `${key} needs a token in the mock but not in the harness, or the reverse`).toBe(
        route?.public !== true,
      );
    }
  });

  it("serves every route of the harness but those it lists as unmocked", () => {
    const served = new Set(
      open("workbench")
        .routeKeys()
        .map((r) => r.key),
    );
    const missing = Object.keys(contract().routes).filter(
      (key) => !served.has(key) && !(key in unmocked),
    );
    expect(missing).toEqual([]);
    for (const key of Object.keys(unmocked)) {
      expect(contract().routes[key], `${key} is listed as unmocked but gone`).toBeDefined();
    }
  });

  it("knows every event type the harness streams", () => {
    expect([...eventTypes].sort()).toEqual(Object.keys(contract().events).sort());
  });
});

describe("the mock answers as the harness does", () => {
  it("answers every route with the harness's status and shapes", async () => {
    const mock = open("workbench");
    const w = mock.world;
    const provider = w.providers[0]?.id ?? "";
    const model = w.models[0]?.id ?? "";
    const head = w.sessions.find((s) => s.id === "ses-backoff")?.head_entry_id ?? "";

    // Every request is one the harness accepts, and every answer one it gives.
    const requests: [string, string, unknown?][] = [
      ["GET /api/healthz", "/api/healthz"],
      ["GET /api/auth/status", "/api/auth/status"],
      ["GET /api/computers", "/api/computers"],
      ["POST /api/auth/login", "/api/auth/login", { password: "correct horse" }],
      [
        "PUT /api/auth/password",
        "/api/auth/password",
        { current_password: "correct horse", new_password: "battery staple" },
      ],
      ["GET /api/settings", "/api/settings"],
      ["PUT /api/settings", "/api/settings", { default_model: "gpt-5-mini" }],
      ["GET /api/system", "/api/system"],
      ["GET /api/tools", "/api/tools"],
      ["GET /api/search/status", "/api/search/status"],
      ["PUT /api/search/keys/{name}", "/api/search/keys/exa", { key: "exa-0123456789abcdef" }],
      ["POST /api/search", "/api/search", { query: "tokio", source: "web" }],
      ["GET /api/providers", "/api/providers"],
      [
        "POST /api/providers/probe",
        "/api/providers/probe",
        { kind: "openai", base_url: "https://api.openai.com/v1", api_key: "sk-test" },
      ],
      [
        "POST /api/providers",
        "/api/providers",
        { name: "Local", kind: "openai", base_url: "http://localhost:8000/v1" },
      ],
      ["PATCH /api/providers/{id}", `/api/providers/${provider}`, { name: "OpenAI (work)" }],
      ["GET /api/models", "/api/models"],
      ["POST /api/models/test", "/api/models/test", { provider_id: provider, model: "gpt-5" }],
      [
        "POST /api/models",
        "/api/models",
        { provider_id: provider, name: "gpt-5-nano", model: "gpt-5-nano", context_window: 400000 },
      ],
      ["PATCH /api/models/{id}", `/api/models/${model}`, { reasoning_effort: "high" }],
      ["GET /api/sessions", "/api/sessions?computer_id=cx-retries"],
      ["POST /api/sessions", "/api/sessions", { computer_id: "cx-retries", title: "Try jitter" }],
      ["GET /api/sessions/{id}", "/api/sessions/ses-backoff"],
      [
        "PATCH /api/sessions/{id}",
        "/api/sessions/ses-backoff",
        { title: "Back off", pinned: true, archived: true },
      ],
      ["GET /api/sessions/{id}/outline", "/api/sessions/ses-backoff/outline"],
      ["POST /api/sessions/{id}/head", "/api/sessions/ses-backoff/head", { entry_id: head }],
      ["PUT /api/sessions/{id}/tools", "/api/sessions/ses-backoff/tools", { tools: ["bash"] }],
      ["GET /api/sessions/{id}/run", "/api/sessions/ses-backoff/run"],
      ["POST /api/sessions/{id}/messages", "/api/sessions/ses-backoff/messages", { text: "Go on" }],
    ];
    for (const [key, path, body] of requests) {
      const reply = await call(mock, key.split(" ")[0] ?? "", path, body);
      expect(reply.status, `${key}: ${JSON.stringify(reply.body)}`).toBe(statusOf(key));
    }
    const deletions: [string, string][] = [
      ["DELETE /api/models/{id}", `/api/models/${model}`],
      ["DELETE /api/providers/{id}", `/api/providers/${provider}`],
      ["DELETE /api/sessions/{id}", "/api/sessions/ses-backoff"],
      ["POST /api/auth/logout", "/api/auth/logout"],
    ];
    for (const [key, path] of deletions) {
      const reply = await call(mock, key.split(" ")[0] ?? "", path);
      expect(reply.status, key).toBe(statusOf(key));
    }
    expect(mock.contractBreaks).toEqual([]);
  });

  it("answers every MCP route with the harness's status and shapes", async () => {
    const mock = open("mcp");
    const requests: [string, string, unknown?][] = [
      ["GET /api/mcp/servers", "/api/mcp/servers"],
      [
        "POST /api/mcp/servers",
        "/api/mcp/servers",
        {
          name: "notion",
          kind: "http",
          url: "https://mcp.notion.com/mcp",
          headers: { "X-Key": "k" },
        },
      ],
      ["GET /api/mcp/servers/{id}", "/api/mcp/servers/mcp-github"],
      [
        "PATCH /api/mcp/servers/{id}",
        "/api/mcp/servers/mcp-github",
        { disabled_tools: ["create_issue"], headers: { "X-Key": null } },
      ],
      ["POST /api/mcp/servers/{id}/connect", "/api/mcp/servers/mcp-github/connect", {}],
      [
        "POST /api/mcp/servers/{id}/connect",
        "/api/mcp/servers/mcp-filesystem/connect",
        { computer_id: "cx-retries" },
      ],
      [
        "POST /api/mcp/servers/{id}/resources/read",
        "/api/mcp/servers/mcp-github/resources/read",
        { uri: "repo://example/payments-api/README.md" },
      ],
      [
        "POST /api/mcp/servers/{id}/prompts/get",
        "/api/mcp/servers/mcp-github/prompts/get",
        { name: "summarize_pr", arguments: { repo: "example/payments-api", number: "42" } },
      ],
      [
        "POST /api/mcp/servers/{id}/authorize",
        "/api/mcp/servers/mcp-linear/authorize",
        { redirect_uri: "http://localhost:4319/mcp/callback" },
      ],
      ["DELETE /api/mcp/servers/{id}/authorization", "/api/mcp/servers/mcp-github/authorization"],
      ["DELETE /api/mcp/servers/{id}", "/api/mcp/servers/mcp-docs"],
    ];
    for (const [key, path, body] of requests) {
      const reply = await call(mock, key.split(" ")[0] ?? "", path, body);
      expect(reply.status, `${key}: ${JSON.stringify(reply.body)}`).toBe(statusOf(key));
    }

    // The authorization URL is the callback, as an authorization server that
    // approves at once would redirect; its parameters finish the sign-in.
    const started = await call(mock, "POST", "/api/mcp/servers/mcp-linear/authorize", {
      redirect_uri: "http://localhost:4319/mcp/callback",
    });
    const back = new URL((started.body as { authorization_url: string }).authorization_url);
    const finished = await call(mock, "POST", "/api/mcp/oauth/callback", {
      state: back.searchParams.get("state"),
      code: back.searchParams.get("code"),
      iss: back.searchParams.get("iss"),
    });
    expect(finished.status).toBe(statusOf("POST /api/mcp/oauth/callback"));
    expect(mock.world.mcpServers.find((d) => d.server.id === "mcp-linear")?.server.state).toBe(
      "connected",
    );
    // A state is redeemed once.
    const again = await call(mock, "POST", "/api/mcp/oauth/callback", {
      state: back.searchParams.get("state"),
      code: "x",
    });
    expect(again.status).toBe(404);
    expect(mock.contractBreaks).toEqual([]);
  });

  it("answers every profile and context route with the harness's status and shapes", async () => {
    const mock = open("workbench");
    const model = mock.world.models[0]?.id ?? "";
    const created = await call(mock, "POST", "/api/profiles", {
      name: "Careful",
      description: "reviews",
      instructions: "Check twice.",
      model_id: model,
      sampling: { temperature: 0.2, stop: ["END"] },
      tools: ["bash", "mcp__github__*"],
    });
    expect(created.status).toBe(statusOf("POST /api/profiles"));
    const profile = (created.body as { id: string }).id;
    const requests: [string, string, unknown?][] = [
      ["GET /api/profiles", "/api/profiles"],
      ["GET /api/profiles/inherited", `/api/profiles/inherited?model_id=${model}`],
      [
        "GET /api/sessions/{id}/configuration",
        `/api/sessions/ses-backoff/configuration?profile_id=${profile}&model_id=`,
      ],
      ["PUT /api/profiles/{id}", `/api/profiles/${profile}`, { name: "Careful", tools: null }],
      [
        "PUT /api/sessions/{id}/profile",
        "/api/sessions/ses-backoff/profile",
        { profile_id: profile },
      ],
      [
        "PUT /api/sessions/{id}/overrides",
        "/api/sessions/ses-backoff/overrides",
        { sampling: { top_k: 20 }, chat_prompt: "" },
      ],
      ["GET /api/sessions/{id}/configuration", "/api/sessions/ses-backoff/configuration"],
      ["PUT /api/sessions/{id}/tools", "/api/sessions/ses-backoff/tools", { tools: null }],
      ["GET /api/sessions/{id}/context", "/api/sessions/ses-backoff/context?model=gpt-5-mini"],
      ["POST /api/sessions/{id}/messages", "/api/sessions/ses-backoff/messages", { text: "Go on" }],
    ];
    for (const [key, path, body] of requests) {
      const reply = await call(mock, key.split(" ")[0] ?? "", path, body);
      expect(reply.status, `${key}: ${JSON.stringify(reply.body)}`).toBe(statusOf(key));
    }
    await mock.idle();
    const listed = await call(mock, "GET", "/api/sessions/ses-backoff/calls");
    expect(listed.status).toBe(statusOf("GET /api/sessions/{id}/calls"));
    const recorded = (listed.body as { calls: { id: string }[] }).calls.at(-1)?.id ?? "";
    const one = await call(mock, "GET", `/api/sessions/ses-backoff/calls/${recorded}`);
    expect(one.status).toBe(statusOf("GET /api/sessions/{id}/calls/{call_id}"));
    const deleted = await call(mock, "DELETE", `/api/profiles/${profile}`);
    expect(deleted.status).toBe(statusOf("DELETE /api/profiles/{id}"));
    const last = await call(mock, "DELETE", "/api/profiles/prof-default");
    expect(last.status).toBe(409);
    expect(mock.contractBreaks).toEqual([]);
  });

  it("resolves a session's configuration layer by layer, as the harness does", async () => {
    const mock = open("workbench");
    const created = await call(mock, "POST", "/api/profiles", {
      name: "Terse",
      instructions: "One line.",
      sampling: { temperature: 0.4, max_output: 200 },
    });
    const profile = (created.body as { id: string }).id;
    await call(mock, "PUT", "/api/sessions/ses-backoff/profile", { profile_id: profile });
    const reply = await call(mock, "PUT", "/api/sessions/ses-backoff/overrides", {
      sampling: { temperature: 0.9 },
    });
    const config = reply.body as {
      resolved: { sampling: Record<string, unknown>; sources: Record<string, string> };
      inherited: { sampling: Record<string, unknown>; sources: Record<string, string> };
    };
    expect(config.resolved.sampling).toMatchObject({ temperature: 0.9, max_output: 200 });
    expect(config.resolved.sources).toMatchObject({
      profile: "session",
      "sampling.temperature": "session",
      "sampling.max_output": "profile",
      instructions: "profile",
    });
    expect(config.inherited.sampling).toMatchObject({ temperature: 0.4 });
    const session = mock.world.sessions.find((s) => s.id === "ses-backoff");
    expect(session?.overridden).toBe(true);
  });

  it("carries a renamed MCP server's name into tool choices, as the harness does", async () => {
    const mock = open("mcp");
    const created = await call(mock, "POST", "/api/profiles", {
      name: "Code",
      tools: ["mcp__github__*"],
    });
    expect(created.status).toBe(201);
    mock.world.toolChoices["ses-any"] = ["mcp__github__search", "mcp__github2__*"];
    await call(mock, "PATCH", "/api/mcp/servers/mcp-github", { name: "gh" });
    const profile = mock.world.profiles.find((p) => p.name === "Code");
    expect(profile?.tools).toEqual(["mcp__gh__*"]);
    expect(mock.world.toolChoices["ses-any"]).toEqual(["mcp__gh__search", "mcp__github2__*"]);
  });

  it("answers with what a draft falls through to and saves nothing, as the harness does", async () => {
    const mock = open("workbench");
    const created = await call(mock, "POST", "/api/profiles", {
      name: "Terse",
      instructions: "One line.",
    });
    const profile = (created.body as { id: string }).id;
    const reply = await call(
      mock,
      "GET",
      `/api/sessions/ses-backoff/configuration?profile_id=${profile}`,
    );
    const config = reply.body as {
      profile_id?: string;
      inherited: { instructions: string; profile_id: string };
    };
    expect(config.inherited).toMatchObject({ instructions: "One line.", profile_id: profile });
    expect(config.profile_id).toBeUndefined();
    const unknown = await call(mock, "GET", "/api/profiles/inherited?model_id=absent");
    expect(unknown.status).toBe(400);
  });

  it("streams an MCP server's request for input, and takes the answer", async () => {
    const mock = open("agent-mcp");
    await call(mock, "POST", "/api/sessions/ses-backoff/messages", { text: "File it" });
    await expect.poll(() => mock.world.elicitations.length).toBe(1);
    const run = await call(mock, "GET", "/api/sessions/ses-backoff/run");
    expect((run.body as { elicitations: unknown[] }).elicitations).toHaveLength(1);
    const id = mock.world.elicitations[0]?.id ?? "";
    const refused = await call(mock, "POST", `/api/elicitations/${id}/answer`, {
      action: "decline",
      content: { assignee: "ada" },
    });
    expect(refused.status).toBe(400);
    const reply = await call(mock, "POST", `/api/elicitations/${id}/answer`, {
      action: "accept",
      content: { assignee: "ada", labels: ["flaky-test"] },
    });
    expect(reply.status).toBe(statusOf("POST /api/elicitations/{id}/answer"));
    await mock.idle();
    expect(mock.world.elicitations).toEqual([]);
    expect(mock.contractBreaks).toEqual([]);
  });

  it("sets a password up the way the harness does", async () => {
    const mock = open("setup-new");
    const reply = await call(mock, "POST", "/api/auth/setup", { password: "correct horse" });
    expect(reply.status).toBe(statusOf("POST /api/auth/setup"));
    expect(mock.contractBreaks).toEqual([]);
  });

  it("streams a scripted run's events as the harness does, and takes an answer", async () => {
    const mock = open("agent-question");
    const session = mock.world.sessions[0]?.id ?? "";
    await call(mock, "POST", `/api/sessions/${session}/messages`, { text: "Change the cap" });
    await expect.poll(() => mock.world.questions.length).toBe(1);
    const question = mock.world.questions[0]?.id ?? "";
    const reply = await call(mock, "POST", `/api/questions/${question}/answer`, { answer: "30s" });
    expect(reply.status).toBe(statusOf("POST /api/questions/{id}/answer"));
    await mock.idle();
    expect(mock.contractBreaks).toEqual([]);
  });

  it("takes images for a model that accepts them, and refuses them otherwise", async () => {
    const mock = open("agent-question");
    const session = mock.world.sessions[0]?.id ?? "";
    // A one-pixel PNG: the signature and a header saying 1 by 1.
    const png = btoa("\x89PNG\r\n\x1a\n\0\0\0\rIHDR\0\0\0\x01\0\0\0\x01");
    const message = { text: "", images: [{ data: png }] };
    const refused = await call(mock, "POST", `/api/sessions/${session}/messages`, message);
    expect(refused.status).toBe(400);

    for (const m of mock.world.models) m.image_input = true;
    const started = await call(mock, "POST", `/api/sessions/${session}/messages`, message);
    expect(started.status).toBe(statusOf("POST /api/sessions/{id}/messages"));
    const user = (mock.world.entries[session] ?? []).findLast((e) => e.kind === "user");
    expect(user?.message).toEqual({
      role: "user",
      images: [{ media_type: "image/png", data: png, width: 1, height: 1 }],
    });
    const junk = await call(mock, "POST", `/api/sessions/${session}/messages`, {
      text: "x",
      mode: "steer",
      images: [{ data: btoa("not an image") }],
    });
    expect(junk.status).toBe(400);
    await expect.poll(() => mock.world.questions.length).toBe(1);
    await call(mock, "POST", `/api/questions/${mock.world.questions[0]?.id ?? ""}/answer`, {
      answer: "30s",
    });
    await mock.idle();
    expect(mock.contractBreaks).toEqual([]);
  });

  it("aborts a run as the harness does", async () => {
    const mock = open("agent-running");
    const session = mock.world.sessions[0]?.id ?? "";
    const started = await call(mock, "POST", `/api/sessions/${session}/messages`, { text: "Go" });
    const run = (started.body as { id: string }).id;
    const reply = await call(mock, "POST", `/api/runs/${run}/abort`);
    expect(reply.status).toBe(statusOf("POST /api/runs/{id}/abort"));
    expect(mock.contractBreaks).toEqual([]);
  });

  it("compacts a session as the harness does, and replays the compaction", async () => {
    const mock = open("workbench");
    const reply = await call(mock, "POST", "/api/sessions/ses-backoff/compact", {
      instructions: "the retry tests",
    });
    expect(reply.status).toBe(statusOf("POST /api/sessions/{id}/compact"));
    await mock.idle();
    const entries = mock.world.entries["ses-backoff"] ?? [];
    expect(entries.at(-1)).toMatchObject({ kind: "compaction", compaction: { kept: 2 } });
    const session = await call(mock, "GET", "/api/sessions/ses-backoff");
    expect(session.body).toMatchObject({ head: { kind: "compaction" } });
    expect(mock.contractBreaks).toEqual([]);

    // What the compaction kept is all there is, so a second one has nothing to do.
    const again = await call(mock, "POST", "/api/sessions/ses-backoff/compact", {});
    expect(again.status).toBe(409);
  });

  it("refuses what the harness refuses, the way it refuses it", async () => {
    const mock = open("workbench");
    const refusals: [string, string, unknown?][] = [
      // The harness's decoder refuses a field it does not know, and no body.
      ["POST", "/api/profiles", { name: "x", colour: "red" }],
      ["POST", "/api/profiles"],
      ["POST", "/api/sessions/ses-backoff/messages", { text: "" }],
      ["GET", "/api/sessions/no-such-session"],
      ["GET", "/api/sessions?chats=true&computer_id=cx-retries"],
    ];
    for (const [method, path, body] of refusals) {
      const reply = await call(mock, method, path, body);
      expect(reply.status, `${method} ${path}`).toBeGreaterThanOrEqual(400);
    }
    // A request with a field the harness does not know is reported, since the
    // page should never send one; the refusals themselves are well formed.
    expect(mock.contractBreaks).toEqual([
      "POST /api/profiles request: $.colour: unknown field",
      "POST /api/profiles request: $: no body",
    ]);
  });

  it("refuses a head on an entry in the middle of a turn with the harness's code", async () => {
    const mock = open("workbench");
    const entries = mock.world.entries["ses-backoff"] ?? [];
    const midTurn = entries.find((e) => (e.message.tool_calls ?? []).length > 0)?.id ?? "";
    const reply = await call(mock, "POST", "/api/sessions/ses-backoff/head", { entry_id: midTurn });
    expect(reply.status).toBe(400);
    expect(mock.contractBreaks).toEqual([]);
  });
});

describe("agent definitions", () => {
  it("lists built-ins, replaces overrides and resets to defaults", async () => {
    const mock = open("workbench");
    const listed = await call(mock, "GET", "/api/agents");
    expect(listed.status).toBe(200);
    expect(listed.body).toMatchObject({
      agents: [
        { name: "eika", class: "main", tools: ["web_search"], max_turns: 40 },
        { name: "researcher", class: "fast", max_turns: 200 },
        { name: "worker", class: "main", max_turns: 200 },
      ],
    });
    const original = await call(mock, "GET", "/api/agents/eika");
    const updated = await call(mock, "PUT", "/api/agents/eika", { class: "deep", prompt: "" });
    expect(updated.body).toMatchObject({
      class: "deep",
      prompt: "",
      overrides: { class: "deep", prompt: "" },
    });
    const partial = await call(mock, "PUT", "/api/agents/eika", { prompt: "custom" });
    expect(partial.body).toMatchObject({
      class: "main",
      prompt: "custom",
      overrides: { class: null, prompt: "custom" },
    });
    expect((await call(mock, "PUT", "/api/agents/eika", {})).body).toEqual(original.body);
    await call(mock, "PUT", "/api/agents/eika", { class: "fast" });
    expect((await call(mock, "DELETE", "/api/agents/eika")).status).toBe(204);
    expect((await call(mock, "GET", "/api/agents/eika")).body).toEqual(original.body);
    expect((await call(mock, "DELETE", "/api/agents/eika")).status).toBe(204);
    for (const method of ["GET", "PUT", "DELETE"]) {
      expect(
        (await call(mock, method, "/api/agents/browser", method === "PUT" ? {} : undefined)).status,
      ).toBe(404);
    }
    for (const body of [
      null,
      { class: "decision" },
      { class: "" },
      { class: "unknown" },
      { prompt: "é".repeat(32769) },
      { prompt: "before\0after" },
    ]) {
      expect((await call(mock, "PUT", "/api/agents/eika", body)).status).toBe(400);
    }
    expect(mock.contractBreaks).toEqual([]);
  });

  it("refuses unknown override fields without changing the agent", async () => {
    const mock = open("workbench");
    await call(mock, "PUT", "/api/agents/eika", { class: "fast", prompt: "saved" });
    const before = await call(mock, "GET", "/api/agents/eika");
    for (const body of [
      { class: "deep", tools: ["bash"] },
      { prompt: "changed", name: "other" },
      { compaction: {} },
    ]) {
      expect((await call(mock, "PUT", "/api/agents/eika", body)).status).toBe(400);
    }
    expect((await call(mock, "GET", "/api/agents/eika")).body).toEqual(before.body);
    expect(mock.contractBreaks).toEqual([
      "PUT /api/agents/{name} request: $.tools: unknown field",
      "PUT /api/agents/{name} request: $.name: unknown field",
      "PUT /api/agents/{name} request: $.compaction: unknown field",
    ]);
  });
});

describe("agent runtime", () => {
  it("uses agent prompts, class fallback and fixed tools beside profiles", async () => {
    const mock = open("workbench");
    const session = mock.world.sessions[0];
    if (!session) throw new Error("fixture has no session");
    session.agent = "eika";
    mock.world.overrides[session.id] = {
      chat_prompt: "legacy",
      instructions: "legacy rules",
      preserve_thinking: true,
      sampling: { temperature: 0.9 },
    };
    mock.world.toolChoices[session.id] = ["bash", "ask_user"];
    await call(mock, "PUT", "/api/agents/eika", { class: "deep", prompt: "agent rules" });
    mock.world.settings.settings.model_classes = { main: mock.world.defaultModel };
    const base = `/api/sessions/${session.id}`;
    const cfg = await call(mock, "GET", `${base}/configuration`);
    expect(cfg.body).toMatchObject({
      resolved: {
        profile_id: "",
        model: mock.world.defaultModel,
        chat_prompt: "agent rules",
        instructions: "",
        tools: ["web_search"],
        sources: { model: "class", chat_prompt: "agent", tools: "agent" },
      },
    });
    const preview = await call(mock, "GET", `${base}/context`);
    expect(preview.body).toMatchObject({
      sections: [{ kind: "base", text: "agent rules" }],
      tools: [{ name: "web_search" }],
    });
    expect(session.tools).toEqual(["web_search"]);
    expect(session.overridden).toBe(false);
    for (const route of ["context", "prediction"])
      expect(
        (await call(mock, "GET", `${base}/${route}?model=${mock.world.defaultModel ?? ""}`)).status,
      ).toBe(400);
    for (const route of ["messages", "compact"])
      expect(
        (
          await call(mock, "POST", `${base}/${route}`, {
            ...(route === "messages" ? { text: "hello" } : {}),
            model: mock.world.defaultModel,
          })
        ).status,
      ).toBe(400);
    await call(mock, "PUT", "/api/agents/eika", { prompt: "" });
    expect((await call(mock, "GET", `${base}/context`)).body).toMatchObject({ sections: [] });
    expect(mock.contractBreaks).toEqual([]);
  });
});

describe("project metadata", () => {
  it("creates defaults, keeps slugs unique and applies partial updates", async () => {
    const mock = open("workbench");
    expect((await call(mock, "GET", "/api/projects")).body).toEqual({ projects: [] });
    const created = await call(mock, "POST", "/api/projects", { name: "Taxes", slug: "taxes" });
    expect(created.status).toBe(201);
    expect(created.body).toMatchObject({
      name: "Taxes",
      slug: "taxes",
      status: "active",
      description: "",
      directory: "/home/eika/projects/taxes",
    });
    const project = mock.world.projects[0];
    if (!project) throw new Error("project not created");
    const path = `/api/projects/${project.id}`;
    expect((await call(mock, "GET", path)).body).toEqual(created.body);
    expect(
      (await call(mock, "POST", "/api/projects", { name: "Copy", slug: "taxes" })).status,
    ).toBe(409);
    for (const status of ["paused", "done", "archived", "active"]) {
      const updated = await call(mock, "PATCH", path, { status, description: "This year" });
      expect(updated.status).toBe(200);
      expect(updated.body).toMatchObject({ status, slug: "taxes", description: "This year" });
    }
    expect(
      (await call(mock, "PATCH", path, { description: "", directory: "/tmp/repo" })).body,
    ).toMatchObject({ description: "", directory: "/tmp/repo", name: "Taxes" });
    expect((await call(mock, "POST", "/api/projects", { name: "Other", slug: "aaa" })).status).toBe(
      201,
    );
    expect((await call(mock, "GET", "/api/projects")).body).toMatchObject({
      projects: [{ slug: "aaa" }, { slug: "taxes" }],
    });
    expect((await call(mock, "GET", "/api/projects/missing")).status).toBe(404);
    expect((await call(mock, "PATCH", "/api/projects/missing", {})).status).toBe(404);
    expect(mock.contractBreaks).toEqual([]);
  });

  it("refuses unsafe paths and invalid metadata without changing the record", async () => {
    const mock = open("workbench");
    await call(mock, "POST", "/api/projects", { name: "Taxes", slug: "taxes" });
    const project = mock.world.projects[0];
    if (!project) throw new Error("project not created");
    const path = `/api/projects/${project.id}`;
    const before = structuredClone(mock.world.projects[0]);
    expect((await call(mock, "PATCH", path, null)).status).toBe(400);
    expect((await call(mock, "POST", "/api/projects", null)).status).toBe(400);
    expect((await call(mock, "PATCH", path, { name: null, status: null })).status).toBe(200);
    for (const [method, route] of [
      ["GET", "/api/projects"],
      ["POST", "/api/projects"],
      ["GET", path],
      ["PATCH", path],
    ]) {
      if (!method || !route) throw new Error("route missing");
      expect(await mock.answer(method, route, undefined, "{}")).toMatchObject({ status: 401 });
    }
    for (const directory of [
      "",
      "/",
      "relative",
      "/a/../b",
      "/a/./b",
      "/a//b",
      "/a/",
      "/a\\b",
      "/a\0b",
      "/a\nb",
    ]) {
      expect((await call(mock, "PATCH", path, { directory })).status, directory).toBe(400);
    }
    for (const body of [
      { name: "" },
      { name: " " },
      { status: "bad" },
      { status: "" },
      { description: "a\0b" },
    ]) {
      expect((await call(mock, "PATCH", path, body)).status).toBe(400);
    }
    for (const slug of ["../bad", "UPPER", "a--b", "-a", "a-", "a/b", "a\n", "x".repeat(81)]) {
      expect((await call(mock, "POST", "/api/projects", { name: "Bad", slug })).status).toBe(400);
    }
    expect(mock.world.projects[0]).toEqual(before);
    expect(mock.contractBreaks).toEqual([]);
  });

  it("rejects instruction writes and slug changes through the contract", async () => {
    const mock = open("workbench");
    await call(mock, "POST", "/api/projects", { name: "Taxes", slug: "taxes" });
    const project = mock.world.projects[0];
    if (!project) throw new Error("project not created");
    const path = `/api/projects/${project.id}`;
    for (const body of [{ instructions: "attack" }, { slug: "changed" }]) {
      expect((await call(mock, "PATCH", path, body)).status).toBe(400);
    }
    expect(
      (
        await call(mock, "POST", "/api/projects", {
          name: "Bad",
          slug: "bad",
          instructions: "attack",
        })
      ).status,
    ).toBe(400);
    expect(mock.world.projects).toHaveLength(1);
    expect(mock.contractBreaks).toHaveLength(3);
  });
});

describe("user instructions", () => {
  it("rejects unknown fields in route handlers before changing stored text", async () => {
    const mock = open("workbench");
    await call(mock, "POST", "/api/projects", { name: "One", slug: "one" });
    const project = mock.world.projects[0];
    if (!project) throw new Error("project not created");
    const handlers = new Map<string, Handler>();
    instructionRoutes({
      w: mock.world,
      on: (method: string, path: string, handler: Handler) => {
        handlers.set(`${method} ${path}`, handler);
      },
    } as RouteContext);

    for (const [key, valid, unknown] of [
      ["PUT /api/instructions", { about_me: "saved", rules: "rules" }, { project_id: "one" }],
      ["PUT /api/projects/{id}/instructions", { instructions: "saved" }, { rules: "injected" }],
    ] as const) {
      const handler = handlers.get(key);
      if (!handler) throw new Error(`missing ${key}`);
      const run = (body: Record<string, unknown>) =>
        handler({
          params: [project.id],
          query: new URLSearchParams(),
          body,
          raw: JSON.stringify(body),
        });
      expect((await run(valid)).status).toBe(200);
      expect((await run({ ...valid, ...unknown })).status).toBe(400);
    }
    expect(mock.world.instructions).toEqual({ about_me: "saved", rules: "rules" });
    expect(mock.world.projectInstructions[project.id]).toBe("saved");
  });

  it("keeps global and project text separate from metadata", async () => {
    const mock = open("workbench");
    const global = { about_me: "About me\n", rules: "Use metric" };
    expect((await call(mock, "GET", "/api/instructions")).body).toEqual({
      about_me: "",
      rules: "",
    });
    expect((await call(mock, "PUT", "/api/instructions", global)).body).toEqual(global);
    await call(mock, "POST", "/api/projects", { name: "One", slug: "one" });
    await call(mock, "POST", "/api/projects", { name: "Two", slug: "two" });
    const [one, two] = mock.world.projects;
    if (!one || !two) throw new Error("projects not created");
    const path = `/api/projects/${one.id}/instructions`;
    const other = `/api/projects/${two.id}/instructions`;
    const text = { instructions: "# Rules\nKeep this.\n" };
    expect((await call(mock, "GET", path)).body).toEqual({ instructions: "" });
    expect((await call(mock, "PUT", path, text)).body).toEqual(text);
    await call(mock, "PATCH", `/api/projects/${one.id}`, { description: "Changed" });
    expect((await call(mock, "GET", path)).body).toEqual(text);
    expect((await call(mock, "GET", other)).body).toEqual({ instructions: "" });
    expect((await call(mock, "GET", "/api/instructions")).body).toEqual(global);
    for (const target of ["/api/instructions", path]) {
      const valid = target === path ? text : global;
      for (const method of ["GET", "PUT", "DELETE"]) {
        expect(await mock.answer(method, target, undefined, JSON.stringify(valid))).toMatchObject({
          status: 401,
        });
      }
      for (let i = 0; i < 2; i++) {
        expect((await call(mock, "DELETE", target)).status).toBe(204);
      }
    }
    expect((await call(mock, "GET", path)).body).toEqual({ instructions: "" });
    expect((await call(mock, "GET", "/api/instructions")).body).toEqual({
      about_me: "",
      rules: "",
    });
    for (const method of ["GET", "PUT", "DELETE"]) {
      expect((await call(mock, method, "/api/projects/missing/instructions", text)).status).toBe(
        404,
      );
    }
    expect(mock.contractBreaks).toEqual([]);
  });

  it("rejects incomplete, invalid and foreign instruction fields", async () => {
    const mock = open("workbench");
    for (const body of [
      null,
      {},
      { about_me: "a" },
      { about_me: "a", rules: null },
      { about_me: "a", rules: 4 },
      { about_me: "a", rules: "\0" },
      { about_me: "a", rules: "a".repeat(65537) },
    ]) {
      expect((await call(mock, "PUT", "/api/instructions", body)).status).toBe(400);
    }
    await call(mock, "POST", "/api/projects", { name: "One", slug: "one" });
    const one = mock.world.projects[0];
    if (!one) throw new Error("project not created");
    const path = `/api/projects/${one.id}/instructions`;
    for (const body of [
      null,
      {},
      { instructions: null },
      { instructions: 4 },
      { instructions: "\0" },
      { instructions: "é".repeat(32769) },
    ]) {
      expect((await call(mock, "PUT", path, body)).status).toBe(400);
    }
    expect((await call(mock, "PUT", path, { instructions: "a", rules: "b" })).status).toBe(400);
    expect(
      (
        await call(mock, "PUT", "/api/instructions", {
          about_me: "a",
          rules: "b",
          project_id: one.id,
        })
      ).status,
    ).toBe(400);
    expect((await call(mock, "GET", path)).body).toEqual({ instructions: "" });
  });
});

describe("session storage metadata", () => {
  it("returns identity and activity defaults and refuses writes to them", async () => {
    const mock = open("workbench");
    const reply = await call(mock, "POST", "/api/sessions", { chat: true });
    expect(reply.status).toBe(201);
    expect(reply.body).toMatchObject({ kind: "user", owner: "local", state: "idle" });
    const row = reply.body as Session;
    expect(row.last_active_at).toBe(row.created_at);
    for (const field of [
      "kind",
      "owner",
      "agent",
      "name",
      "project_id",
      "parent_session_id",
      "state",
      "brief",
      "result",
      "last_active_at",
    ]) {
      const create = await call(mock, "POST", "/api/sessions", { chat: true, [field]: "changed" });
      expect(create.status, field).toBe(400);
      const update = await call(mock, "PATCH", `/api/sessions/${row.id}`, { [field]: "changed" });
      expect(update.status, field).toBe(400);
    }
    const read = await call(mock, "GET", `/api/sessions/${row.id}`);
    expect(read.body).toMatchObject({ session: row });
  });

  it("keeps preamble and notice text and message channels in entry responses", async () => {
    const mock = open("workbench");
    const row = mock.world.sessions[0];
    if (!row) throw new Error("missing session");
    const text = "  café <tag> & text\nnext line";
    const entry: Entry = {
      id: "notice-entry",
      seq: 1,
      kind: "notice",
      text,
      channel: "web",
      created_at: row.created_at,
      message: { role: "" },
    };
    mock.world.entries[row.id] = [entry];
    row.head_entry_id = entry.id;
    const reply = await call(mock, "GET", `/api/sessions/${row.id}`);
    expect(reply.body).toMatchObject({ head: { text, channel: "web", kind: "notice" } });
    const outline = await call(mock, "GET", `/api/sessions/${row.id}/outline`);
    expect(outline.body).toMatchObject({
      nodes: [{ preview: "café <tag> & text ...", resumable: true }],
    });
  });
});
