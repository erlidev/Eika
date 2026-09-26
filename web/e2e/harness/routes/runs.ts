/** Runs, queues, and questions. */

import type { Run } from "../../../src/api/types.ts";
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function runRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/sessions/{id}/run", ({ params }) => {
    const id = params[0] ?? "";
    const active = w.activeRuns[id];
    const last = active
      ? w.runs[active]
      : Object.values(w.runs)
          .filter((r) => r.session_id === id)
          .at(-1);
    return ok({
      session_id: id,
      active: active !== undefined,
      ...(last ? { run: last } : {}),
      pending_steering: w.pendingSteering[id] ?? [],
      pending_follow_ups: w.pendingFollowUps[id] ?? [],
      questions: w.questions.filter((q) => q.session_id === id),
      elicitations: w.elicitations.filter((e) => e.session_id === id),
    });
  });
  on("POST", "/api/sessions/{id}/messages", ({ params, body }) => {
    const session = find(w.sessions, params[0], "session");
    if ("status" in session) return session;
    const text = str(body.text);
    if (text === "") return fail(400, "invalid_request", "text is required");
    const mode = str(body.mode) || "run";
    const active = w.activeRuns[session.id];
    if (mode !== "run") {
      if (!active) return fail(409, "conflict", "no run in progress");
      const queue = mode === "steer" ? w.pendingSteering : w.pendingFollowUps;
      (queue[session.id] ??= []).push(text);
      return ok(w.runs[active], 202);
    }
    if (active) return fail(409, "conflict", "a run is already going");
    if (w.models.length === 0) return fail(409, "conflict", "no model is configured");
    const run: Run = {
      id: ctx.nextId("run"),
      session_id: session.id,
      state: "running",
      started_at: now(),
    };
    w.runs[run.id] = run;
    w.activeRuns[session.id] = run.id;
    ctx.nameSession(session, text);
    void ctx.play(session, run, text);
    return ok(run, 202);
  });
  on("POST", "/api/runs/{id}/abort", ({ params }) => {
    const run = w.runs[params[0] ?? ""];
    if (!run) return fail(404, "not_found", "run not found");
    if (run.state !== "running") return fail(409, "conflict", "run already finished");
    ctx.finish(run, "aborted");
    return ok(run);
  });
  on("POST", "/api/questions/{id}/answer", ({ params, body }) => {
    const answer = ctx.answers.get(params[0] ?? "");
    if (!answer) return fail(404, "not_found", "no run waits on that question");
    if (str(body.answer) === "") return fail(400, "invalid_request", "answer is required");
    answer(str(body.answer));
    return { status: 204 };
  });
}
