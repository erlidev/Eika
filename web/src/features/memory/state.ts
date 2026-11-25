/** The pure part of the Memory page: filters, their wire query, labels and source links. */

import { ApiError } from "@/api/client";
import type {
  EditMemoryRequest,
  Memory,
  MemoryItem,
  MemoryListQuery,
  MemoryState,
  MemoryType,
  Session,
} from "@/api/types";

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

/** Draft is the fields of a memory the user edits. */
export type Draft = Pick<Memory, "description" | "type" | "expires" | "body">;

export const draftOf = (m: Memory): Draft => ({
  description: m.description,
  type: m.type,
  expires: m.expires,
  body: m.body,
});

export const bodyLimit = 2048;
const descriptionMin = 20;
const descriptionMax = 200;

export const byteLength = (text: string): number => new TextEncoder().encode(text).length;

/** draftProblems is what the server would refuse in a draft, by field; "" is fine. */
export function draftProblems(d: Draft): { description: string; body: string } {
  const length = Array.from(d.description).length;
  return {
    description:
      length < descriptionMin || length > descriptionMax
        ? `The description is ${String(descriptionMin)} to ${String(descriptionMax)} characters, one line.`
        : "",
    body:
      byteLength(d.body) > bodyLimit
        ? `The body is at most ${String(bodyLimit)} bytes; it is ${String(byteLength(d.body))}.`
        : "",
  };
}

/** changesOf is the edit request that turns `base` into `draft`, or null when nothing differs. */
export function changesOf(base: Memory, draft: Draft): EditMemoryRequest | null {
  const edit: EditMemoryRequest = { content_sha: base.content_sha };
  if (draft.description !== base.description) edit.description = draft.description;
  if (draft.type !== base.type) edit.type = draft.type;
  if (draft.expires !== base.expires) edit.expires = draft.expires;
  if (draft.body !== base.body) edit.body = draft.body;
  return Object.keys(edit).length > 1 ? edit : null;
}

/** isStale is a 409 that says the memory changed, as opposed to a pin limit. */
export function isStale(error: unknown): boolean {
  return (
    error instanceof ApiError && error.status === 409 && error.message.includes("changed since")
  );
}

/** isUnavailable is a 503: memory could not judge the change now, and wrote nothing. */
export function isUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 503;
}
