/** Project metadata routes. Instructions have a separate user-only API. */
import type { Project, ProjectFields } from "../../../src/api/types.ts";
import { fixedNow } from "../world.ts";
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

const bytes = (s: string) => new TextEncoder().encode(s).length;
const validSlug = (s: string) =>
  s.trim() === s && bytes(s) <= 80 && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(s);
const validFields = (p: ProjectFields) =>
  p.name.trim() !== "" &&
  bytes(p.name) <= 200 &&
  bytes(p.description) <= 4096 &&
  !p.name.includes("\0") &&
  !p.description.includes("\0") &&
  ["active", "paused", "done", "archived"].includes(p.status) &&
  bytes(p.directory) <= 4096 &&
  p.directory.startsWith("/") &&
  p.directory !== "/" &&
  !p.directory.endsWith("/") &&
  !p.directory.includes("//") &&
  !p.directory.split("/").some((part) => part === "." || part === "..") &&
  !/[\\\p{Cc}]/u.test(p.directory);

export function projectRoutes({ w, on, nextId }: RouteContext): void {
  const invalid = () => fail(400, "invalid_request", "invalid project metadata");
  const missing = () => fail(404, "not_found", "project does not exist");
  on("GET", "/api/projects", () =>
    ok({
      projects: [...w.projects].sort((a, b) => (a.slug < b.slug ? -1 : a.slug > b.slug ? 1 : 0)),
    }),
  );
  on("GET", "/api/projects/{id}", ({ params }) => {
    const project = w.projects.find((p) => p.id === params[0]);
    return project ? ok(project) : missing();
  });
  on("POST", "/api/projects", ({ body, raw }) => {
    if (raw.trim() === "null") return invalid();
    if (typeof body.slug !== "string" || !validSlug(body.slug)) return invalid();
    const fields = {
      name: body.name ?? "",
      description: body.description ?? "",
      status: body.status === "" ? "active" : (body.status ?? "active"),
      directory:
        body.directory === "" || body.directory == null
          ? `/home/eika/projects/${body.slug}`
          : body.directory,
    } as ProjectFields;
    if (!validFields(fields)) return invalid();
    if (w.projects.some((p) => p.slug === body.slug))
      return fail(409, "conflict", "project slug already exists");
    const project: Project = {
      ...fields,
      id: nextId("prj"),
      slug: body.slug,
      created_at: fixedNow,
      updated_at: fixedNow,
    };
    w.projects.push(project);
    return ok(project, 201);
  });
  on("PATCH", "/api/projects/{id}", ({ params, body, raw }) => {
    if (raw.trim() === "null") return invalid();
    const project = w.projects.find((p) => p.id === params[0]);
    if (!project) return missing();
    const fields = {
      name: body.name ?? project.name,
      description: body.description ?? project.description,
      status: body.status ?? project.status,
      directory: body.directory ?? project.directory,
    } as ProjectFields;
    if (!validFields(fields)) return invalid();
    Object.assign(project, fields, { updated_at: fixedNow });
    return ok(project);
  });
}
