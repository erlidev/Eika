/** The settings, the system check, and the tool list. */

import { syncSession } from "../profiles.ts";
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function settingsRoutes(ctx: RouteContext): void {
  const { w, on } = ctx;
  on("GET", "/api/settings", () => ok(w.settings));
  on("PUT", "/api/settings", ({ body }) => {
    const named = body.default_profile;
    if (typeof named === "string" && named !== "" && !w.profiles.some((p) => p.id === named)) {
      return fail(
        400,
        "invalid_request",
        `default_profile names "${named}", which is not a profile`,
      );
    }
    const utility = body.utility_models;
    if (utility !== undefined && utility !== null) {
      const names: unknown[] =
        typeof utility === "object" ? Object.values(utility as Record<string, unknown>) : [utility];
      const unknown = names.find(
        (n) => typeof n !== "string" || (n !== "" && !w.models.some((m) => m.name === n)),
      );
      if (unknown !== undefined) {
        return fail(
          400,
          "invalid_request",
          `utility_models names ${JSON.stringify(unknown)}, which is not a configured model`,
        );
      }
    }
    const compaction = body.compaction;
    if (typeof compaction === "object" && compaction !== null) {
      const c = compaction as Record<string, unknown>;
      for (const field of ["reserve_tokens", "keep_recent_tokens"]) {
        const n = c[field];
        if (n !== undefined && (typeof n !== "number" || n < 1024 || n > 1 << 20)) {
          return fail(
            400,
            "invalid_request",
            `compaction.${field} must be from 1024 to ${String(1 << 20)}`,
          );
        }
      }
    }
    for (const [key, value] of Object.entries(body)) {
      if (value === null) Reflect.deleteProperty(w.settings.settings, key);
      else w.settings.settings[key] = value;
    }
    if (typeof body.default_model === "string") w.defaultModel = body.default_model;
    // A new default profile can change what every session offers.
    for (const session of w.sessions) syncSession(w, session);
    return ok(w.settings);
  });
  on("GET", "/api/system", () => ok(w.system));
  on("GET", "/api/tools", () => ok({ tools: w.tools }));
}
