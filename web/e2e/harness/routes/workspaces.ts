/** Workspaces, their sandboxes, and their state. */

import type { Sandbox, Workspace } from "../../../src/api/types.ts";
import { defaultSandbox } from "../world.ts";
import { checkName, fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

/** noEgressControl is the harness's refusal of restricted egress it cannot give. */
const noEgressControl =
  "restricting a sandbox's network needs the internal sandbox network, which this harness is not configured with";

/** isObject narrows a JSON value to an object. */
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** isSandbox narrows a request body to a sandbox. */
function isSandbox(value: unknown): value is Sandbox {
  return isObject(value) && isObject(value.limits) && isObject(value.egress);
}

/** restricted reports whether a sandbox keeps its workspace off the open network. */
function restricted(sandbox: Sandbox): boolean {
  return sandbox.egress.mode !== "open";
}

export function workspaceRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/workspaces", ({ query }) => {
    const project = query.get("project_id");
    return ok({
      workspaces: project ? w.workspaces.filter((x) => x.project_id === project) : w.workspaces,
    });
  });
  on("POST", "/api/workspaces", ({ body }) => {
    const project = find(w.projects, str(body.project_id), "project");
    if ("status" in project) return project;
    const name = str(body.name);
    const row: Workspace = {
      id: ctx.nextId("ws"),
      project_id: project.id,
      name,
      branch: str(body.branch) || `eika/${name}`,
      base_commit: "3f9c2a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a",
      image: str(body.image) || w.settings.defaults.sandbox_image,
      state: "running",
      sandbox: isSandbox(body.sandbox) ? body.sandbox : newSandbox(),
      pinned: false,
      archived: false,
      created_at: now(),
      updated_at: now(),
    };
    if (restricted(row.sandbox) && !w.system.sandbox.egress_control) {
      return fail(400, "invalid_request", noEgressControl);
    }
    w.workspaces.unshift(row);
    return ok(row, 201);
  });
  // A new workspace's sandbox: the settings' defaults, as the harness reads them.
  const newSandbox = (): Sandbox => {
    const base = defaultSandbox();
    const limits = w.settings.settings.sandbox_limits;
    const egress = w.settings.settings.sandbox_egress;
    return {
      limits: isObject(limits)
        ? { ...w.settings.defaults.sandbox_limits, ...limits }
        : w.settings.defaults.sandbox_limits,
      egress: isObject(egress)
        ? { ...w.settings.defaults.sandbox_egress, ...egress }
        : w.settings.defaults.sandbox_egress,
      ports: base.ports,
    };
  };
  on("PATCH", "/api/workspaces/{id}", ({ params, body }) => {
    const row = find(w.workspaces, params[0], "workspace");
    if ("status" in row) return row;
    if (body.name !== undefined) {
      const name = checkName("name", body.name);
      if (typeof name !== "string") return name;
      row.name = name;
    }
    if (typeof body.pinned === "boolean") row.pinned = body.pinned;
    if (typeof body.archived === "boolean") row.archived = body.archived;
    row.updated_at = now();
    ctx.emit(
      ctx.event("workspace.state", `workspace:${row.id}`, {
        workspace_id: row.id,
        project_id: row.project_id,
        state: row.state,
      }),
    );
    return ok(row);
  });
  on("PUT", "/api/workspaces/{id}/sandbox", ({ params, body }) => {
    const row = find(w.workspaces, params[0], "workspace");
    if ("status" in row) return row;
    if (!isSandbox(body))
      return fail(400, "invalid_request", "a sandbox has limits, egress, and ports");
    if (restricted(body) && !w.system.sandbox.egress_control) {
      return fail(400, "invalid_request", noEgressControl);
    }
    row.sandbox = { ...body, ports: body.ports ?? [] };
    row.updated_at = now();
    ctx.emit(
      ctx.event("workspace.state", `workspace:${row.id}`, {
        workspace_id: row.id,
        project_id: row.project_id,
        state: row.state,
      }),
    );
    return ok(row);
  });
  on("GET", "/api/workspaces/{id}/usage", ({ params }) => {
    const row = find(w.workspaces, params[0], "workspace");
    if ("status" in row) return row;
    if (row.state !== "running") {
      return fail(409, "conflict", `workspace ${row.id} is ${row.state}, not running`);
    }
    const limits = row.sandbox.limits;
    const memoryLimit =
      limits.memory_mb > 0 ? limits.memory_mb * 2 ** 20 : w.system.sandbox.memory_bytes;
    return ok({
      cpu_percent: 37.5,
      cpus: limits.cpus > 0 ? limits.cpus : w.system.sandbox.cpus,
      memory_bytes: 612 * 2 ** 20,
      memory_limit_bytes: memoryLimit,
      pids: 23,
      pids_limit: limits.pids,
      network_rx_bytes: 48_300_000,
      network_tx_bytes: 2_150_000,
      sampled_at: now(),
      blocked: w.blocked[row.id] ?? [],
    });
  });
  on("POST", "/api/workspaces/{id}/ports/{port}/preview", ({ params }) => {
    const row = find(w.workspaces, params[0], "workspace");
    if ("status" in row) return row;
    const port = Number(params[1]);
    if (!(row.sandbox.ports ?? []).some((p) => p.port === port)) {
      return fail(404, "not_found", `workspace ${row.id} does not forward port ${String(port)}`);
    }
    if (row.state !== "running") {
      return fail(409, "conflict", `workspace ${row.id} is ${row.state}, not running`);
    }
    return ok({
      url: `http://${String(port)}-${row.id}.localhost:8080/__eika/preview?ticket=mock`,
    });
  });
  on("GET", "/api/workspaces/{id}", ({ params }) => {
    const row = find(w.workspaces, params[0], "workspace");
    return "status" in row ? row : ok(row);
  });
  const setState = (id: string | undefined, state: Workspace["state"]) => {
    const row = find(w.workspaces, id, "workspace");
    if ("status" in row) return row;
    row.state = state;
    row.updated_at = now();
    ctx.emit(
      ctx.event("workspace.state", `workspace:${row.id}`, {
        workspace_id: row.id,
        project_id: row.project_id,
        state,
      }),
    );
    return ok(row);
  };
  on("POST", "/api/workspaces/{id}/start", ({ params }) => setState(params[0], "running"));
  on("POST", "/api/workspaces/{id}/stop", ({ params }) => setState(params[0], "stopped"));
  on("DELETE", "/api/workspaces/{id}", ({ params }) => {
    w.workspaces = w.workspaces.filter((x) => x.id !== params[0]);
    w.sessions = w.sessions.filter((s) => s.workspace_id !== params[0]);
    return { status: 204 };
  });
  on("GET", "/api/workspaces/{id}/diff", ({ params }) => {
    const row = find(w.workspaces, params[0], "workspace");
    if ("status" in row) return row;
    if (row.state !== "running") return fail(409, "conflict", "workspace is not running");
    return ok(
      w.diffs[row.id] ?? {
        workspace_id: row.id,
        base_commit: row.base_commit,
        diff: "",
        status: "",
      },
    );
  });
}
