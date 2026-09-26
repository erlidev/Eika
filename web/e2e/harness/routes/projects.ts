/** Projects. */

import type { Project } from "../../../src/api/types.ts";
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function projectRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/projects", () => ok({ projects: w.projects }));
  on("POST", "/api/projects", ({ body }) => {
    if (str(body.name) === "") return fail(400, "invalid_request", "name is required");
    if (w.projects.some((p) => p.name === body.name)) {
      return fail(409, "conflict", `a project named ${str(body.name)} exists`);
    }
    const row: Project = {
      id: ctx.nextId("proj"),
      name: str(body.name),
      kind: body.kind === "local" ? "local" : "remote",
      default_branch: str(body.default_branch) || "main",
      created_at: now(),
      ...(body.remote_url ? { remote_url: str(body.remote_url) } : {}),
      ...(body.host_path ? { host_path: str(body.host_path) } : {}),
      ...(body.remote_username ? { remote_username: str(body.remote_username) } : {}),
      ...(body.remote_password ? { remote_password_set: true } : {}),
    };
    w.projects.unshift(row);
    return ok(row, 201);
  });
  on("GET", "/api/projects/{id}", ({ params }) => {
    const row = find(w.projects, params[0], "project");
    return "status" in row ? row : ok(row);
  });
  on("PATCH", "/api/projects/{id}", ({ params, body }) => {
    const row = find(w.projects, params[0], "project");
    if ("status" in row) return row;
    if (typeof body.default_branch === "string") row.default_branch = body.default_branch;
    if (typeof body.remote_username === "string") row.remote_username = body.remote_username;
    if (typeof body.remote_password === "string") {
      row.remote_password_set = body.remote_password !== "";
    }
    return ok(row);
  });
  on("DELETE", "/api/projects/{id}", ({ params }) => {
    const gone = new Set(w.workspaces.filter((x) => x.project_id === params[0]).map((x) => x.id));
    w.projects = w.projects.filter((p) => p.id !== params[0]);
    w.workspaces = w.workspaces.filter((x) => !gone.has(x.id));
    w.sessions = w.sessions.filter(
      (s) => s.workspace_id === undefined || !gone.has(s.workspace_id),
    );
    return { status: 204 };
  });
}
