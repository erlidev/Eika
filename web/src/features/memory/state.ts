/** The pure part of the Memory page: filters, their wire query, labels and source links. */

import type { MemoryItem, MemoryListQuery, MemoryState, MemoryType, Session } from "@/api/types";

export const memoryTypes: readonly MemoryType[] = [
  "user",
  "preference",
  "person",
  "fact",
  "procedure",
];

export const typeLabel: Record<MemoryType, string> = {
  user: "User",
  preference: "Preference",
  person: "Person",
  fact: "Fact",
  procedure: "Procedure",
};

export const stateLabel: Record<MemoryState, string> = {
  active: "Active",
  pending: "Pending",
  archived: "Archived",
};

/** Filters is what the list shows: a type, a state, pinned only, and a search; "" is any. */
export type Filters = {
  type: MemoryType | "";
  state: MemoryState | "";
  pinned: boolean;
  search: string;
};

export const noFilters: Filters = { type: "", state: "", pinned: false, search: "" };

/**
 * listQuery is the request a filter set makes. A search takes only `q`, a type and
 * `include_archived`, so its state and pinned filters are applied by `narrow`.
 */
export function listQuery(f: Filters): MemoryListQuery {
  const q = f.search.trim();
  if (q !== "") {
    return {
      q,
      ...(f.type === "" ? {} : { type: f.type }),
      include_archived: f.state === "" || f.state === "archived",
    };
  }
  return {
    ...(f.type === "" ? {} : { type: f.type }),
    ...(f.state === "" ? {} : { state: f.state }),
    ...(f.pinned ? { pinned: true } : {}),
  };
}

/** narrow applies the filters a search could not send. A plain list is already narrowed. */
export function narrow(items: MemoryItem[], f: Filters): MemoryItem[] {
  if (f.search.trim() === "") return items;
  return items.filter((m) => (f.state === "" || m.state === f.state) && (!f.pinned || m.pinned));
}

/** SourcePart is one word of a memory's source: plain text, or a link to where it was learned. */
export type SourcePart = { text: string; to?: string };

/**
 * sourceParts splits a source such as `session:ses-1 entry:e-9` into its words and
 * links a session that can be opened: a subagent in Activity, the home session in
 * Chat. An entry has no page of its own, so it stays text.
 */
export function sourceParts(source: string, sessions: Session[]): SourcePart[] {
  return source
    .split(/\s+/)
    .filter((word) => word !== "")
    .map((text) => {
      const id = /^session:(.+)$/.exec(text)?.[1];
      const session = id === undefined ? undefined : sessions.find((s) => s.id === id);
      if (session?.kind === "subagent")
        return { text, to: `/activity/${encodeURIComponent(session.id)}` };
      if (session?.kind === "home") return { text, to: "/" };
      return { text };
    });
}
