/** Profiles. */

import {
  inheritedProfile,
  profileBody,
  profilesBody,
  samplingProblem,
  settingsOf,
  syncSession,
} from "../profiles.ts";
import { fail, ok, str, strings } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function profileRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  const profileInput = (body: Record<string, unknown>) => {
    const name = str(body.name).trim();
    if (name === "") return fail(400, "invalid_request", "name must be 1 to 64 characters");
    const settings = settingsOf(body);
    const problem = samplingProblem(settings.sampling);
    if (problem !== undefined) return fail(400, "invalid_request", problem);
    if (settings.model_id !== undefined && !w.models.some((m) => m.id === settings.model_id)) {
      return fail(400, "invalid_request", `model ${settings.model_id} does not exist`);
    }
    const tools = Array.isArray(body.tools) ? [...new Set(strings(body.tools))].sort() : null;
    return { name, description: str(body.description).trim(), ...settings, tools };
  };
  on("GET", "/api/profiles", () => ok(profilesBody(w)));
  on("GET", "/api/profiles/inherited", ({ query }) => {
    const modelId = query.get("model_id") ?? "";
    if (modelId !== "" && !w.models.some((m) => m.id === modelId)) {
      return fail(400, "invalid_request", `model ${modelId} does not exist`);
    }
    return ok(inheritedProfile(w, modelId));
  });
  on("POST", "/api/profiles", ({ body }) => {
    const input = profileInput(body);
    if ("status" in input) return input;
    if (w.profiles.some((p) => p.name === input.name)) {
      return fail(409, "conflict", `a profile named "${input.name}" already exists`);
    }
    const row = { id: ctx.nextId("prof"), ...input, created_at: now(), updated_at: now() };
    w.profiles.push(row);
    return ok(profileBody(w, row), 201);
  });
  on("PUT", "/api/profiles/{id}", ({ params, body }) => {
    const row = find(w.profiles, params[0], "profile");
    if ("status" in row) return row;
    const input = profileInput(body);
    if ("status" in input) return input;
    if (w.profiles.some((p) => p.name === input.name && p.id !== row.id)) {
      return fail(409, "conflict", `a profile named "${input.name}" already exists`);
    }
    const updated = { ...input, id: row.id, created_at: row.created_at, updated_at: now() };
    w.profiles = w.profiles.map((p) => (p.id === row.id ? updated : p));
    for (const session of w.sessions) syncSession(w, session);
    return ok(profileBody(w, updated));
  });
  on("DELETE", "/api/profiles/{id}", ({ params }) => {
    const row = find(w.profiles, params[0], "profile");
    if ("status" in row) return row;
    if (w.profiles.length === 1) {
      return fail(
        409,
        "conflict",
        `profile ${row.id} is the last one, and a run always needs a profile`,
      );
    }
    w.profiles = w.profiles.filter((p) => p.id !== row.id);
    for (const session of w.sessions) {
      if (session.profile_id === row.id) Reflect.deleteProperty(session, "profile_id");
      syncSession(w, session);
    }
    return { status: 204 };
  });
}
