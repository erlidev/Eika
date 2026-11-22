/** The withheld list: what ingest removed, newest first, paged by row id. */
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function withheldRoutes({ w, on }: RouteContext): void {
  on("GET", "/api/withheld", ({ query, rawQuery }) => {
    if (rawQuery.includes(";") || /%(?![0-9a-fA-F]{2})/.test(rawQuery))
      return fail(400, "invalid_request", "invalid pagination query");
    for (const key of query.keys()) {
      if ((key !== "before" && key !== "limit") || query.getAll(key).length !== 1)
        return fail(400, "invalid_request", "invalid pagination query");
    }
    const rawLimit = query.get("limit");
    if (rawLimit !== null && (!/^[1-9][0-9]*$/.test(rawLimit) || Number(rawLimit) > 100))
      return fail(400, "invalid_request", "limit must be an integer from 1 to 100");
    const limit = rawLimit === null ? 50 : Number(rawLimit);
    const newest = w.withheld.toSorted(
      (a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id),
    );
    const before = query.get("before");
    let eligible = newest;
    if (before !== null) {
      const at = newest.findIndex((row) => row.id === before);
      if (at < 0) return fail(400, "invalid_request", "invalid before cursor");
      eligible = newest.slice(at + 1);
    }
    const page = eligible.slice(0, limit);
    return ok({
      withheld: page,
      ...(eligible.length > limit ? { next_before: page[page.length - 1]?.id } : {}),
    });
  });
  on("GET", "/api/withheld/{id}", ({ params }) => {
    const row = w.withheld.find((r) => r.id === params[0]);
    return row === undefined ? fail(404, "not_found", "withheld row does not exist") : ok(row);
  });
}
