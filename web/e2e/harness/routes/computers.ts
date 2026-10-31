/**
 * Computers: only the list. The UI reads it to find the home computer, which
 * the harness makes for itself; the API serves no other computer route.
 */

import { ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function computerRoutes(ctx: RouteContext): void {
  const { w, on } = ctx;
  // Oldest first, as the harness lists them; the world keeps the newest first.
  on("GET", "/api/computers", () =>
    ok({ computers: w.computers.toSorted((a, b) => a.created_at.localeCompare(b.created_at)) }),
  );
}
