/**
 * The order the sidebar draws the chats in: pinned ones first, and the
 * archived ones apart, for the folded Archived heading.
 */

import type { Session } from "@/api/types";

/**
 * pinnedFirst moves the pinned rows ahead of the others and keeps the order
 * within each group.
 */
export function pinnedFirst<T extends { pinned: boolean }>(rows: readonly T[]): T[] {
  return [...rows.filter((r) => r.pinned), ...rows.filter((r) => !r.pinned)];
}

/** splitArchived takes the archived sessions out of a list, each group in pinned-first order. */
export function splitArchived(sessions: readonly Session[]): {
  active: Session[];
  archived: Session[];
} {
  return {
    active: pinnedFirst(sessions.filter((s) => !s.archived)),
    archived: pinnedFirst(sessions.filter((s) => s.archived)),
  };
}
