import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function scheduleRoutes({ w, on, now, nextId }: RouteContext): void {
  const find = (id: string | undefined) => w.routines.find((r) => r.id === id);
  const missing = () => fail(404, "not_found", "routine not found");
  on("GET", "/api/routines", () => ok({ routines: w.routines }));
  on("GET", "/api/routines/{id}", ({ params }) => {
    const r = find(params[0]);
    return r ? ok(r) : missing();
  });
  on("POST", "/api/routines/preview", ({ body }) => {
    if (
      !str(body.schedule_expr) ||
      !str(body.timezone) ||
      !["at", "cron"].includes(str(body.schedule_kind))
    )
      return fail(400, "invalid_request", "invalid schedule");
    const match = w.routines.find(
      (r) =>
        r.schedule_kind === body.schedule_kind &&
        r.schedule_expr === body.schedule_expr &&
        r.timezone === body.timezone,
    );
    return ok({
      schedule_words: match?.schedule_words ?? `cron ${str(body.schedule_expr)}`,
      next_five: match?.next_five ?? [],
    });
  });
  on("POST", "/api/routines", ({ body }) => {
    const r = {
      id: nextId("routine"),
      name: str(body.name),
      description: str(body.description),
      enabled: body.enabled === true,
      schedule_kind: str(body.schedule_kind) || "cron",
      schedule_expr: str(body.schedule_expr),
      timezone: str(body.timezone) || "UTC",
      schedule_words: str(body.schedule_expr),
      next_five: [],
      next_run_at: null,
      agent: str(body.agent),
      prompt: str(body.prompt),
      approved_at: now(),
      warning: { spans: [], unscanned: false, max_score: 0 },
      last_outcome: "",
      last_reason: "",
      project_id: "",
      max_runtime_seconds: 1800,
      grace_seconds: 300,
      missed: "run_once",
      created_by: "user",
      last_run_at: null,
    };
    w.routines.push(r);
    return ok(r, 201);
  });
  on("PATCH", "/api/routines/{id}", ({ params, body }) => {
    const r = find(params[0]);
    if (!r) return missing();
    Object.assign(r, body);
    if (body.enabled === true) r.approved_at = now();
    return ok(r);
  });
  on("POST", "/api/routines/{id}/approve", ({ params }) => {
    const r = find(params[0]);
    if (!r) return missing();
    r.approved_at = now();
    r.enabled = true;
    return ok(r);
  });
  on("POST", "/api/routines/{id}/disable", ({ params }) => {
    const r = find(params[0]);
    if (!r) return missing();
    r.enabled = false;
    return ok(r);
  });
  on("DELETE", "/api/routines/{id}", ({ params }) => {
    const r = find(params[0]);
    if (!r) return missing();
    w.routines.splice(w.routines.indexOf(r), 1);
    return { status: 204 };
  });
  on("GET", "/api/routines/{id}/runs", ({ params }) =>
    find(params[0]) ? ok({ runs: w.routineRuns[params[0] ?? ""] ?? [] }) : missing(),
  );
  on("GET", "/api/wakeups", () => ok({ wakeups: w.wakeups }));
  on("DELETE", "/api/wakeups/{id}", ({ params }) => {
    const index = w.wakeups.findIndex((wake) => wake.id === params[0] && wake.state === "pending");
    if (index < 0) return missing();
    w.wakeups.splice(index, 1);
    return { status: 204 };
  });
}
