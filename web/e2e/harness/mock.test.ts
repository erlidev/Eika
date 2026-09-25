// @vitest-environment node
/**
 * The mock harness against the API contract, route by route. The specs hold
 * the mock to docs/api/contract.json on every request the UI makes, but only
 * for the routes a spec happens to reach; this calls each one directly, so a
 * route no spec uses cannot drift either.
 */

import { describe, expect, it } from "vitest";

import { eventTypes } from "../../src/api/events.ts";
import { contract } from "./contract.ts";
import { MockHarness, mockToken } from "./mock.ts";
import type { Reply } from "./mock.ts";
import { scenario } from "./scenarios.ts";

/**
 * unmocked are the routes the mock does not serve, with why: the UI never
 * calls them, and a test that calls one sees the mock's 404.
 */
const unmocked: Record<string, string> = {
  "POST /api/workspaces/{id}/merge": "only subagents merge a workspace",
  "POST /api/subagents/{id}/abort": "the UI stops a child agent's run, not the agent",
  "GET /api/events": "a WebSocket, which install routes itself",
  "GET /api/workspaces/{id}/terminal": "a WebSocket, which install routes itself",
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
      ["GET /api/projects", "/api/projects"],
      [
        "POST /api/projects",
        "/api/projects",
        { name: "billing", kind: "remote", remote_url: "https://github.com/example/billing.git" },
      ],
      ["GET /api/projects/{id}", "/api/projects/proj-api"],
      ["PATCH /api/projects/{id}", "/api/projects/proj-api", { default_branch: "trunk" }],
      ["GET /api/workspaces", "/api/workspaces?project_id=proj-api"],
      ["POST /api/workspaces", "/api/workspaces", { project_id: "proj-api", name: "try-jitter" }],
      ["GET /api/workspaces/{id}", "/api/workspaces/ws-retries"],
      ["POST /api/workspaces/{id}/stop", "/api/workspaces/ws-retries/stop"],
      ["POST /api/workspaces/{id}/start", "/api/workspaces/ws-retries/start"],
      ["GET /api/workspaces/{id}/diff", "/api/workspaces/ws-retries/diff"],
      ["GET /api/workspaces/{id}/files", "/api/workspaces/ws-retries/files"],
      ["GET /api/workspaces/{id}/file", "/api/workspaces/ws-retries/file?path=README.md"],
      [
        "POST /api/workspaces/{id}/commit",
        "/api/workspaces/ws-retries/commit",
        { message: "Back off" },
      ],
      ["POST /api/workspaces/{id}/push", "/api/workspaces/ws-retries/push", { upstream: true }],
      ["GET /api/sessions", "/api/sessions?workspace_id=ws-retries&descendants=true"],
      ["POST /api/sessions", "/api/sessions", { workspace_id: "ws-retries", title: "Try jitter" }],
      ["GET /api/sessions/{id}", "/api/sessions/ses-backoff"],
      ["GET /api/sessions/{id}/outline", "/api/sessions/ses-backoff/outline"],
      ["GET /api/sessions/{id}/path", "/api/sessions/ses-backoff/path"],
      ["POST /api/sessions/{id}/head", "/api/sessions/ses-backoff/head", { entry_id: head }],
      ["POST /api/sessions/{id}/fork", "/api/sessions/ses-backoff/fork", { entry_id: head }],
      ["PUT /api/sessions/{id}/tools", "/api/sessions/ses-backoff/tools", { tools: ["bash"] }],
      ["GET /api/sessions/{id}/agents", "/api/sessions/ses-backoff/agents"],
      ["GET /api/sessions/{id}/run", "/api/sessions/ses-backoff/run"],
      ["POST /api/sessions/{id}/messages", "/api/sessions/ses-backoff/messages", { text: "Go on" }],
    ];
    for (const [key, path, body] of requests) {
      const reply = await call(mock, key.split(" ")[0] ?? "", path, body);
      expect(reply.status, `${key}: ${JSON.stringify(reply.body)}`).toBe(statusOf(key));
    }
    // The editor saves a file as its raw text.
    const saved = await call(
      mock,
      "PUT",
      "/api/workspaces/ws-retries/file?path=NOTES.md",
      undefined,
      "# Notes\n",
    );
    expect(saved.status).toBe(statusOf("PUT /api/workspaces/{id}/file"));
    await mock.idle();

    const deletions: [string, string][] = [
      ["DELETE /api/models/{id}", `/api/models/${model}`],
      ["DELETE /api/providers/{id}", `/api/providers/${provider}`],
      ["DELETE /api/sessions/{id}", "/api/sessions/ses-backoff"],
      ["DELETE /api/workspaces/{id}", "/api/workspaces/ws-retries"],
      ["DELETE /api/projects/{id}", "/api/projects/proj-api"],
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
        { workspace_id: "ws-retries" },
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
        { sampling: { top_k: 20 }, workspace_prompt: "" },
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
    const listed = await call(mock, "GET", "/api/sessions/ses-backoff/requests");
    expect(listed.status).toBe(statusOf("GET /api/sessions/{id}/requests"));
    const recorded = (listed.body as { requests: { id: string }[] }).requests.at(-1)?.id ?? "";
    const one = await call(mock, "GET", `/api/sessions/ses-backoff/requests/${recorded}`);
    expect(one.status).toBe(statusOf("GET /api/sessions/{id}/requests/{request_id}"));
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

  it("aborts a run as the harness does", async () => {
    const mock = open("agent-running");
    const session = mock.world.sessions[0]?.id ?? "";
    const started = await call(mock, "POST", `/api/sessions/${session}/messages`, { text: "Go" });
    const run = (started.body as { id: string }).id;
    const reply = await call(mock, "POST", `/api/runs/${run}/abort`);
    expect(reply.status).toBe(statusOf("POST /api/runs/{id}/abort"));
    expect(mock.contractBreaks).toEqual([]);
  });

  it("refuses what the harness refuses, the way it refuses it", async () => {
    const mock = open("workbench");
    const refusals: [string, string, unknown?][] = [
      // The harness's decoder refuses a field it does not know, and no body.
      ["POST", "/api/projects", { name: "x", colour: "red" }],
      ["POST", "/api/projects"],
      ["POST", "/api/sessions/ses-backoff/messages", { text: "" }],
      ["GET", "/api/sessions/no-such-session"],
      ["POST", "/api/workspaces/ws-retries/commit", { message: "" }],
      ["GET", "/api/sessions?chats=true&workspace_id=ws-retries"],
    ];
    for (const [method, path, body] of refusals) {
      const reply = await call(mock, method, path, body);
      expect(reply.status, `${method} ${path}`).toBeGreaterThanOrEqual(400);
    }
    // A request with a field the harness does not know is reported, since the
    // page should never send one; the refusals themselves are well formed.
    expect(mock.contractBreaks).toEqual([
      "POST /api/projects request: $.colour: unknown field",
      "POST /api/projects request: $: no body",
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
