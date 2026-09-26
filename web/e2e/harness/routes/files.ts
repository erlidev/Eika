/** Files, commit, and push: the Files, Terminal, and Changes panels. */

import type { Workspace } from "../../../src/api/types.ts";
import { minutesAgo } from "../world.ts";
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

/** mockCommit is the commit every commit and push reports. */
const mockCommit = "9b2e4c1f7a3d5e6b8c0a1f2e3d4c5b6a7f8e9d0c";

export function fileRoutes(ctx: RouteContext): void {
  const { w, on, now, running } = ctx;
  const filesOf = (id: string) => (w.files[id] ??= {});
  const entry = (id: string, path: string) => {
    const files = filesOf(id);
    const isDir = !(path in files);
    const content = files[path] ?? "";
    return {
      name: path.split("/").at(-1) ?? path,
      path,
      size: isDir ? 4096 : Buffer.byteLength(content),
      mode: isDir ? 0o755 : 0o644,
      mod_time: minutesAgo(30),
      is_dir: isDir,
    };
  };
  const diffOf = (row: Workspace) =>
    (w.diffs[row.id] ??= {
      workspace_id: row.id,
      base_commit: row.base_commit,
      diff: "",
      status: "",
    });
  const changed = (row: Workspace) => {
    row.updated_at = now();
    ctx.emit(
      ctx.event("workspace.state", `workspace:${row.id}`, {
        workspace_id: row.id,
        project_id: row.project_id,
        state: row.state,
      }),
    );
  };
  on("GET", "/api/workspaces/{id}/files", ({ params, query }) => {
    const row = running(params[0]);
    if ("status" in row) return row;
    const dir = query.get("path") ?? "";
    const prefix = dir === "" ? "" : `${dir}/`;
    const children = new Set<string>();
    for (const path of Object.keys(filesOf(row.id))) {
      if (!path.startsWith(prefix)) continue;
      const name = path.slice(prefix.length).split("/")[0] ?? "";
      if (name !== "") children.add(prefix + name);
    }
    if (dir !== "" && children.size === 0) return fail(404, "not_found", "no such directory");
    const entries = [...children].map((path) => entry(row.id, path));
    entries.sort((a, b) =>
      a.is_dir !== b.is_dir ? (a.is_dir ? -1 : 1) : a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
    );
    return ok({ entries });
  });
  on("GET", "/api/workspaces/{id}/file", ({ params, query }) => {
    const row = running(params[0]);
    if ("status" in row) return row;
    const path = query.get("path") ?? "";
    const content = filesOf(row.id)[path];
    if (content === undefined) return fail(404, "not_found", "no such file");
    const size = Buffer.byteLength(content);
    const binary = content.includes("\u0000");
    const tooLarge = size > 2 << 20;
    return ok({
      path,
      size,
      binary,
      too_large: tooLarge,
      content: binary || tooLarge ? "" : content,
    });
  });
  on("PUT", "/api/workspaces/{id}/file", ({ params, query, raw }) => {
    const row = running(params[0]);
    if ("status" in row) return row;
    const path = query.get("path") ?? "";
    if (path === "" || path.startsWith("/") || path.split("/").includes("..")) {
      return fail(403, "forbidden", "path escapes the workspace");
    }
    const files = filesOf(row.id);
    const isNew = !(path in files);
    files[path] = raw;
    const diff = diffOf(row);
    if (!diff.status.split("\n").some((line) => line.slice(3) === path)) {
      diff.status += `${isNew ? "??" : " M"} ${path}\n`;
    }
    changed(row);
    return ok(entry(row.id, path));
  });
  on("POST", "/api/workspaces/{id}/commit", ({ params, body }) => {
    const row = running(params[0]);
    if ("status" in row) return row;
    if (str(body.message).trim() === "") {
      return fail(400, "invalid_request", "message is required");
    }
    const diff = diffOf(row);
    if (diff.status.trim() === "") return fail(409, "conflict", "nothing to commit");
    diff.status = "";
    changed(row);
    return ok({ commit: mockCommit });
  });
  on("POST", "/api/workspaces/{id}/push", ({ params, body }) => {
    const row = running(params[0]);
    if ("status" in row) return row;
    const upstream = body.upstream === true;
    const project = w.projects.find((p) => p.id === row.project_id);
    if (upstream && (project?.remote_url ?? "") === "") {
      return fail(400, "invalid_request", "the project has no remote to push to");
    }
    changed(row);
    return ok({ branch: row.branch, commit: mockCommit, upstream_pushed: upstream });
  });
}
