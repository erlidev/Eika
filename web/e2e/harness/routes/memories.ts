/**
 * The user's memory API: list/search/read and the changes only the user makes.
 * It follows docs/api/http.md, "Memory": content_sha is the stale-edit
 * precondition, a credential is refused, a flagged edit becomes pending, and a
 * pin is limited to 12 memories and 3,000 tokens (counted here as 4 characters
 * a token, which the real server does with the model's own counter).
 */
import { badUtf8, fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";
import type { Memory, MemoryType } from "../../../src/api/types.ts";

const types: string[] = ["user", "preference", "person", "fact", "procedure"];
const states: string[] = ["active", "pending", "archived"];
const credential = /AKIA[A-Z0-9]{16}|sk-ant-[A-Za-z0-9_-]{20,}|-----BEGIN /;
/** flagged is what the mock's screen treats as an injection. */
const flagged = /ignore (all )?previous instructions/i;
const pinTokenBudget = 3000;
const maxPins = 12;

/** shaOf is a stand-in digest that changes whenever a memory's stored fields do. */
export function memorySha(m: Memory): string {
  const text = JSON.stringify([
    m.description,
    m.type,
    m.expires,
    m.pinned,
    m.state,
    m.body,
    m.updated,
    m.archive_reason ?? "",
  ]);
  let h = 5381;
  for (const ch of text) h = ((h * 33) ^ (ch.codePointAt(0) ?? 0)) >>> 0;
  return `sha-${h.toString(16)}`;
}

const validName = (name: string | undefined): name is string =>
  name !== undefined && /^[a-z0-9][a-z0-9-]{0,63}$/.test(name);

export function memoryRoutes({ w, on, now, emit, event }: RouteContext): void {
  const missing = () => fail(404, "not_found", "no memory with that name");
  const find = (name: string | undefined) =>
    validName(name) ? w.memories.find((m) => m.name === name) : undefined;
  const view = (m: Memory): Memory => ({ ...m, content_sha: memorySha(m) });
  const stale = () =>
    fail(409, "conflict", "this memory changed since it was read; reload it and try again");
  /** changed publishes what the server does after a durable change: memory.changed, then the pending count. */
  const changed = (name: string, state: string) => {
    emit(event("memory.changed", "inbox", state === "" ? { name } : { name, state }));
    emit(
      event("inbox.count", "inbox", {
        pending: w.memories.filter((m) => m.state === "pending").length,
      }),
    );
  };
  const touch = (m: Memory) => {
    m.updated = now();
    changed(m.name, m.state);
  };
  const pinnedTokens = (extra: Memory) =>
    [...w.memories.filter((m) => m.pinned && m.state === "active" && m.name !== extra.name), extra]
      .map((m) => Math.ceil((m.body.length + m.name.length + 8) / 4))
      .reduce((a, b) => a + b, 12);
  const pinFails = (m: Memory) => {
    if (m.state !== "active")
      return fail(
        409,
        "conflict",
        "only an active memory can be pinned; accept a pending one first",
      );
    const others = w.memories.filter((o) => o.pinned && o.state === "active" && o.name !== m.name);
    if (others.length >= maxPins)
      return fail(
        409,
        "conflict",
        `too many pinned memories: limit is ${String(maxPins)}; unpin another memory first`,
      );
    const tokens = pinnedTokens(m);
    if (tokens > pinTokenBudget)
      return fail(
        409,
        "conflict",
        `pinned memories exceed the token budget: ${String(tokens)} of ${String(pinTokenBudget)} tokens; unpin another memory first`,
      );
    return undefined;
  };
  const ifSha = (m: Memory, given: unknown) =>
    given !== undefined && given !== null && given !== memorySha(m) ? stale() : undefined;
  const query = (rawQuery: string, allowed: string[]) => {
    const q = new URLSearchParams(rawQuery);
    for (const key of q.keys())
      if (!allowed.includes(key) || q.getAll(key).length !== 1)
        return fail(400, "invalid_request", `unknown query parameter "${key}"`);
    return undefined;
  };

  on("GET", "/api/memories", ({ query: q, rawQuery }) => {
    const bad = query(rawQuery, [
      "q",
      "type",
      "state",
      "pinned",
      "include_archived",
      "limit",
      "after",
    ]);
    if (bad) return bad;
    const text = q.get("q")?.trim();
    const rawLimit = q.get("limit");
    if (rawLimit !== null && (!/^[1-9][0-9]*$/.test(rawLimit) || Number(rawLimit) > 50))
      return fail(400, "invalid_request", "limit must be an integer from 1 to 50");
    const limit = rawLimit === null ? 20 : Number(rawLimit);
    const type = q.get("type");
    if (type !== null && !types.includes(type)) return fail(400, "invalid_request", "invalid type");
    const state = q.get("state");
    if (state !== null && !states.includes(state))
      return fail(400, "invalid_request", "invalid state");
    for (const key of ["pinned", "include_archived"])
      if (q.has(key) && q.get(key) !== "true" && q.get(key) !== "false")
        return fail(400, "invalid_request", `${key} must be true or false`);
    if (q.has("q") && (!text || text.length > 200))
      return fail(400, "invalid_request", "q must be 1 to 200 characters");
    if (text) {
      if (q.has("after") || q.has("state") || q.has("pinned"))
        return fail(
          400,
          "invalid_request",
          "a search takes only q, type, include_archived and limit",
        );
      const words = text.toLowerCase().split(/\s+/);
      const includeArchived = q.get("include_archived") === "true";
      const hits = w.memories
        .filter(
          (m) =>
            (type === null || m.type === type) &&
            (includeArchived || m.state !== "archived") &&
            words.every((x) => `${m.name} ${m.description} ${m.body}`.toLowerCase().includes(x)),
        )
        .slice(0, limit)
        .map((m) => ({
          name: m.name,
          description: m.description,
          type: m.type,
          state: m.state,
          pinned: m.pinned,
          snippet: m.body.slice(0, 200),
        }));
      return ok({ memories: hits });
    }
    if (q.get("include_archived") === "true")
      return fail(
        400,
        "invalid_request",
        "include_archived applies to a search; use state to list archived memories",
      );
    const sorted = w.memories
      .filter(
        (m) =>
          (type === null || m.type === type) &&
          (state === null || m.state === state) &&
          (!q.has("pinned") || m.pinned === (q.get("pinned") === "true")),
      )
      .toSorted((a, b) => b.updated.localeCompare(a.updated) || a.name.localeCompare(b.name));
    const after = q.get("after");
    let from = 0;
    if (after !== null) {
      const at = sorted.findIndex((m) => m.name === after);
      if (at < 0) return fail(400, "invalid_request", "invalid after cursor");
      from = at + 1;
    }
    const page = sorted.slice(from, from + limit);
    const items = page.map((m) => ({
      name: m.name,
      description: m.description,
      type: m.type,
      state: m.state,
      pinned: m.pinned,
      ...(m.expires ? { expires: m.expires } : {}),
      updated: m.updated,
      content_sha: memorySha(m),
    }));
    const more = from + limit < sorted.length;
    return ok({ memories: items, ...(more ? { next_cursor: page[page.length - 1]?.name } : {}) });
  });

  on("GET", "/api/memories/{name}", ({ params }) => {
    const m = find(params[0]);
    return m === undefined ? missing() : ok(view(m));
  });

  on("PATCH", "/api/memories/{name}", (req) => {
    const bytes = badUtf8(req);
    if (bytes) return bytes;
    const { params, body, raw } = req;
    const m = find(params[0]);
    if (m === undefined) return missing();
    if (raw.trim() === "null")
      return fail(400, "invalid_request", "request body must be an object");
    const fields = ["content_sha", "description", "type", "body", "expires"];
    if (!Object.keys(body).every((k) => fields.includes(k)))
      return fail(400, "invalid_request", "unknown field");
    if (typeof body.content_sha !== "string" || body.content_sha === "")
      return fail(400, "invalid_request", "content_sha is required");
    if (fields.slice(1).every((k) => body[k] === undefined))
      return fail(400, "invalid_request", "nothing to change");
    if (body.content_sha !== memorySha(m)) return stale();
    const next: Memory = { ...m };
    if (body.description !== undefined) {
      if (typeof body.description !== "string")
        return fail(400, "invalid_request", "invalid description");
      next.description = body.description;
    }
    if (body.type !== undefined) {
      if (typeof body.type !== "string" || !types.includes(body.type))
        return fail(400, "invalid_request", "invalid type");
      next.type = body.type as MemoryType;
    }
    if (body.body !== undefined) {
      if (typeof body.body !== "string" || new TextEncoder().encode(body.body).length > 2048)
        return fail(400, "invalid_request", "invalid body");
      next.body = body.body;
    }
    if (body.expires !== undefined) {
      if (
        typeof body.expires !== "string" ||
        (body.expires !== "" && !/^\d{4}-\d{2}-\d{2}$/.test(body.expires))
      )
        return fail(400, "invalid_request", "invalid expiry");
      next.expires = body.expires;
    }
    const length = Array.from(next.description).length;
    if (length < 20 || length > 200 || /[\r\n]/.test(next.description))
      return fail(400, "invalid_request", "invalid description");
    if (next.description !== m.description || next.body !== m.body) {
      const text = `${next.description}\n${next.body}`;
      if (credential.test(text))
        return fail(
          400,
          "invalid_request",
          "this memory contains a credential in its body. Remove it and save the memory again without it.",
        );
      if (flagged.test(text)) {
        const span = flagged.exec(next.body) ?? flagged.exec(next.description);
        next.screen = {
          spans: span
            ? [
                {
                  field: flagged.test(next.body) ? "body" : "description",
                  start: span.index,
                  end: span.index + span[0].length,
                  score: 0.97,
                  text: span[0],
                },
              ]
            : [],
          max_score: 0.97,
          unscanned: false,
        };
        if (m.state === "active") {
          next.state = "pending";
          next.pinned = false;
        }
      } else if (m.state === "pending") {
        next.screen = { spans: [], max_score: 0, unscanned: false };
      }
    }
    if (next.pinned) {
      const refused = pinFails(next);
      if (refused) return refused;
    }
    Object.assign(m, next);
    touch(m);
    return ok(view(m));
  });

  const pin =
    (pinned: boolean) =>
    (req: { params: string[]; body: Record<string, unknown>; raw: string }) => {
      const m = find(req.params[0]);
      if (m === undefined) return missing();
      if (!Object.keys(req.body).every((k) => k === "content_sha"))
        return fail(400, "invalid_request", "unknown field");
      const refused = ifSha(m, req.body.content_sha);
      if (refused) return refused;
      if (m.pinned === pinned) return ok(view(m));
      if (pinned) {
        const no = pinFails({ ...m, pinned: true });
        if (no) return no;
      }
      m.pinned = pinned;
      touch(m);
      return ok(view(m));
    };
  on("POST", "/api/memories/{name}/pin", pin(true));
  on("POST", "/api/memories/{name}/unpin", pin(false));

  on("POST", "/api/memories/{name}/archive", ({ params, body }) => {
    const m = find(params[0]);
    if (m === undefined) return missing();
    if (!Object.keys(body).every((k) => k === "content_sha" || k === "reason"))
      return fail(400, "invalid_request", "unknown field");
    const refused = ifSha(m, body.content_sha);
    if (refused) return refused;
    let reason = "archived by the user";
    if (body.reason !== undefined) {
      const given = typeof body.reason === "string" ? body.reason.trim() : "";
      if (given === "" || Array.from(given).length > 200)
        return fail(400, "invalid_request", "reason must be 1 to 200 characters");
      reason = given;
    }
    m.state = "archived";
    m.archive_reason = reason;
    touch(m);
    return ok(view(m));
  });

  on("DELETE", "/api/memories/{name}", ({ params, query: q, rawQuery }) => {
    const bad = query(rawQuery, ["content_sha"]);
    if (bad) return bad;
    const m = find(params[0]);
    if (m === undefined) return missing();
    const refused = ifSha(m, q.get("content_sha") ?? undefined);
    if (refused) return refused;
    w.memories = w.memories.filter((o) => o !== m);
    changed(m.name, "");
    return { status: 204 };
  });

  const pending = () =>
    w.memories
      .filter((m) => m.state === "pending")
      .toSorted((a, b) => b.updated.localeCompare(a.updated) || a.name.localeCompare(b.name));

  on("GET", "/api/inbox/count", () => ok({ pending: pending().length }));

  on("GET", "/api/inbox/memories", ({ query: q, rawQuery }) => {
    const bad = query(rawQuery, ["limit", "after"]);
    if (bad) return bad;
    const rawLimit = q.get("limit");
    if (rawLimit !== null && (!/^[1-9][0-9]*$/.test(rawLimit) || Number(rawLimit) > 50))
      return fail(400, "invalid_request", "limit must be an integer from 1 to 50");
    const limit = rawLimit === null ? 20 : Number(rawLimit);
    const rows = pending();
    const after = q.get("after");
    let from = 0;
    if (after !== null) {
      const at = rows.findIndex((m) => m.name === after);
      if (at < 0) return fail(400, "invalid_request", "invalid after cursor");
      from = at + 1;
    }
    const page = rows.slice(from, from + limit);
    return ok({
      pending: rows.length,
      memories: page.map(view),
      ...(from + limit < rows.length ? { next_cursor: page[page.length - 1]?.name } : {}),
    });
  });

  /** answerable finds the memory an answer is for, requiring content_sha and a pending memory. */
  const answerable = (name: string | undefined, body: Record<string, unknown>) => {
    const m = find(name);
    if (m === undefined) return missing();
    if (typeof body.content_sha !== "string" || body.content_sha === "")
      return fail(400, "invalid_request", "content_sha is required");
    if (body.content_sha !== memorySha(m)) return stale();
    if (m.state !== "pending")
      return fail(409, "conflict", "this memory is not pending; it was already answered");
    return m;
  };

  on("POST", "/api/inbox/memories/{name}/accept", (req) => {
    const bytes = badUtf8(req);
    if (bytes) return bytes;
    const { params, body } = req;
    const fields = ["content_sha", "description", "type", "body", "expires"];
    if (!Object.keys(body).every((k) => fields.includes(k)))
      return fail(400, "invalid_request", "unknown field");
    const m = answerable(params[0], body);
    if (!("state" in m)) return m;
    const next: Memory = { ...m };
    if (typeof body.description === "string") next.description = body.description;
    if (typeof body.type === "string" && types.includes(body.type))
      next.type = body.type as MemoryType;
    if (typeof body.body === "string") next.body = body.body;
    if (typeof body.expires === "string") next.expires = body.expires;
    // A credential is refused on accept whatever the memory's history; a flag
    // is not, because accepting is the user's explicit promotion.
    if (credential.test(`${next.description}\n${next.body}`))
      return fail(
        400,
        "invalid_request",
        "this memory contains a credential in its body. Remove it and save the memory again without it.",
      );
    Object.assign(m, next, { state: "active" });
    delete m.screen;
    touch(m);
    return ok(view(m));
  });

  on("POST", "/api/inbox/memories/{name}/reject", ({ params, body }) => {
    if (!Object.keys(body).every((k) => k === "content_sha" || k === "reason"))
      return fail(400, "invalid_request", "unknown field");
    const m = answerable(params[0], body);
    if (!("state" in m)) return m;
    let reason = "rejected by the user";
    if (body.reason !== undefined) {
      const given = typeof body.reason === "string" ? body.reason.trim() : "";
      if (given === "" || Array.from(given).length > 200)
        return fail(400, "invalid_request", "reason must be 1 to 200 characters");
      reason = given;
    }
    m.state = "archived";
    m.archive_reason = reason;
    touch(m);
    return ok(view(m));
  });
}
