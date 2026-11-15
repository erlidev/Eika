/** The settings, the system check, and the tool list. */

import { syncSession } from "../profiles.ts";
import { classView, modelClassNames } from "../world.ts";
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function settingsRoutes(ctx: RouteContext): void {
  const { w, on } = ctx;
  /** served is the settings table with the classes resolved, as GET and PUT answer. */
  const served = () => ({ ...w.settings, ...classView(w) });
  on("GET", "/api/settings", () => ok(served()));
  on("PUT", "/api/settings", ({ body }) => {
    for (const key of ["time_zone", "locale"] as const) {
      const value = body[key];
      if (value === undefined || value === null) continue;
      if (typeof value !== "string") return fail(400, "invalid_request", `${key} must be a string`);
      if (key === "locale" && !/^[A-Za-z0-9-]{1,64}$/.test(value))
        return fail(400, "invalid_request", "locale must be 1 to 64 letters, digits or hyphens");
      if (key === "time_zone") {
        try {
          if (!value || value === "Local") throw new Error();
          new Intl.DateTimeFormat("en", { timeZone: value });
        } catch {
          return fail(400, "invalid_request", "time_zone must be an IANA time zone");
        }
      }
    }
    const named = body.default_profile;
    if (typeof named === "string" && named !== "" && !w.profiles.some((p) => p.id === named)) {
      return fail(
        400,
        "invalid_request",
        `default_profile names "${named}", which is not a profile`,
      );
    }
    const classes = body.model_classes;
    if (classes !== undefined && classes !== null) {
      if (typeof classes !== "object") {
        return fail(
          400,
          "invalid_request",
          "model_classes must be an object of class names to model names",
        );
      }
      for (const [name, model] of Object.entries(classes)) {
        if (!(modelClassNames as readonly string[]).includes(name)) {
          return fail(
            400,
            "invalid_request",
            `model_classes names ${JSON.stringify(name)}, which is not a class`,
          );
        }
        if (model === "") continue;
        const row = w.models.find((m) => m.name === model);
        if (typeof model !== "string" || row === undefined) {
          return fail(
            400,
            "invalid_request",
            `model_classes names ${JSON.stringify(model)}, which is not a configured model`,
          );
        }
        const kind = name === "decision" ? "decision" : "chat";
        if (row.kind !== kind) {
          return fail(
            400,
            "invalid_request",
            `model_classes assigns ${JSON.stringify(model)}, a ${row.kind} model, to the ${name} class, which needs a ${kind} model`,
          );
        }
      }
    }
    const chosen = w.models.find((m) => m.name === body.default_model);
    if (chosen !== undefined && chosen.kind !== "chat") {
      return fail(
        400,
        "invalid_request",
        `default_model names ${JSON.stringify(chosen.name)}, a ${chosen.kind} model, which cannot hold a conversation`,
      );
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
    return ok(served());
  });
  on("GET", "/api/system", () => ok(w.system));
  on("GET", "/api/tools", () => ok({ tools: w.tools }));
}
