/** Search: its status, its keys, and a search to try. */

import type { SearchLimit, SearchStatus } from "../../../src/api/types.ts";
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function searchRoutes(ctx: RouteContext): void {
  const { w, on, now } = ctx;
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
}
