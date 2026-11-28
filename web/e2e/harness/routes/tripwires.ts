/**
 * The tripwire half of the Inbox, as docs/api/http.md, "Tripwire entries",
 * writes it. "This was fine" closes an open entry; "stop the run" closes it
 * as stopped, aborting the run only if it is still going.
 */
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";
import type { TripwireEntry } from "../../../src/api/types.ts";

export function tripwireRoutes({ w, on, now, emit, event }: RouteContext): void {
  const open = () =>
    w.tripwires
      .filter((t) => t.status === "open")
      .toSorted((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id));
  on("GET", "/api/inbox/tripwires", () => ok({ entries: open() }));

  on("GET", "/api/tripwires/counts", () => {
    const names = [...new Set(w.tripwires.map((t) => t.tripwire))].toSorted();
    return ok({
      counts: names.map((name) => {
        const rows = w.tripwires.filter((t) => t.tripwire === name);
        return {
          tripwire: name,
          fired: rows.length,
          fine: rows.filter((t) => t.status === "fine").length,
        };
      }),
    });
  });

  const answer = (id: string | undefined, status: "fine" | "stopped") => {
    const entry: TripwireEntry | undefined = w.tripwires.find((t) => t.id === id);
    if (entry === undefined) return fail(404, "not_found", "no tripwire entry with that id");
    if (entry.status !== "open") return fail(409, "conflict", "this entry was already answered");
    entry.status = status;
    entry.answered_at = now();
    entry.running = false;
    emit(event("tripwire.changed", "inbox", { id: entry.id, status }));
    emit(
      event("inbox.count", "inbox", {
        pending:
          w.memories.filter((m) => m.state === "pending").length +
          w.tripwires.filter((t) => t.status === "open").length,
      }),
    );
    return ok(entry);
  };
  on("POST", "/api/inbox/tripwires/{id}/fine", ({ params }) => answer(params[0], "fine"));
  on("POST", "/api/inbox/tripwires/{id}/stop", ({ params }) => answer(params[0], "stopped"));
}
