import { wireSession } from "../world.ts";
/** Sessions: their entries, heads, tools, and configuration. */

import type { Entry } from "../../../src/api/types.ts";
import { knownAgent, previewContext, recordedContext, syncSession } from "../modelContext.ts";
import { checkName, fail, ok, str } from "./context.ts";
import type { Reply, RouteContext } from "./context.ts";

/** preview is the start of an entry's content, as internal/session words it. */
function preview(entry: Entry): string {
  if (entry.compaction) {
    return `compacted about ${String(entry.compaction.tokens_before)} tokens into a summary`;
  }
  if (entry.text !== undefined) {
    let text = entry.text.trim();
    const first = text.search(/[\r\n]/);
    if (first >= 0) text = text.slice(0, first).trim() + " ...";
    const runes = Array.from(text);
    return runes.length > 80 ? runes.slice(0, 80).join("").trim() + " ..." : text;
  }
  const message = entry.message;
  if (message.content) return message.content.slice(0, 80);
  const call = message.tool_calls?.[0];
  return call?.name ?? "";
}

/**
 * resumableEntries pairs each entry with whether a run can continue from it,
 * the way `internal/session` does: an entry is resumable when the path down
 * to it leaves no tool call unanswered and it is not a preamble.
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
    return { entry, resumable: left.length === 0 && entry.kind !== "preamble" };
  });
}

/**
 * midTurn is the refusal the harness answers with when a head names
 * an entry that leaves tool calls unanswered, and nothing when it does not.
 */
function midTurn(entries: Entry[], entryId: string): Reply | undefined {
  const found = resumableEntries(entries).find(({ entry }) => entry.id === entryId);
  if (found === undefined || found.resumable) return undefined;
  if (found.entry.kind === "preamble") {
    return fail(
      400,
      "invalid_request",
      `entry ${entryId} is a preamble and cannot be a branch point`,
    );
  }
  return fail(
    400,
    "invalid_request",
    `entry ${entryId} leaves tool calls unanswered, so a run cannot continue from it`,
  );
}

export function sessionRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/home", () => {
    const row = w.sessions.find(
      (s) => s.kind === "home" && s.owner === "local" && s.name === "home",
    );
    if (!row) return fail(404, "not_found", "home session not found");
    syncSession(w, row);
    const head = (w.entries[row.id] ?? []).find((e) => e.id === row.head_entry_id);
    return ok({ session: wireSession(row), ...(head ? { head } : {}) });
  });
  on("GET", "/api/sessions", ({ query }) => {
    const cx = query.get("computer_id");
    const sessions = cx ? w.sessions.filter((s) => s.computer_id === cx) : w.sessions;
    for (const session of sessions) syncSession(w, session);
    return ok({
      sessions: sessions
        .toSorted((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id))
        .map(wireSession),
    });
  });
  on("GET", "/api/sessions/{id}", ({ params }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    syncSession(w, row);
    const head = (w.entries[row.id] ?? []).find((e) => e.id === row.head_entry_id);
    return ok({ session: wireSession(row), ...(head ? { head } : {}) });
  });
  on("PATCH", "/api/sessions/{id}", ({ params, body }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    if (body.title !== undefined) {
      const title = checkName("title", body.title);
      if (typeof title !== "string") return title;
      row.title = title;
      // A title someone chose is final: the first run no longer names it.
      w.untitled = w.untitled.filter((id) => id !== row.id);
    }
    if (typeof body.pinned === "boolean") row.pinned = body.pinned;
    if (typeof body.archived === "boolean") row.archived = body.archived;
    row.updated_at = now();
    ctx.emit(
      ctx.event("session.updated", "global", {
        session_id: row.id,
        ...(row.computer_id === undefined ? {} : { computer_id: row.computer_id }),
      }),
    );
    return ok(wireSession(row));
  });
  on("DELETE", "/api/sessions/{id}", ({ params }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    if (row.kind === "home") return fail(409, "conflict", "the home session cannot be deleted");
    w.sessions = w.sessions.filter((s) => s.id !== params[0]);
    return { status: 204 };
  });
  on("GET", "/api/sessions/{id}/entries", ({ params, query, rawQuery }) => {
    if (rawQuery.includes(";") || /%(?![0-9a-fA-F]{2})/.test(rawQuery))
      return fail(400, "invalid_request", "invalid pagination query");
    for (const key of query.keys()) {
      if ((key !== "before" && key !== "limit") || query.getAll(key).length !== 1)
        return fail(400, "invalid_request", "invalid pagination query");
    }
    const rawLimit = query.get("limit");
    if (rawLimit !== null && (!/^[1-9][0-9]*$/.test(rawLimit) || Number(rawLimit) > 100))
      return fail(400, "invalid_request", "limit must be an integer from 1 to 100");
    const before = query.get("before");
    if (before !== null) {
      const rawBefore = rawQuery.split("&").find((part) => new URLSearchParams(part).has("before"));
      try {
        decodeURIComponent((rawBefore?.split("=").slice(1).join("=") ?? "").replaceAll("+", " "));
      } catch {
        return fail(400, "invalid_request", "invalid before cursor");
      }
      if (before === "" || before.includes("\0") || new TextEncoder().encode(before).length > 1024)
        return fail(400, "invalid_request", "invalid before cursor");
    }
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    const all = (w.entries[row.id] ?? []).toSorted((a, b) => a.seq - b.seq);
    const cursor = before === null ? undefined : all.find((e) => e.id === before);
    if (before !== null && cursor === undefined)
      return fail(400, "invalid_request", "invalid before cursor");
    const eligible = cursor ? all.filter((e) => e.seq < cursor.seq) : all;
    const limit = rawLimit === null ? 50 : Number(rawLimit);
    const entries = eligible.slice(-limit);
    return ok({
      session_id: row.id,
      entries,
      ...(eligible.length > limit ? { next_before: entries[0]?.id } : {}),
    });
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
      return ok(wireSession(row));
    }
    const entry = find(w.entries[row.id] ?? [], str(body.entry_id), "entry");
    if ("status" in entry) return entry;
    const refused = midTurn(w.entries[row.id] ?? [], entry.id);
    if (refused) return refused;
    row.head_entry_id = entry.id;
    return ok(wireSession(row));
  });
  on("GET", "/api/sessions/{id}/context", ({ params, query }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    const model = query.get("model") ?? "";
    if (!knownAgent(row)) return fail(400, "invalid_request", `unknown agent "${row.agent ?? ""}"`);
    if (model !== "")
      return fail(400, "invalid_request", "agent sessions use model classes; model must be empty");
    return ok(previewContext(w, row, model));
  });
  on("GET", "/api/sessions/{id}/prediction", ({ params, query }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    const requested = query.get("model") ?? "";
    if (!knownAgent(row)) return fail(400, "invalid_request", `unknown agent "${row.agent ?? ""}"`);
    if (requested !== "")
      return fail(400, "invalid_request", "agent sessions use model classes; model must be empty");
    const next = previewContext(w, row, requested);
    const model = w.models.find((m) => m.model === next.parameters.model);
    if (model === undefined || next.prediction === undefined) {
      return fail(409, "conflict", "no model is configured; add one under Settings, Models");
    }
    return ok({ model: model.name, ...next.prediction, context_window: next.context_window });
  });
  on("GET", "/api/sessions/{id}/calls", ({ params }) => {
    const row = find(w.sessions, params[0], "session");
    if ("status" in row) return row;
    return ok({ session_id: row.id, calls: (w.calls[row.id] ?? []).map((r) => r.record) });
  });
  on("GET", "/api/sessions/{id}/calls/{call_id}", ({ params }) => {
    const found = (w.calls[params[0] ?? ""] ?? []).find((r) => r.record.id === params[1]);
    if (!found) return fail(404, "not_found", "model call not found");
    return ok(recordedContext(w, found));
  });
}
