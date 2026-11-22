/** Halt: whether Eika is halted, the button that halts it, and Resume. */
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function haltRoutes(ctx: RouteContext): void {
  const { w, on, now } = ctx;
  const publish = () => {
    ctx.emit(ctx.event("halt.state", "global", { ...w.halt }));
  };
  on("GET", "/api/halt", () => ok(w.halt));
  on("POST", "/api/halt", ({ body }) => {
    const reason = str(body.reason);
    if (new TextEncoder().encode(reason).length > 200)
      return fail(400, "invalid_request", "reason is at most 200 bytes");
    if (w.halt.halted) return ok(w.halt);
    w.halt = { halted: true, at: now(), ...(reason === "" ? {} : { reason }) };
    // Halting aborts every run going, as Core does before it answers.
    for (const id of Object.values(w.activeRuns)) {
      const run = w.runs[id];
      if (run?.state === "running") ctx.finish(run, "aborted");
    }
    publish();
    return ok(w.halt);
  });
  on("POST", "/api/halt/resume", () => {
    if (!w.halt.halted) return ok(w.halt);
    w.halt = { halted: false };
    publish();
    return ok(w.halt);
  });
}
