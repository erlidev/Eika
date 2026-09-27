/** Runs, queues, and questions. */

import type { MessageImage, Run } from "../../../src/api/types.ts";
import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";
import { sessionConfiguration } from "../profiles.ts";
import { pathOf } from "../world.ts";

/**
 * mockImage stands in for the harness's image preparation: a PNG or JPEG is
 * kept as it is, with a PNG's size read from its header. The mock fits
 * nothing; the harness's own tests cover that.
 */
function mockImage(upload: unknown): MessageImage | undefined {
  if (typeof upload !== "object" || upload === null || !("data" in upload)) return undefined;
  const data = upload.data;
  if (typeof data !== "string") return undefined;
  let bytes: string;
  try {
    bytes = atob(data);
  } catch {
    return undefined;
  }
  const word = (at: number) =>
    ((bytes.charCodeAt(at) << 24) |
      (bytes.charCodeAt(at + 1) << 16) |
      (bytes.charCodeAt(at + 2) << 8) |
      bytes.charCodeAt(at + 3)) >>>
    0;
  if (bytes.startsWith("\x89PNG\r\n\x1a\n") && bytes.length >= 24) {
    return { media_type: "image/png", data, width: word(16), height: word(20) };
  }
  if (bytes.startsWith("\xff\xd8\xff")) {
    return { media_type: "image/jpeg", data, width: 0, height: 0 };
  }
  return undefined;
}

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
    const text = str(body.text).trim() === "" ? "" : str(body.text);
    const uploads = Array.isArray(body.images) ? (body.images as unknown[]) : [];
    if (text === "" && uploads.length === 0) {
      return fail(400, "invalid_request", "text is required");
    }
    if (uploads.length > 10) {
      return fail(400, "invalid_request", "a message carries at most 10 images");
    }
    const images: MessageImage[] = [];
    for (const [i, upload] of uploads.entries()) {
      const image = mockImage(upload);
      if (!image) {
        return fail(
          400,
          "invalid_request",
          `image ${String(i + 1)}: unsupported image format: use PNG, JPEG, GIF, or WebP`,
        );
      }
      images.push(image);
    }
    // The model a run uses is the one the request names, else the
    // session's; a running one keeps the model it started with.
    const modelName = str(body.model) || sessionConfiguration(w, session).resolved.model || "";
    const model = w.models.find((m) => m.name === modelName);
    if (images.length > 0 && model?.image_input !== true) {
      return fail(
        400,
        "invalid_request",
        `model ${modelName} does not accept images; turn on Image input for it under Settings, Models, or choose a model that has it`,
      );
    }
    const mode = str(body.mode) || "run";
    const active = w.activeRuns[session.id];
    if (mode !== "run") {
      if (!active) return fail(409, "conflict", "no run in progress");
      const queue = mode === "steer" ? w.pendingSteering : w.pendingFollowUps;
      (queue[session.id] ??= []).push({ text, images: images.length });
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
    if (text !== "") ctx.nameSession(session, text);
    void ctx.play(session, run, text, images);
    return ok(run, 202);
  });
  on("POST", "/api/sessions/{id}/compact", ({ params, body }) => {
    const session = find(w.sessions, params[0], "session");
    if ("status" in session) return session;
    if (w.activeRuns[session.id]) return fail(409, "conflict", "a run is already going");
    if (w.models.length === 0) return fail(409, "conflict", "no model is configured");
    // The harness keeps about 20,000 tokens; the mock keeps two messages, so
    // a context of two or fewer beside its summary has nothing to summarize.
    const path = pathOf(w, session);
    const last = path.findLastIndex((e) => e.kind === "compaction");
    const kept = path[last]?.compaction?.kept ?? 0;
    const context = last < 0 ? path.length : path.length - last - 1 + kept;
    if (context <= 2) {
      return fail(
        409,
        "conflict",
        "nothing to compact: the conversation fits in what a compaction keeps",
      );
    }
    const run: Run = {
      id: ctx.nextId("run"),
      session_id: session.id,
      state: "running",
      started_at: now(),
    };
    w.runs[run.id] = run;
    w.activeRuns[session.id] = run.id;
    void ctx.compact(session, run, str(body.instructions));
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
