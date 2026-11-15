/** User-only instruction routes, separate from project metadata. */
import { badUtf8, fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

const validText = (text: unknown): text is string =>
  typeof text === "string" &&
  !text.includes("\0") &&
  new TextEncoder().encode(text).length <= 65536;

const hasOnlyFields = (body: Record<string, unknown>, fields: string[]) =>
  Object.keys(body).every((key) => fields.includes(key));

export function instructionRoutes({ w, on }: RouteContext): void {
  const invalid = () => fail(400, "invalid_request", "invalid instruction text");
  const missing = () => fail(404, "not_found", "project does not exist");
  on("GET", "/api/instructions", () => ok(w.instructions));
  on("PUT", "/api/instructions", (req) => {
    const { body, raw } = req;
    const bytes = badUtf8(req);
    if (bytes) return bytes;
    if (raw.trim() === "null") return invalid();
    if (!hasOnlyFields(body, ["about_me", "rules"])) return invalid();
    if (!validText(body.about_me) || !validText(body.rules)) return invalid();
    w.instructions = { about_me: body.about_me, rules: body.rules };
    return ok(w.instructions);
  });
  on("DELETE", "/api/instructions", () => {
    w.instructions = { about_me: "", rules: "" };
    return { status: 204 };
  });
  on("GET", "/api/projects/{id}/instructions", ({ params }) => {
    const id = params[0];
    if (id === undefined || !w.projects.some((p) => p.id === id)) return missing();
    return ok({ instructions: w.projectInstructions[id] ?? "" });
  });
  on("PUT", "/api/projects/{id}/instructions", (req) => {
    const { params, body, raw } = req;
    const bytes = badUtf8(req);
    if (bytes) return bytes;
    if (raw.trim() === "null") return invalid();
    if (!hasOnlyFields(body, ["instructions"])) return invalid();
    if (!validText(body.instructions)) return invalid();
    const id = params[0];
    if (id === undefined || !w.projects.some((p) => p.id === id)) return missing();
    w.projectInstructions[id] = body.instructions;
    return ok({ instructions: body.instructions });
  });
  on("DELETE", "/api/projects/{id}/instructions", ({ params }) => {
    const id = params[0];
    if (id === undefined || !w.projects.some((p) => p.id === id)) return missing();
    Reflect.deleteProperty(w.projectInstructions, id);
    return { status: 204 };
  });
}
