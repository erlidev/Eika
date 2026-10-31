/**
 * Computers: only the list. The UI reads it to find the home computer, which
 * the harness makes for itself; the routes that act on a computer are
 * unmocked because the UI never calls them (see mock.test.ts).
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
