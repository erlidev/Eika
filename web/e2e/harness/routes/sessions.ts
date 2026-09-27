/** Sessions: their entries, heads, forks, tools, and configuration. */

import type { Entry, Session } from "../../../src/api/types.ts";
import {
  previewContext,
  recordedContext,
  samplingProblem,
  sessionConfiguration,
  settingsOf,
  syncSession,
} from "../profiles.ts";
import type { ConfigurationDraft } from "../profiles.ts";
import { sessionTools } from "../world.ts";
import { fail, ok, str } from "./context.ts";
import type { Reply, RouteContext } from "./context.ts";

/** preview is the start of an entry's content, as internal/session words it. */
function preview(entry: Entry): string {
  if (entry.compaction) {
    return `compacted about ${String(entry.compaction.tokens_before)} tokens into a summary`;
  }
  const message = entry.message;
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

export function sessionRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
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
      id: ctx.nextId(chat ? "chat" : "ses"),
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
    if (str(body.title) === "") w.untitled.push(row.id);
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
        preview: preview(e),
        commit: e.commit,
        resumable,
        created_at: e.created_at,
      })),
    });
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
      id: ctx.nextId(source.workspace_id === undefined ? "chat" : "ses"),
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
}
