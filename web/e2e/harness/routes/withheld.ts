/** The withheld list: what ingest removed, newest first, paged by row id. */
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

/** normalizeHost follows the server's exact-host validation. */
function normalizeHost(value: unknown): string {
  if (typeof value !== "string") return "";
  const input = value.replace(/\.$/, "").toLowerCase();
  if (
    !input ||
    input.length > 253 ||
    /[\s/@?#%]/.test(input) ||
    input.includes("[") ||
    input.includes("]")
  )
    return "";
  let host: string;
  try {
    host = new URL(input.includes(":") ? `http://[${input}]/` : `http://${input}/`).hostname;
  } catch {
    return "";
  }
  host = host.replace(/^\[|\]$/g, "");
  if (host.includes(":")) return host;
  return host.length <= 253 &&
    host
      .split(".")
      .every((part) => part.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(part))
    ? host
    : "";
}

export function withheldRoutes({ w, on }: RouteContext): void {
  const setTrust = (value: unknown, trusted: boolean) => {
    const host = normalizeHost(value);
    if (!host) return fail(400, "invalid_request", "invalid host");
    w.trustedSources = w.trustedSources.filter((item) => item !== host);
    if (trusted) w.trustedSources.push(host);
    return { status: 204 };
  };
  on("GET", "/api/trusted-sources", () => ok({ hosts: w.trustedSources.toSorted() }));
  on("POST", "/api/trusted-sources", ({ body }) => setTrust(body.host, true));
  on("DELETE", "/api/trusted-sources/{host}", ({ params }) => setTrust(params[0], false));
  on("POST", "/api/withheld/{id}/trust", ({ params }) => {
    const row = w.withheld.find((r) => r.id === params[0]);
    if (row === undefined) return fail(404, "not_found", "withheld row does not exist");
    if (row.source !== "web_fetch" && row.source !== "web_search")
      return fail(400, "invalid_request", "withheld source has no web host");
    try {
      const url = new URL(row.source_name);
      if (url.protocol !== "http:" && url.protocol !== "https:") throw new Error();
      return setTrust(url.hostname.replace(/^\[|\]$/g, ""), true);
    } catch {
      return fail(400, "invalid_request", "withheld source has no web host");
    }
  });
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
