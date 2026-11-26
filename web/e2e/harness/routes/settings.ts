/** The settings, the system check, and the tool list. */

import { syncSession } from "../modelContext.ts";
import { classView, modelClassNames } from "../world.ts";
import { badUtf8, fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

/** maxSubagentMaxRunning is the most subagents the settings accept as running at once. */
const maxSubagentMaxRunning = 32;

/**
 * validSubagentMaxRunning checks the value as the server's decoder reads it.
 * JSON.parse turns 4.0 and 4e0 into the integer 4, which Go refuses for a
 * whole number, so the written text of the number is what counts.
 */
function validSubagentMaxRunning(value: unknown, raw: string): boolean {
  const written = /"subagent_max_running"\s*:\s*([^,}\s]+)/.exec(raw)?.[1];
  if (written !== undefined && !/^[1-9][0-9]*$/.test(written)) return false;
  return typeof value === "number" && value >= 1 && value <= maxSubagentMaxRunning;
}

/** The mock assumes that public_url is configured. */
function validNtfyTopic(value: unknown): boolean {
  if (typeof value !== "string" || /%(?![0-9a-fA-F]{2})/.test(value)) return false;
  try {
    const url = new URL(value);
    const parts = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/([^/?#]*)([^?#]*)/.exec(value);
    const authority = parts?.[1] ?? "";
    const path = (parts?.[2] ?? "").replace(/%([0-9a-fA-F]{2})/g, (_, hex: string) =>
      String.fromCharCode(Number.parseInt(hex, 16)),
    );
    return (
      (url.protocol === "https:" || url.protocol === "http:") &&
      authority !== "" &&
      !authority.includes("@") &&
      path !== "" &&
      path !== "/" &&
      url.search === "" &&
      url.hash === ""
    );
  } catch {
    return false;
  }
}

export function settingsRoutes(ctx: RouteContext): void {
  const { w, on } = ctx;
  /** served is the settings table with the classes resolved, as GET and PUT answer. */
  const served = () => ({ ...w.settings, ...classView(w) });
  on("POST", "/api/notifications/test", () =>
    w.settings.settings.ntfy_topic === true
      ? { status: 204 }
      : fail(400, "invalid_request", "configure an ntfy topic first"),
  );
  on("GET", "/api/settings", () => ok(served()));
  on("PUT", "/api/settings", (req) => {
    const { body, raw } = req;
    const bytes = badUtf8(req);
    if (bytes) return bytes;
    const topic = body.ntfy_topic;
    if (topic !== undefined && topic !== null && !validNtfyTopic(topic)) {
      return fail(400, "invalid_request", "ntfy topic requires a valid URL and public_url");
    }
    if (body.screen_required != null && typeof body.screen_required !== "boolean")
      return fail(400, "invalid_request", "screen_required must be true or false");
    if (
      body.screen_threshold != null &&
      (typeof body.screen_threshold !== "number" ||
        body.screen_threshold <= 0 ||
        body.screen_threshold > 1)
    )
      return fail(400, "invalid_request", "screen_threshold must be above 0 and at most 1");
    const wakeups = body.wakeups_max;
    if (
      wakeups != null &&
      (typeof wakeups !== "number" || !Number.isInteger(wakeups) || wakeups < 1 || wakeups > 1000)
    )
      return fail(400, "invalid_request", "wakeups_max must be a whole number from 1 to 1000");
    const running = body.subagent_max_running;
    if (running !== undefined && running !== null && !validSubagentMaxRunning(running, raw)) {
      return fail(
        400,
        "invalid_request",
        `subagent_max_running must be a whole number of subagents from 1 to ${String(maxSubagentMaxRunning)}`,
      );
    }
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
      if (key === "ntfy_topic") {
        w.settings.settings[key] = typeof value === "string" && value.length > 0;
        continue;
      }
      if (value === null) Reflect.deleteProperty(w.settings.settings, key);
      else w.settings.settings[key] = value;
    }
    if (typeof body.default_model === "string") w.defaultModel = body.default_model;
    // Agent definitions determine each session’s tools.
    for (const session of w.sessions) syncSession(w, session);
    return ok(served());
  });
  on("GET", "/api/system", () => ok(w.system));
  on("GET", "/api/tools", () => ok({ tools: w.tools }));
}
