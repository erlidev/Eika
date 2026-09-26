/**
 * MCP servers, as internal/server/mcp.go serves them. An authorization
 * server is not mocked: the authorization URL is the callback itself,
 * as an authorization server that approves at once would redirect.
 */

import type { ContentDetail, MCPServerDetails } from "../../../src/api/types.ts";
import { syncMCPTools } from "../world.ts";
import { fail, ok, str, strings } from "./context.ts";
import type { Reply, RouteContext } from "./context.ts";

export function mcpRoutes(ctx: RouteContext): void {
  const { w, on, now, running } = ctx;
  const mcpNamePattern = /^[A-Za-z0-9-]+(_[A-Za-z0-9-]+)*$/;
  const mcpServer = (id: string | undefined) => {
    const row = w.mcpServers.find((d) => d.server.id === id);
    return row ?? fail(404, "not_found", "mcp server not found");
  };
  const mcpChanged = (d: MCPServerDetails, state: string = d.server.state) => {
    syncMCPTools(w);
    d.server.updated_at = now();
    ctx.emit(
      ctx.event("mcp.server", "global", {
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
        id: ctx.nextId("mcp"),
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
    const state = ctx.nextId("state");
    ctx.authorizations.set(state, d.server.id);
    const issuer = d.auth.issuer ?? "https://auth.example.com";
    redirect.search = new URLSearchParams({ code: "mock-code", state, iss: issuer }).toString();
    return ok({ authorization_url: redirect.toString() });
  });
  on("POST", "/api/mcp/oauth/callback", ({ body }) => {
    const id = ctx.authorizations.get(str(body.state));
    if (id === undefined) return fail(404, "not_found", "no authorization waits for that state");
    ctx.authorizations.delete(str(body.state));
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
    if (!prompt) return fail(400, "invalid_request", `get the prompt: no prompt ${str(body.name)}`);
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
    const answer = ctx.elicitAnswers.get(params[0] ?? "");
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
}
